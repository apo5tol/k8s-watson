package ollama

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"k8s-watson/internal/agent"
	"k8s-watson/internal/models"
)

func TestClientChatReturnsCompletedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertChatRequest(t, r)
		writeResponse(t, w, chatResponse{Message: chatMessage{Role: "assistant", Content: "  pods are healthy  "}, Done: true})
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, time.Second)
	got, err := client.Chat(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if got.Text != "pods are healthy" {
		t.Errorf("Chat() text = %q, want %q", got.Text, "pods are healthy")
	}
}

func TestClientChatConvertsAgentProtocol(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request chatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		assertProtocolRequest(t, request)
		writeResponse(t, w, chatResponse{
			Message: chatMessage{
				Content: "I will inspect the pods.",
				ToolCalls: []chatToolCall{
					{Function: chatToolFunction{Name: "kubectl", Arguments: []byte(`{"verb":"get","args":["pods"]}`)}},
					{Function: chatToolFunction{Name: "kubectl", Arguments: []byte(`{"verb":"describe","args":["pod","api"]}`)}},
				},
			},
			Done: true,
		})
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, time.Second)
	response, err := client.Chat(context.Background(), models.Request{
		Messages: []agent.Message{
			{Role: agent.RoleSystem, Content: "use tools"},
			{Role: agent.RoleUser, Content: "show pods"},
			{Role: agent.RoleAssistant, ToolCalls: []agent.ToolCall{{ID: "call-1", Name: "kubectl", Arguments: []byte(`{"verb":"get"}`)}}},
			{Role: agent.RoleTool, ToolResult: &agent.ToolResult{ToolCallID: "call-1", ToolName: "kubectl", Content: "pods"}},
		},
		Tools: []agent.ToolDefinition{{
			Name:        "kubectl",
			Description: "Run kubectl",
			InputSchema: []byte(`{"type":"object"}`),
		}},
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	assertProtocolResponse(t, response)
}

func assertProtocolRequest(t *testing.T, request chatRequest) {
	t.Helper()
	if len(request.Messages) != 4 {
		t.Fatalf("messages = %#v, want four messages", request.Messages)
	}
	assertProtocolMessages(t, request.Messages)
	if len(request.Tools) != 1 || request.Tools[0].Type != "function" || request.Tools[0].Function.Name != "kubectl" {
		t.Errorf("tools = %#v, want kubectl function", request.Tools)
	}
}

func assertProtocolMessages(t *testing.T, messages []chatMessage) {
	t.Helper()
	if messages[0].Role != "system" || messages[0].Content != "use tools" {
		t.Errorf("system message = %#v, want system instruction", messages[0])
	}
	if messages[2].Role != "assistant" || len(messages[2].ToolCalls) != 1 {
		t.Errorf("assistant message = %#v, want tool call", messages[2])
	}
	toolCall := messages[2].ToolCalls[0].Function
	if toolCall.Index == nil || *toolCall.Index != 0 || string(toolCall.Arguments) != `{"verb":"get"}` {
		t.Errorf("tool call = %#v, want indexed arguments", messages[2].ToolCalls[0])
	}
	if messages[3].Role != "tool" || messages[3].ToolName != "kubectl" || messages[3].Content != "pods" {
		t.Errorf("tool result = %#v, want Ollama tool message", messages[3])
	}
}

func assertProtocolResponse(t *testing.T, response models.Response) {
	t.Helper()
	if response.Text != "I will inspect the pods." || len(response.ToolCalls) != 2 {
		t.Fatalf("response = %#v, want text and two tool calls", response)
	}
	if response.ToolCalls[0].ID == response.ToolCalls[1].ID || response.ToolCalls[0].ID == "" {
		t.Errorf("tool call IDs = %#v, want unique non-empty IDs", response.ToolCalls)
	}
	if response.ToolCalls[0].Name != "kubectl" || string(response.ToolCalls[1].Arguments) != `{"verb":"describe","args":["pod","api"]}` {
		t.Errorf("tool calls = %#v, want decoded calls", response.ToolCalls)
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

	got, err := client.Chat(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if got.Text != "recovered" {
		t.Errorf("Chat() text = %q, want recovered", got.Text)
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
	_, err := client.Chat(context.Background(), testRequest())
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
	go func() { _, err := client.Chat(ctx, testRequest()); errs <- err }()
	<-retrying
	cancel()

	assertErrorKind(t, <-errs, ErrorCanceled)
	if got := calls.Load(); got != 1 {
		t.Errorf("requests = %d, want 1", got)
	}
}

func TestClientChatCancellationInterruptsRequest(t *testing.T) {
	requestStarted := make(chan struct{})
	releaseRequest := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		close(requestStarted)
		<-releaseRequest
	}))
	defer func() {
		close(releaseRequest)
		server.Close()
	}()

	client := newTestClient(t, server.URL, time.Second)
	client.sleep = func(context.Context, time.Duration) error {
		t.Fatal("retry delay called after cancellation")
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	errs := make(chan error, 1)
	go func() {
		_, err := client.Chat(ctx, testRequest())
		errs <- err
	}()
	<-requestStarted
	cancel()

	select {
	case err := <-errs:
		assertErrorKind(t, err, ErrorCanceled)
	case <-time.After(time.Second):
		t.Fatal("canceled request did not stop")
	}
}

func TestClientChatRejectsInvalidResponses(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "empty content", body: `{"message":{"content":"  "},"done":true}`},
		{name: "tool call without name", body: `{"message":{"tool_calls":[{"function":{"arguments":{}}}]},"done":true}`},
		{name: "tool call with invalid arguments", body: `{"message":{"tool_calls":[{"function":{"name":"kubectl","arguments":[]}}]},"done":true}`},
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
			_, err := client.Chat(context.Background(), testRequest())
			assertErrorKind(t, err, ErrorInvalidResponse)
		})
	}
}

func TestClientChatRejectsOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", maximumResponseBodySize+1)))
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, time.Second)
	_, err := client.Chat(context.Background(), testRequest())
	assertErrorKind(t, err, ErrorInvalidResponse)
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
	_, err := client.Chat(context.Background(), testRequest())
	assertErrorKind(t, err, ErrorRequest)
	if got := calls.Load(); got != 1 {
		t.Errorf("requests = %d, want 1", got)
	}
}

func TestClientChatDoesNotRetryPermanentTransportErrors(t *testing.T) {
	client := newTestClient(t, "http://ollama.example", time.Second)
	var calls atomic.Int32
	client.http.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("invalid server certificate")
	})
	client.sleep = func(context.Context, time.Duration) error {
		t.Fatal("retry delay called for permanent transport error")
		return nil
	}

	_, err := client.Chat(context.Background(), testRequest())
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

func testRequest() models.Request {
	return models.Request{
		Messages: []agent.Message{{
			Role:    agent.RoleUser,
			Content: "show pods",
		}},
		Tools: []agent.ToolDefinition{},
	}
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
	if request.Model != "llama3" || len(request.Messages) != 1 || request.Messages[0].Role != "user" || request.Messages[0].Content != "show pods" {
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

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
