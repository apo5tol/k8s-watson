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
	"k8s-watson/internal/tools"
)

type fakeModel struct {
	chat func(context.Context, models.Request) (models.Response, error)
}

func (m fakeModel) Chat(ctx context.Context, request models.Request) (models.Response, error) {
	return m.chat(ctx, request)
}

type fakeTool struct {
	definition agent.ToolDefinition
	prepare    func(context.Context, agent.ToolCall) (tools.PreparedCall, error)
}

func (t fakeTool) Definition() agent.ToolDefinition {
	return t.definition
}

func (t fakeTool) Prepare(ctx context.Context, call agent.ToolCall) (tools.PreparedCall, error) {
	return t.prepare(ctx, call)
}

type fakePreparedCall struct {
	execute func(context.Context) (agent.ToolResult, error)
}

func (c fakePreparedCall) Execute(ctx context.Context) (agent.ToolResult, error) {
	return c.execute(ctx)
}

func testEngine(t *testing.T, model models.Model, registeredTools ...tools.Tool) *Engine {
	t.Helper()
	registry, err := tools.NewRegistry(registeredTools...)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	engine, err := New(model, registry, Config{MaxHistoryChars: 100, MaxInputBytes: 16 * 1024, MaxIterations: 8}, nil)
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
	registry, err := tools.NewRegistry()
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	if _, err := New(nil, registry, Config{MaxHistoryChars: 1, MaxInputBytes: 1, MaxIterations: 1}, nil); !errors.Is(err, ErrModelRequired) {
		t.Errorf("New(nil) error = %v, want ErrModelRequired", err)
	}
	if _, err := New(fakeModel{}, registry, Config{}, nil); !errors.Is(err, ErrInvalidConfig) {
		t.Errorf("New() error = %v, want ErrInvalidConfig", err)
	}
	if _, err := New(fakeModel{}, nil, Config{MaxHistoryChars: 1, MaxInputBytes: 1, MaxIterations: 1}, nil); !errors.Is(err, tools.ErrRegistryRequired) {
		t.Errorf("New() error = %v, want tools.ErrRegistryRequired", err)
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

func TestEngineRunsToolCallsAndReturnsResultsToModel(t *testing.T) {
	var requests []models.Request
	var executions int
	tool := fakeTool{
		definition: agent.ToolDefinition{Name: "fake", Description: "fake tool"},
		prepare: func(_ context.Context, call agent.ToolCall) (tools.PreparedCall, error) {
			if string(call.Arguments) != `{"value":"pods"}` {
				t.Errorf("arguments = %s, want pods", call.Arguments)
			}
			return fakePreparedCall{execute: func(context.Context) (agent.ToolResult, error) {
				executions++
				return agent.ToolResult{Content: "pod list"}, nil
			}}, nil
		},
	}
	engine := testEngine(t, fakeModel{chat: func(_ context.Context, request models.Request) (models.Response, error) {
		requests = append(requests, request)
		if len(requests) == 1 {
			return models.Response{ToolCalls: []agent.ToolCall{{ID: "call-1", Name: "fake", Arguments: []byte(`{"value":"pods"}`)}}}, nil
		}
		return models.Response{Text: "pods are healthy"}, nil
	}}, tool)
	if err := engine.Submit("show pods"); err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	waitEvent(t, engine)
	waitForCompletion(t, engine)
	assertToolLoop(t, executions, requests)
}

func waitForCompletion(t *testing.T, engine *Engine) {
	t.Helper()
	for event := waitEvent(t, engine); event.Kind != EventTurnCompleted; event = waitEvent(t, engine) {
	}
}

func assertToolLoop(t *testing.T, executions int, requests []models.Request) {
	t.Helper()
	if executions != 1 || len(requests) != 2 {
		t.Fatalf("executions = %d, requests = %d, want one execution and two requests", executions, len(requests))
	}
	assertToolDefinition(t, requests[0])
	assertToolResult(t, requests[1].Messages)
}

func assertToolDefinition(t *testing.T, request models.Request) {
	t.Helper()
	if len(request.Tools) != 1 || request.Tools[0].Name != "fake" {
		t.Errorf("first request tools = %#v, want fake definition", request.Tools)
	}
}

func assertToolResult(t *testing.T, messages []agent.Message) {
	t.Helper()
	if len(messages) != 4 || messages[2].Role != agent.RoleAssistant || len(messages[2].ToolCalls) != 1 {
		t.Fatalf("second request messages = %#v, want assistant tool call", messages)
	}
	result := messages[3].ToolResult
	if messages[3].Role != agent.RoleTool || result == nil || result.ToolCallID != "call-1" || result.ToolName != "fake" || result.Content != "pod list" {
		t.Errorf("tool result = %#v, want matched fake result", result)
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
	}}, mustRegistry(t), Config{MaxHistoryChars: 100, MaxInputBytes: 4, MaxIterations: 8}, nil)
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

func mustRegistry(t *testing.T, registeredTools ...tools.Tool) *tools.Registry {
	t.Helper()
	registry, err := tools.NewRegistry(registeredTools...)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	return registry
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
