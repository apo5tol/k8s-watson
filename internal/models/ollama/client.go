package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"k8s-watson/internal/agent"
	"k8s-watson/internal/models"
)

const (
	maxAttempts             = 3
	retryDelay              = time.Second
	maximumResponseBodySize = 1 << 20
)

type ErrorKind string

const (
	ErrorCanceled        ErrorKind = "canceled"
	ErrorTimeout         ErrorKind = "timeout"
	ErrorTemporary       ErrorKind = "temporary"
	ErrorRequest         ErrorKind = "request"
	ErrorInvalidResponse ErrorKind = "invalid_response"
)

type Error struct {
	Kind ErrorKind
	Err  error
}

func (e *Error) Error() string {
	return fmt.Sprintf("ollama %s error: %v", e.Kind, e.Err)
}

func (e *Error) Unwrap() error {
	return e.Err
}

type Client struct {
	endpoint *url.URL
	model    string
	timeout  time.Duration
	http     *http.Client
	logger   *slog.Logger
	sleep    func(context.Context, time.Duration) error
	callID   atomic.Uint64
}

var _ models.Model = (*Client)(nil)

func New(endpoint *url.URL, model string, timeout time.Duration, logger *slog.Logger) (*Client, error) {
	if endpoint == nil {
		return nil, errors.New("ollama endpoint is required")
	}
	if strings.TrimSpace(model) == "" {
		return nil, errors.New("ollama model is required")
	}
	if timeout <= 0 {
		return nil, errors.New("ollama timeout must be positive")
	}
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	endpointCopy := *endpoint
	return &Client{
		endpoint: &endpointCopy,
		model:    model,
		timeout:  timeout,
		http:     &http.Client{},
		logger:   logger,
		sleep:    sleep,
	}, nil
}

func (c *Client) Chat(ctx context.Context, request models.Request) (models.Response, error) {
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		startedAt := time.Now()
		response, err := c.chatOnce(ctx, request)
		if err == nil {
			c.logger.Info("ollama request completed", "event", "ollama_request_completed", "attempt", attempt, "duration", time.Since(startedAt))
			return response, nil
		}

		kind := errorKind(err)
		c.logger.Error("ollama request failed", "event", "ollama_request_failed", "attempt", attempt, "duration", time.Since(startedAt), "error_kind", kind, "error", err)
		if attempt == maxAttempts || !isRetryable(err) {
			return models.Response{}, err
		}

		c.logger.Info("ollama request retrying", "event", "ollama_request_retrying", "attempt", attempt+1, "delay", retryDelay, "error_kind", kind)
		if err := c.sleep(ctx, retryDelay); err != nil {
			return models.Response{}, canceledError(err)
		}
	}

	return models.Response{}, &Error{Kind: ErrorRequest, Err: errors.New("ollama request attempts exhausted")}
}

func (c *Client) chatOnce(ctx context.Context, request models.Request) (models.Response, error) {
	if err := ctx.Err(); err != nil {
		return models.Response{}, canceledError(err)
	}

	attemptCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	payload, err := newChatRequest(c.model, request)
	if err != nil {
		return models.Response{}, &Error{Kind: ErrorRequest, Err: fmt.Errorf("prepare chat request: %w", err)}
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return models.Response{}, &Error{Kind: ErrorRequest, Err: fmt.Errorf("encode chat request: %w", err)}
	}

	requestURL := c.endpoint.JoinPath("api", "chat")
	req, err := http.NewRequestWithContext(attemptCtx, http.MethodPost, requestURL.String(), bytes.NewReader(body))
	if err != nil {
		return models.Response{}, &Error{Kind: ErrorRequest, Err: fmt.Errorf("create chat request: %w", err)}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return models.Response{}, classifyTransportError(ctx, attemptCtx, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return models.Response{}, classifyHTTPError(resp)
	}

	parsedResponse, err := c.decodeResponse(resp.Body)
	if err != nil {
		return models.Response{}, &Error{Kind: ErrorInvalidResponse, Err: err}
	}

	return parsedResponse, nil
}

func (c *Client) decodeResponse(body io.Reader) (models.Response, error) {
	contents, isTruncated, err := readResponseBody(body)
	if err != nil {
		return models.Response{}, fmt.Errorf("read chat response: %w", err)
	}
	if isTruncated {
		return models.Response{}, errors.New("chat response exceeds size limit")
	}

	var response chatResponse
	if err := json.Unmarshal(contents, &response); err != nil {
		return models.Response{}, fmt.Errorf("decode chat response: %w", err)
	}
	if !response.Done {
		return models.Response{}, errors.New("chat response is not complete")
	}

	return c.parseResponse(response.Message)
}

func newChatRequest(model string, request models.Request) (chatRequest, error) {
	stream := false
	think := false
	messages, err := toChatMessages(request.Messages)
	if err != nil {
		return chatRequest{}, err
	}
	tools, err := toChatTools(request.Tools)
	if err != nil {
		return chatRequest{}, err
	}

	return chatRequest{
		Model:    model,
		Messages: messages,
		Tools:    tools,
		Stream:   &stream,
		Think:    &think,
	}, nil
}

func toChatMessages(messages []agent.Message) ([]chatMessage, error) {
	converted := make([]chatMessage, 0, len(messages))
	for _, message := range messages {
		convertedMessage, err := toChatMessage(message)
		if err != nil {
			return nil, err
		}
		converted = append(converted, convertedMessage)
	}

	return converted, nil
}

func toChatMessage(message agent.Message) (chatMessage, error) {
	switch message.Role {
	case agent.RoleSystem, agent.RoleUser:
		return toTextMessage(message)
	case agent.RoleAssistant:
		return toAssistantMessage(message)
	case agent.RoleTool:
		return toToolMessage(message)
	default:
		return chatMessage{}, fmt.Errorf("unknown message role %q", message.Role)
	}
}

func toTextMessage(message agent.Message) (chatMessage, error) {
	if len(message.ToolCalls) != 0 || message.ToolResult != nil {
		return chatMessage{}, fmt.Errorf("%s message cannot contain tool data", message.Role)
	}
	return chatMessage{
		Role:    string(message.Role),
		Content: message.Content,
	}, nil
}

func toAssistantMessage(message agent.Message) (chatMessage, error) {
	if message.ToolResult != nil {
		return chatMessage{}, errors.New("assistant message cannot contain a tool result")
	}

	toolCalls, err := toChatToolCalls(message.ToolCalls)
	if err != nil {
		return chatMessage{}, err
	}
	return chatMessage{
		Role:      string(agent.RoleAssistant),
		Content:   message.Content,
		ToolCalls: toolCalls,
	}, nil
}

func toToolMessage(message agent.Message) (chatMessage, error) {
	if len(message.ToolCalls) != 0 {
		return chatMessage{}, errors.New("tool message cannot contain tool calls")
	}
	if message.ToolResult == nil {
		return chatMessage{}, errors.New("tool message requires a tool result")
	}
	if strings.TrimSpace(message.ToolResult.ToolCallID) == "" {
		return chatMessage{}, errors.New("tool result requires a tool call id")
	}
	if strings.TrimSpace(message.ToolResult.ToolName) == "" {
		return chatMessage{}, errors.New("tool result requires a tool name")
	}

	return chatMessage{
		Role:     string(agent.RoleTool),
		Content:  message.ToolResult.Content,
		ToolName: message.ToolResult.ToolName,
	}, nil
}

func toChatToolCalls(calls []agent.ToolCall) ([]chatToolCall, error) {
	converted := make([]chatToolCall, 0, len(calls))
	for index, call := range calls {
		if strings.TrimSpace(call.ID) == "" {
			return nil, errors.New("tool call requires an id")
		}
		if strings.TrimSpace(call.Name) == "" {
			return nil, errors.New("tool call requires a name")
		}
		if !isJSONObject(call.Arguments) {
			return nil, errors.New("tool call arguments must be a JSON object")
		}
		callIndex := index
		converted = append(converted, chatToolCall{
			Type: "function",
			Function: chatToolFunction{
				Index:     &callIndex,
				Name:      call.Name,
				Arguments: call.Arguments,
			},
		})
	}

	return converted, nil
}

func toChatTools(tools []agent.ToolDefinition) ([]chatTool, error) {
	converted := make([]chatTool, 0, len(tools))
	for _, tool := range tools {
		if strings.TrimSpace(tool.Name) == "" {
			return nil, errors.New("tool definition requires a name")
		}
		if !isJSONObject(tool.InputSchema) {
			return nil, errors.New("tool input schema must be a JSON object")
		}
		converted = append(converted, chatTool{
			Type: "function",
			Function: chatToolFunction{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.InputSchema,
			},
		})
	}

	return converted, nil
}

func (c *Client) parseResponse(message chatMessage) (models.Response, error) {
	toolCalls, err := c.parseToolCalls(message.ToolCalls)
	if err != nil {
		return models.Response{}, err
	}

	text := strings.TrimSpace(message.Content)
	if text == "" && len(toolCalls) == 0 {
		return models.Response{}, errors.New("chat response is empty")
	}

	return models.Response{
		Text:      text,
		ToolCalls: toolCalls,
	}, nil
}

func (c *Client) parseToolCalls(calls []chatToolCall) ([]agent.ToolCall, error) {
	parsed := make([]agent.ToolCall, 0, len(calls))
	for _, call := range calls {
		if call.Type != "" && call.Type != "function" {
			return nil, fmt.Errorf("unsupported tool call type %q", call.Type)
		}
		if strings.TrimSpace(call.Function.Name) == "" {
			return nil, errors.New("tool call requires a name")
		}
		if !isJSONObject(call.Function.Arguments) {
			return nil, errors.New("tool call arguments must be a JSON object")
		}
		parsed = append(parsed, agent.ToolCall{
			ID:        fmt.Sprintf("ollama-call-%d", c.callID.Add(1)),
			Name:      call.Function.Name,
			Arguments: call.Function.Arguments,
		})
	}

	return parsed, nil
}

func isJSONObject(value json.RawMessage) bool {
	if !json.Valid(value) {
		return false
	}

	var object map[string]json.RawMessage
	return json.Unmarshal(value, &object) == nil
}

func classifyTransportError(parentCtx, attemptCtx context.Context, err error) error {
	if parentCtx.Err() != nil {
		return canceledError(parentCtx.Err())
	}
	if attemptCtx.Err() != nil {
		return &Error{Kind: ErrorTimeout, Err: attemptCtx.Err()}
	}

	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return &Error{Kind: ErrorTimeout, Err: err}
	}
	return &Error{Kind: ErrorRequest, Err: err}
}

func classifyHTTPError(resp *http.Response) error {
	body, isTruncated, err := readResponseBody(resp.Body)
	if err != nil {
		return &Error{Kind: ErrorRequest, Err: fmt.Errorf("read error response: %w", err)}
	}

	message := strings.TrimSpace(string(body))
	var errorResponse struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &errorResponse) == nil && strings.TrimSpace(errorResponse.Error) != "" {
		message = errorResponse.Error
	}
	if message == "" {
		message = resp.Status
	}
	if isTruncated {
		message += "\n[response truncated]"
	}

	kind := ErrorRequest
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= http.StatusInternalServerError {
		kind = ErrorTemporary
	}
	return &Error{Kind: kind, Err: fmt.Errorf("HTTP %d: %s", resp.StatusCode, message)}
}

func readResponseBody(body io.Reader) ([]byte, bool, error) {
	limitedBody := io.LimitReader(body, maximumResponseBodySize+1)
	contents, err := io.ReadAll(limitedBody)
	if err != nil {
		return nil, false, err
	}
	if len(contents) > maximumResponseBodySize {
		return contents[:maximumResponseBodySize], true, nil
	}

	return contents, false, nil
}

func canceledError(err error) error {
	return &Error{Kind: ErrorCanceled, Err: err}
}

func errorKind(err error) ErrorKind {
	var ollamaErr *Error
	if errors.As(err, &ollamaErr) {
		return ollamaErr.Kind
	}
	return ErrorRequest
}

func isRetryable(err error) bool {
	kind := errorKind(err)
	return kind == ErrorTimeout || kind == ErrorTemporary
}

func sleep(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Tools    []chatTool    `json:"tools,omitempty"`
	Stream   *bool         `json:"stream"`
	Think    *bool         `json:"think"`
}

type chatMessage struct {
	Role      string         `json:"role"`
	Content   string         `json:"content,omitempty"`
	ToolCalls []chatToolCall `json:"tool_calls,omitempty"`
	ToolName  string         `json:"tool_name,omitempty"`
}

type chatResponse struct {
	Message chatMessage `json:"message"`
	Done    bool        `json:"done"`
}

type chatTool struct {
	Type     string           `json:"type"`
	Function chatToolFunction `json:"function"`
}

type chatToolCall struct {
	Type     string           `json:"type,omitempty"`
	Function chatToolFunction `json:"function"`
}

type chatToolFunction struct {
	Index       *int            `json:"index,omitempty"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
	Arguments   json.RawMessage `json:"arguments,omitempty"`
}
