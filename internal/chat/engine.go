package chat

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"time"

	"k8s-watson/internal/agent"
	"k8s-watson/internal/models"
)

type Config struct {
	MaxHistoryChars int
	MaxInputBytes   int
	MaxIterations   int
}

type Engine struct {
	model  models.Model
	config Config
	logger *slog.Logger

	mu         sync.Mutex
	wg         sync.WaitGroup
	turns      []Turn
	state      State
	activeID   TurnID
	nextID     TurnID
	generation uint64
	cancel     context.CancelFunc
	done       chan struct{}
	closed     bool
	events     chan Event
}

func New(model models.Model, config Config, logger *slog.Logger) (*Engine, error) {
	if model == nil {
		return nil, ErrModelRequired
	}
	if config.MaxHistoryChars <= 0 || config.MaxInputBytes <= 0 || config.MaxIterations <= 0 {
		return nil, ErrInvalidConfig
	}
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	return &Engine{
		model:  model,
		config: config,
		logger: logger,
		turns:  []Turn{},
		state:  StateIdle,
		events: make(chan Event, 32),
	}, nil
}

func (e *Engine) Submit(question string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.closed {
		return ErrClosed
	}
	if e.state.Active() {
		return ErrBusy
	}
	if len([]byte(question)) > e.config.MaxInputBytes {
		return ErrInputTooLong
	}

	e.nextID++
	turn := Turn{
		ID:    e.nextID,
		State: StateCallingModel,
		Messages: []agent.Message{{
			Role:    agent.RoleUser,
			Content: question,
		}},
		Entries: []Entry{{Kind: EntryUser, Text: question, TurnID: e.nextID}},
	}
	e.turns = append(e.turns, turn)
	e.state = StateCallingModel
	e.activeID = turn.ID
	e.generation++
	generation := e.generation
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	e.cancel = cancel
	e.done = done
	request := models.Request{
		Messages: contextMessages(e.turns, turn.ID, e.config.MaxHistoryChars),
		Tools:    []agent.ToolDefinition{},
	}
	e.publishLocked(EventHistoryChanged, turn.ID, nil)
	e.logger.Info("chat turn started", "event", "chat_turn_started", "turn_id", turn.ID)

	e.wg.Add(1)
	go e.callModel(ctx, done, generation, turn.ID, request)
	return nil
}

func (e *Engine) callModel(ctx context.Context, done chan struct{}, generation uint64, turnID TurnID, request models.Request) {
	defer e.wg.Done()
	startedAt := time.Now()
	response, err := e.model.Chat(ctx, request)
	close(done)

	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed || e.generation != generation || e.activeID != turnID || e.state != StateCallingModel {
		e.logger.Debug("ignored stale chat result", "event", "chat_stale_result", "turn_id", turnID)
		return
	}
	e.cancel = nil
	e.done = nil
	if err != nil {
		e.finishLocked(turnID, StateFailed, EntryError, err.Error(), err, EventTurnFailed)
		return
	}
	if len(response.ToolCalls) != 0 {
		e.finishLocked(turnID, StateFailed, EntryError, ErrToolCallsUnsupported.Error(), ErrToolCallsUnsupported, EventTurnFailed)
		return
	}

	turn := &e.turns[len(e.turns)-1]
	turn.Messages = append(turn.Messages, agent.Message{Role: agent.RoleAssistant, Content: response.Text})
	e.finishLocked(turnID, StateCompleted, EntryAssistant, response.Text, nil, EventTurnCompleted)
	e.logger.Info("chat turn completed", "event", "chat_turn_completed", "turn_id", turnID, "duration", time.Since(startedAt))
}

func (e *Engine) finishLocked(turnID TurnID, state State, kind EntryKind, text string, err error, event EventKind) {
	turn := &e.turns[len(e.turns)-1]
	turn.State = state
	turn.Err = err
	turn.Entries = append(turn.Entries, Entry{Kind: kind, Text: text, TurnID: turnID})
	e.state = state
	e.activeID = 0
	e.publishLocked(event, turnID, err)
}

func (e *Engine) Cancel() bool {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.state.Active() || e.cancel == nil {
		return false
	}
	turnID := e.activeID
	e.generation++
	e.cancel()
	e.cancel = nil
	e.done = nil
	e.finishLocked(turnID, StateCancelled, EntryCancelled, "Request cancelled.", nil, EventTurnCancelled)
	e.logger.Info("chat turn cancelled", "event", "chat_turn_cancelled", "turn_id", turnID)
	return true
}

func (e *Engine) Clear() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.closed {
		return ErrClosed
	}
	if e.state.Active() {
		return ErrActiveTurn
	}
	e.turns = []Turn{}
	e.state = StateIdle
	e.activeID = 0
	e.generation++
	e.publishLocked(EventHistoryCleared, 0, nil)
	e.logger.Info("chat history cleared", "event", "chat_history_cleared")
	return nil
}

func (e *Engine) Snapshot() Snapshot {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.snapshotLocked()
}

func (e *Engine) Events() <-chan Event {
	return e.events
}

func (e *Engine) Close() {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return
	}
	e.closed = true
	cancel := e.cancel
	e.generation++
	e.cancel = nil
	e.done = nil
	e.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	e.wg.Wait()
}

func (e *Engine) publishLocked(kind EventKind, turnID TurnID, err error) {
	event := Event{Kind: kind, TurnID: turnID, Snapshot: e.snapshotLocked(), Err: err}
	select {
	case e.events <- event:
	default:
		e.logger.Warn("chat event dropped", "event", "chat_event_dropped", "event_kind", kind)
	}
}

func (e *Engine) snapshotLocked() Snapshot {
	entries := []Entry{}
	for _, turn := range e.turns {
		entries = append(entries, turn.Entries...)
	}
	return Snapshot{State: e.state, TurnID: e.activeID, Entries: entries}
}
