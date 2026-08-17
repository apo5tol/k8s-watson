package chat

import (
	"context"
	"errors"
	"testing"

	"k8s-watson/internal/agent"
	"k8s-watson/internal/models"
)

type fakeModel struct {
	chat func(context.Context, models.Request) (models.Response, error)
}

func (m fakeModel) Chat(ctx context.Context, request models.Request) (models.Response, error) {
	return m.chat(ctx, request)
}

func TestNewRejectsNilModel(t *testing.T) {
	if _, err := New(nil); err == nil {
		t.Error("New(nil) error = nil, want error")
	}
}

func TestServiceAskDelegatesToModel(t *testing.T) {
	type contextKey struct{}
	ctx := context.WithValue(context.Background(), contextKey{}, "request")
	model := fakeModel{chat: func(gotCtx context.Context, request models.Request) (models.Response, error) {
		if got := gotCtx.Value(contextKey{}); got != "request" {
			t.Errorf("context value = %v, want request", got)
		}
		if len(request.Messages) != 1 || request.Messages[0].Role != agent.RoleUser || request.Messages[0].Content != "show pods" {
			t.Errorf("messages = %#v, want one user message", request.Messages)
		}
		if request.Tools == nil || len(request.Tools) != 0 {
			t.Errorf("tools = %#v, want initialized empty list", request.Tools)
		}
		return models.Response{Text: "pods are healthy", ToolCalls: []agent.ToolCall{}}, nil
	}}
	service, err := New(model)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	got, err := service.Ask(ctx, "show pods")
	if err != nil {
		t.Fatalf("Ask() error = %v", err)
	}
	if got != "pods are healthy" {
		t.Errorf("Ask() = %q, want pods are healthy", got)
	}
}

func TestServiceAskReturnsModelError(t *testing.T) {
	want := context.Canceled
	service, err := New(fakeModel{chat: func(context.Context, models.Request) (models.Response, error) {
		return models.Response{}, want
	}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = service.Ask(context.Background(), "show pods")
	if !errors.Is(err, want) {
		t.Errorf("Ask() error = %v, want %v", err, want)
	}
}

func TestServiceAskRejectsToolCalls(t *testing.T) {
	service, err := New(fakeModel{chat: func(context.Context, models.Request) (models.Response, error) {
		return models.Response{ToolCalls: []agent.ToolCall{{ID: "call-1", Name: "kubectl", Arguments: []byte(`{}`)}}}, nil
	}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = service.Ask(context.Background(), "show pods")
	if err == nil || err.Error() != "chat service does not support tool calls" {
		t.Errorf("Ask() error = %v, want unsupported tool calls error", err)
	}
}
