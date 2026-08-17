package chat

import (
	"context"
	"errors"
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
	engine := testEngine(t, fakeModel{chat: func(context.Context, models.Request) (models.Response, error) {
		close(started)
		<-release
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
	time.Sleep(10 * time.Millisecond)
	snapshot := engine.Snapshot()
	if snapshot.State != StateCancelled || len(snapshot.Entries) != 2 || snapshot.Entries[1].Kind != EntryCancelled {
		t.Errorf("snapshot = %#v, want cancelled turn without late answer", snapshot)
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
