package chat

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"k8s-watson/internal/agent"
	"k8s-watson/internal/models"
	"k8s-watson/internal/tools"
)

type Config struct {
	MaxHistoryChars int
	MaxInputBytes   int
	MaxIterations   int
}

type Engine struct {
	model    models.Model
	registry *tools.Registry
	config   Config
	logger   *slog.Logger

	mu         sync.Mutex
	wg         sync.WaitGroup
	turns      []Turn
	state      State
	activeID   TurnID
	nextID     TurnID
	generation uint64
	cancel     context.CancelFunc
	closed     bool
	events     chan Event
}

func New(model models.Model, registry *tools.Registry, config Config, logger *slog.Logger) (*Engine, error) {
	if model == nil {
		return nil, ErrModelRequired
	}
	if registry == nil {
		return nil, tools.ErrRegistryRequired
	}
	if config.MaxHistoryChars <= 0 || config.MaxInputBytes <= 0 || config.MaxIterations <= 0 {
		return nil, ErrInvalidConfig
	}
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	return &Engine{
		model:    model,
		registry: registry,
		config:   config,
		logger:   logger,
		turns:    []Turn{},
		state:    StateIdle,
		events:   make(chan Event, 32),
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
	e.cancel = cancel
	e.publishLocked(EventHistoryChanged, turn.ID, nil)
	e.logger.Info("chat turn started", "event", "chat_turn_started", "turn_id", turn.ID)

	e.wg.Add(1)
	go e.runTurn(ctx, generation, turn.ID)
	return nil
}

func (e *Engine) runTurn(ctx context.Context, generation uint64, turnID TurnID) {
	defer e.wg.Done()
	for iteration := 0; ; iteration++ {
		if iteration >= e.config.MaxIterations {
			e.failTurn(generation, turnID, ErrMaxIterations)
			return
		}

		request, ok := e.modelRequest(generation, turnID)
		if !ok {
			return
		}
		startedAt := time.Now()
		response, err := e.model.Chat(ctx, request)
		if err != nil {
			e.failTurn(generation, turnID, err)
			return
		}
		if !e.handleModelResponse(generation, turnID, response, startedAt) {
			return
		}
		if len(response.ToolCalls) == 0 {
			return
		}

		for _, call := range response.ToolCalls {
			if !e.runTool(ctx, generation, turnID, call) {
				return
			}
		}
	}
}

func (e *Engine) modelRequest(generation uint64, turnID TurnID) (models.Request, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.isActiveLocked(generation, turnID) {
		return models.Request{}, false
	}
	if e.state != StateCallingModel {
		e.state = StateCallingModel
		e.publishLocked(EventStateChanged, turnID, nil)
	}
	return models.Request{
		Messages: contextMessages(e.turns, turnID, e.config.MaxHistoryChars),
		Tools:    e.registry.Definitions(),
	}, true
}

func (e *Engine) handleModelResponse(generation uint64, turnID TurnID, response models.Response, startedAt time.Time) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.isActiveLocked(generation, turnID) || e.state != StateCallingModel {
		e.logger.Debug("ignored stale chat result", "event", "chat_stale_result", "turn_id", turnID)
		return false
	}

	turn := &e.turns[len(e.turns)-1]
	turn.Messages = append(turn.Messages, agent.Message{
		Role:      agent.RoleAssistant,
		Content:   response.Text,
		ToolCalls: response.ToolCalls,
	})
	if len(response.ToolCalls) == 0 {
		e.finishLocked(turnID, StateCompleted, EntryAssistant, response.Text, nil, EventTurnCompleted)
		e.logger.Info("chat turn completed", "event", "chat_turn_completed", "turn_id", turnID, "duration", time.Since(startedAt))
		return true
	}

	e.state = StateToolProposed
	e.publishLocked(EventStateChanged, turnID, nil)
	return true
}

func (e *Engine) runTool(ctx context.Context, generation uint64, turnID TurnID, call agent.ToolCall) bool {
	tool, err := e.registry.Find(call.Name)
	if err != nil {
		e.failTurn(generation, turnID, err)
		return false
	}

	prepared, err := tool.Prepare(ctx, call)
	if err != nil {
		e.failTurn(generation, turnID, fmt.Errorf("prepare tool %q: %w", call.Name, err))
		return false
	}

	e.mu.Lock()
	if !e.isActiveLocked(generation, turnID) {
		e.mu.Unlock()
		return false
	}
	e.state = StateRunningTool
	e.publishLocked(EventStateChanged, turnID, nil)
	e.mu.Unlock()

	result, err := prepared.Execute(ctx)
	if err != nil {
		e.failTurn(generation, turnID, fmt.Errorf("execute tool %q: %w", call.Name, err))
		return false
	}
	result.ToolCallID = call.ID
	result.ToolName = call.Name

	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.isActiveLocked(generation, turnID) || e.state != StateRunningTool {
		return false
	}
	turn := &e.turns[len(e.turns)-1]
	turn.Messages = append(turn.Messages, agent.Message{Role: agent.RoleTool, ToolResult: &result})
	e.state = StateToolProposed
	e.publishLocked(EventHistoryChanged, turnID, nil)
	return true
}

func (e *Engine) failTurn(generation uint64, turnID TurnID, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.isActiveLocked(generation, turnID) {
		return
	}
	e.finishLocked(turnID, StateFailed, EntryError, err.Error(), err, EventTurnFailed)
}

func (e *Engine) isActiveLocked(generation uint64, turnID TurnID) bool {
	return !e.closed && e.generation == generation && e.activeID == turnID && e.state.Active()
}

func (e *Engine) finishLocked(turnID TurnID, state State, kind EntryKind, text string, err error, event EventKind) {
	turn := &e.turns[len(e.turns)-1]
	turn.State = state
	turn.Err = err
	turn.Entries = append(turn.Entries, Entry{Kind: kind, Text: text, TurnID: turnID})
	e.state = state
	e.activeID = 0
	e.cancel = nil
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
