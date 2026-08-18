package chat

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"k8s-watson/internal/agent"
	"k8s-watson/internal/models"
)

type fakeModel struct {
	chat func(context.Context, models.Request) (models.Response, error)
}

func (m fakeModel) Chat(ctx context.Context, request models.Request) (models.Response, error) {
	return m.chat(ctx, request)
}

func testEngine(t *testing.T, model models.Model) *Engine {
	t.Helper()
	engine, err := New(model, Config{MaxHistoryChars: 100, MaxInputBytes: 16 * 1024, MaxIterations: 8}, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(engine.Close)
	return engine
}

func waitEvent(t *testing.T, engine *Engine) Event {
	t.Helper()
	select {
	case event := <-engine.Events():
		return event
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for chat event")
		return Event{}
	}
}

func TestNewRejectsInvalidDependencies(t *testing.T) {
	if _, err := New(nil, Config{MaxHistoryChars: 1, MaxInputBytes: 1, MaxIterations: 1}, nil); !errors.Is(err, ErrModelRequired) {
		t.Errorf("New(nil) error = %v, want ErrModelRequired", err)
	}
	if _, err := New(fakeModel{}, Config{}, nil); !errors.Is(err, ErrInvalidConfig) {
		t.Errorf("New() error = %v, want ErrInvalidConfig", err)
	}
}

func TestEngineRetainsCompletedTurnsInModelContext(t *testing.T) {
	var requests []models.Request
	var mu sync.Mutex
	engine := testEngine(t, fakeModel{chat: func(_ context.Context, request models.Request) (models.Response, error) {
		mu.Lock()
		requests = append(requests, request)
		mu.Unlock()
		return models.Response{Text: "answer"}, nil
	}})
	if err := engine.Submit("first"); err != nil {
		t.Fatalf("Submit(first) error = %v", err)
	}
	waitEvent(t, engine)
	waitEvent(t, engine)
	if err := engine.Submit("second"); err != nil {
		t.Fatalf("Submit(second) error = %v", err)
	}
	waitEvent(t, engine)
	waitEvent(t, engine)

	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(requests))
	}
	got := requests[1].Messages
	if len(got) != 4 || got[0].Role != agent.RoleSystem || got[1].Content != "first" || got[2].Content != "answer" || got[3].Content != "second" {
		t.Errorf("second request messages = %#v, want system plus completed first turn and second question", got)
	}
	if requests[1].Tools == nil || len(requests[1].Tools) != 0 {
		t.Errorf("tools = %#v, want initialized empty slice", requests[1].Tools)
	}
}

func TestEnginePublishesLifecycleEvents(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	engine := testEngine(t, fakeModel{chat: func(context.Context, models.Request) (models.Response, error) {
		close(started)
		<-release
		return models.Response{Text: "answer"}, nil
	}})

	if err := engine.Submit("question"); err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	startedEvent := waitEvent(t, engine)
	assertStartedEvent(t, startedEvent)

	<-started
	close(release)
	completedEvent := waitEvent(t, engine)
	assertCompletedEvent(t, completedEvent, startedEvent.TurnID)
}

func assertStartedEvent(t *testing.T, event Event) {
	t.Helper()
	if event.Kind != EventHistoryChanged || event.TurnID == 0 {
		t.Errorf("started event = %#v, want history change with a turn ID", event)
	}
	if event.Snapshot.State != StateCallingModel || event.Snapshot.TurnID != event.TurnID {
		t.Errorf("started snapshot = %#v, want active calling-model turn", event.Snapshot)
	}
	if len(event.Snapshot.Entries) != 1 || event.Snapshot.Entries[0].Kind != EntryUser {
		t.Errorf("started entries = %#v, want one user entry", event.Snapshot.Entries)
	}
}

func assertCompletedEvent(t *testing.T, event Event, turnID TurnID) {
	t.Helper()
	if event.Kind != EventTurnCompleted || event.TurnID != turnID {
		t.Errorf("completed event = %#v, want completion for the active turn", event)
	}
	if event.Snapshot.State != StateCompleted || event.Snapshot.TurnID != 0 {
		t.Errorf("completed snapshot = %#v, want completed turn without active ID", event.Snapshot)
	}
	if len(event.Snapshot.Entries) != 2 || event.Snapshot.Entries[1].Kind != EntryAssistant {
		t.Errorf("completed entries = %#v, want assistant answer", event.Snapshot.Entries)
	}
}

func TestEngineReportsModelFailureAndAcceptsNextQuestion(t *testing.T) {
	modelErr := errors.New("model unavailable")
	var calls int
	engine := testEngine(t, fakeModel{chat: func(context.Context, models.Request) (models.Response, error) {
		calls++
		if calls == 1 {
			return models.Response{}, modelErr
		}
		return models.Response{Text: "recovered"}, nil
	}})

	if err := engine.Submit("first"); err != nil {
		t.Fatalf("Submit(first) error = %v", err)
	}
	waitEvent(t, engine)
	failedEvent := waitEvent(t, engine)
	if failedEvent.Kind != EventTurnFailed || !errors.Is(failedEvent.Err, modelErr) {
		t.Errorf("failed event = %#v, want model failure", failedEvent)
	}
	if failedEvent.Snapshot.State != StateFailed || failedEvent.Snapshot.TurnID != 0 {
		t.Errorf("failed snapshot = %#v, want failed turn without active ID", failedEvent.Snapshot)
	}

	if err := engine.Submit("second"); err != nil {
		t.Fatalf("Submit(second) error = %v, want recovery after failure", err)
	}
	waitEvent(t, engine)
	completedEvent := waitEvent(t, engine)
	if completedEvent.Kind != EventTurnCompleted || completedEvent.Snapshot.State != StateCompleted {
		t.Errorf("recovery event = %#v, want completed turn", completedEvent)
	}
}

func TestEngineRejectsToolCalls(t *testing.T) {
	engine := testEngine(t, fakeModel{chat: func(context.Context, models.Request) (models.Response, error) {
		return models.Response{ToolCalls: []agent.ToolCall{{ID: "call-1", Name: "kubectl", Arguments: []byte(`{}`)}}}, nil
	}})
	if err := engine.Submit("show pods"); err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	waitEvent(t, engine)
	event := waitEvent(t, engine)
	if !errors.Is(event.Err, ErrToolCallsUnsupported) || event.Kind != EventTurnFailed {
		t.Errorf("event = %#v, want unsupported tool call failure", event)
	}
}

func TestEngineCancellationIgnoresLateResult(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan struct{})
	engine := testEngine(t, fakeModel{chat: func(context.Context, models.Request) (models.Response, error) {
		close(started)
		<-release
		close(finished)
		return models.Response{Text: "late answer"}, nil
	}})
	if err := engine.Submit("show pods"); err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	waitEvent(t, engine)
	<-started
	if !engine.Cancel() {
		t.Fatal("Cancel() = false, want true")
	}
	cancelled := waitEvent(t, engine)
	if cancelled.Kind != EventTurnCancelled {
		t.Errorf("event kind = %q, want cancellation", cancelled.Kind)
	}
	close(release)
	<-finished
	snapshot := engine.Snapshot()
	if snapshot.State != StateCancelled || len(snapshot.Entries) != 2 || snapshot.Entries[1].Kind != EntryCancelled {
		t.Errorf("snapshot = %#v, want cancelled turn without late answer", snapshot)
	}
}

func TestEngineIgnoresCancelledResultAfterNextTurnStarts(t *testing.T) {
	firstStarted := make(chan struct{})
	firstRelease := make(chan struct{})
	firstFinished := make(chan struct{})
	engine := testEngine(t, fakeModel{chat: func(_ context.Context, request models.Request) (models.Response, error) {
		if request.Messages[len(request.Messages)-1].Content == "first" {
			close(firstStarted)
			<-firstRelease
			close(firstFinished)
			return models.Response{Text: "late answer"}, nil
		}
		return models.Response{Text: "second answer"}, nil
	}})

	if err := engine.Submit("first"); err != nil {
		t.Fatalf("Submit(first) error = %v", err)
	}
	waitEvent(t, engine)
	<-firstStarted
	if !engine.Cancel() {
		t.Fatal("Cancel() = false, want true")
	}
	waitEvent(t, engine)

	if err := engine.Submit("second"); err != nil {
		t.Fatalf("Submit(second) error = %v", err)
	}
	waitEvent(t, engine)
	completedEvent := waitEvent(t, engine)
	close(firstRelease)
	<-firstFinished

	snapshot := engine.Snapshot()
	if snapshot.State != StateCompleted || len(snapshot.Entries) != 4 {
		t.Errorf("snapshot = %#v, want completed second turn without late entry", snapshot)
	}
	if completedEvent.Snapshot.Entries[3].Text != "second answer" {
		t.Errorf("second response = %#v, want second answer", completedEvent.Snapshot.Entries)
	}
}

func TestEngineCancelStopsContextAndIsIdempotent(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})
	engine := testEngine(t, fakeModel{chat: func(ctx context.Context, _ models.Request) (models.Response, error) {
		close(started)
		<-ctx.Done()
		close(cancelled)
		return models.Response{}, ctx.Err()
	}})

	if engine.Cancel() {
		t.Fatal("Cancel() = true in idle state, want false")
	}
	if err := engine.Submit("question"); err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	waitEvent(t, engine)
	<-started
	if !engine.Cancel() {
		t.Fatal("Cancel() = false, want true")
	}
	waitEvent(t, engine)
	<-cancelled
	if engine.Cancel() {
		t.Fatal("second Cancel() = true, want false")
	}
}

func TestEngineClearAndInputLimit(t *testing.T) {
	engine := testEngine(t, fakeModel{chat: func(context.Context, models.Request) (models.Response, error) {
		return models.Response{Text: "answer"}, nil
	}})
	if err := engine.Submit("first"); err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	waitEvent(t, engine)
	waitEvent(t, engine)
	if err := engine.Clear(); err != nil {
		t.Fatalf("Clear() error = %v", err)
	}
	if snapshot := engine.Snapshot(); len(snapshot.Entries) != 0 || snapshot.State != StateIdle {
		t.Errorf("snapshot after clear = %#v, want idle empty history", snapshot)
	}
	if err := engine.Submit(string(make([]byte, 16*1024+1))); !errors.Is(err, ErrInputTooLong) {
		t.Errorf("Submit(long input) error = %v, want ErrInputTooLong", err)
	}
}

func TestEngineEnforcesInputByteLimit(t *testing.T) {
	var requests int
	engine, err := New(fakeModel{chat: func(context.Context, models.Request) (models.Response, error) {
		requests++
		return models.Response{Text: "answer"}, nil
	}}, Config{MaxHistoryChars: 100, MaxInputBytes: 4, MaxIterations: 8}, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(engine.Close)

	if err := engine.Submit("éé"); err != nil {
		t.Fatalf("Submit() error = %v, want exact byte limit accepted", err)
	}
	waitEvent(t, engine)
	waitEvent(t, engine)
	if err := engine.Submit(strings.Repeat("é", 3)); !errors.Is(err, ErrInputTooLong) {
		t.Errorf("Submit() error = %v, want ErrInputTooLong", err)
	}
	if requests != 1 {
		t.Errorf("model requests = %d, want rejected input not sent to model", requests)
	}
}

func TestEngineRejectsBusySubmitAndActiveClear(t *testing.T) {
	started := make(chan struct{})
	engine := testEngine(t, fakeModel{chat: func(ctx context.Context, _ models.Request) (models.Response, error) {
		close(started)
		<-ctx.Done()
		return models.Response{}, ctx.Err()
	}})

	if err := engine.Submit("first"); err != nil {
		t.Fatalf("Submit(first) error = %v", err)
	}
	waitEvent(t, engine)
	<-started
	if err := engine.Submit("second"); !errors.Is(err, ErrBusy) {
		t.Errorf("Submit(second) error = %v, want ErrBusy", err)
	}
	if err := engine.Clear(); !errors.Is(err, ErrActiveTurn) {
		t.Errorf("Clear() error = %v, want ErrActiveTurn", err)
	}
	engine.Cancel()
	waitEvent(t, engine)
}

func TestEngineCloseCancelsActiveRequest(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})
	engine := testEngine(t, fakeModel{chat: func(ctx context.Context, _ models.Request) (models.Response, error) {
		close(started)
		<-ctx.Done()
		close(cancelled)
		return models.Response{}, ctx.Err()
	}})

	if err := engine.Submit("question"); err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	waitEvent(t, engine)
	<-started
	engine.Close()
	<-cancelled
	engine.Close()
	if err := engine.Submit("another question"); !errors.Is(err, ErrClosed) {
		t.Errorf("Submit() error = %v, want ErrClosed", err)
	}
	if err := engine.Clear(); !errors.Is(err, ErrClosed) {
		t.Errorf("Clear() error = %v, want ErrClosed", err)
	}
}
