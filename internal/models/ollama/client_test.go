package ollama

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"
)

func TestClientChatReturnsCompletedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertChatRequest(t, r)
		writeResponse(t, w, chatResponse{Message: chatMessage{Role: "assistant", Content: "  pods are healthy  "}, Done: true})
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, time.Second)
	got, err := client.Chat(context.Background(), "show pods")
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if got != "pods are healthy" {
		t.Errorf("Chat() = %q, want %q", got, "pods are healthy")
	}
}

func TestClientChatRetriesTemporaryErrors(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < maxAttempts {
			http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		writeResponse(t, w, chatResponse{Message: chatMessage{Content: "recovered"}, Done: true})
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, time.Second)
	var sleeps atomic.Int32
	client.sleep = func(context.Context, time.Duration) error {
		sleeps.Add(1)
		return nil
	}

	got, err := client.Chat(context.Background(), "show pods")
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if got != "recovered" {
		t.Errorf("Chat() = %q, want recovered", got)
	}
	if got := calls.Load(); got != maxAttempts {
		t.Errorf("requests = %d, want %d", got, maxAttempts)
	}
	if got := sleeps.Load(); got != maxAttempts-1 {
		t.Errorf("retry delays = %d, want %d", got, maxAttempts-1)
	}
}

func TestClientChatTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, 10*time.Millisecond)
	client.sleep = func(context.Context, time.Duration) error { return nil }
	_, err := client.Chat(context.Background(), "show pods")
	assertErrorKind(t, err, ErrorTimeout)
}

func TestClientChatCancellationStopsRetry(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, time.Second)
	retrying := make(chan struct{})
	client.sleep = func(ctx context.Context, _ time.Duration) error {
		close(retrying)
		<-ctx.Done()
		return ctx.Err()
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errs := make(chan error, 1)
	go func() { _, err := client.Chat(ctx, "show pods"); errs <- err }()
	<-retrying
	cancel()

	assertErrorKind(t, <-errs, ErrorCanceled)
	if got := calls.Load(); got != 1 {
		t.Errorf("requests = %d, want 1", got)
	}
}

func TestClientChatRejectsInvalidResponses(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "empty content", body: `{"message":{"content":"  "},"done":true}`},
		{name: "incomplete", body: `{"message":{"content":"working"},"done":false}`},
		{name: "invalid JSON", body: `{`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()

			client := newTestClient(t, server.URL, time.Second)
			_, err := client.Chat(context.Background(), "show pods")
			assertErrorKind(t, err, ErrorInvalidResponse)
		})
	}
}

func TestClientChatDoesNotRetryRequestErrors(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "model not found", http.StatusNotFound)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, time.Second)
	client.sleep = func(context.Context, time.Duration) error {
		t.Fatal("retry delay called for request error")
		return nil
	}
	_, err := client.Chat(context.Background(), "show pods")
	assertErrorKind(t, err, ErrorRequest)
	if got := calls.Load(); got != 1 {
		t.Errorf("requests = %d, want 1", got)
	}
}

func newTestClient(t *testing.T, endpoint string, timeout time.Duration) *Client {
	t.Helper()
	parsedEndpoint, err := url.Parse(endpoint)
	if err != nil {
		t.Fatalf("parse endpoint: %v", err)
	}
	client, err := New(parsedEndpoint, "llama3", timeout, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return client
}

func assertChatRequest(t *testing.T, r *http.Request) {
	t.Helper()
	if r.URL.Path != "/api/chat" {
		t.Errorf("path = %q, want /api/chat", r.URL.Path)
	}
	if r.Method != http.MethodPost {
		t.Errorf("method = %q, want POST", r.Method)
	}

	var request chatRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if request.Model != "llama3" || len(request.Messages) != 1 || request.Messages[0] != (chatMessage{Role: "user", Content: "show pods"}) {
		t.Errorf("request = %#v, want model and one user message", request)
	}
	if request.Stream == nil || *request.Stream || request.Think == nil || *request.Think {
		t.Errorf("request stream = %v, think = %v, want both false", request.Stream, request.Think)
	}
}

func writeResponse(t *testing.T, w http.ResponseWriter, response chatResponse) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		t.Errorf("encode response: %v", err)
	}
}

func assertErrorKind(t *testing.T, err error, want ErrorKind) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want %q", want)
	}
	var ollamaErr *Error
	if !errors.As(err, &ollamaErr) {
		t.Fatalf("error = %T (%v), want *Error", err, err)
	}
	if ollamaErr.Kind != want {
		t.Errorf("error kind = %q, want %q", ollamaErr.Kind, want)
	}
}
