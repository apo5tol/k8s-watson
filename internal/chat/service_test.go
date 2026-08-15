package chat

import (
	"context"
	"errors"
	"testing"
)

type fakeModel struct {
	chat func(context.Context, string) (string, error)
}

func (m fakeModel) Chat(ctx context.Context, question string) (string, error) {
	return m.chat(ctx, question)
}

func TestNewRejectsNilModel(t *testing.T) {
	if _, err := New(nil); err == nil {
		t.Error("New(nil) error = nil, want error")
	}
}

func TestServiceAskDelegatesToModel(t *testing.T) {
	type contextKey struct{}
	ctx := context.WithValue(context.Background(), contextKey{}, "request")
	model := fakeModel{chat: func(gotCtx context.Context, question string) (string, error) {
		if got := gotCtx.Value(contextKey{}); got != "request" {
			t.Errorf("context value = %v, want request", got)
		}
		if question != "show pods" {
			t.Errorf("question = %q, want show pods", question)
		}
		return "pods are healthy", nil
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
	service, err := New(fakeModel{chat: func(context.Context, string) (string, error) {
		return "", want
	}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = service.Ask(context.Background(), "show pods")
	if !errors.Is(err, want) {
		t.Errorf("Ask() error = %v, want %v", err, want)
	}
}
