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
	"time"
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
}

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

func (c *Client) Chat(ctx context.Context, prompt string) (string, error) {
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		startedAt := time.Now()
		response, err := c.chatOnce(ctx, prompt)
		if err == nil {
			c.logger.Info("ollama request completed", "event", "ollama_request_completed", "attempt", attempt, "duration", time.Since(startedAt))
			return response, nil
		}

		kind := errorKind(err)
		c.logger.Error("ollama request failed", "event", "ollama_request_failed", "attempt", attempt, "duration", time.Since(startedAt), "error_kind", kind, "error", err)
		if attempt == maxAttempts || !isRetryable(err) {
			return "", err
		}

		c.logger.Info("ollama request retrying", "event", "ollama_request_retrying", "attempt", attempt+1, "delay", retryDelay, "error_kind", kind)
		if err := c.sleep(ctx, retryDelay); err != nil {
			return "", canceledError(err)
		}
	}

	return "", &Error{Kind: ErrorRequest, Err: errors.New("ollama request attempts exhausted")}
}

func (c *Client) chatOnce(ctx context.Context, prompt string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", canceledError(err)
	}

	attemptCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	body, err := json.Marshal(newChatRequest(c.model, prompt))
	if err != nil {
		return "", &Error{Kind: ErrorRequest, Err: fmt.Errorf("encode chat request: %w", err)}
	}

	requestURL := c.endpoint.JoinPath("api", "chat")
	req, err := http.NewRequestWithContext(attemptCtx, http.MethodPost, requestURL.String(), bytes.NewReader(body))
	if err != nil {
		return "", &Error{Kind: ErrorRequest, Err: fmt.Errorf("create chat request: %w", err)}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", classifyTransportError(ctx, attemptCtx, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", classifyHTTPError(resp)
	}

	body, isTruncated, err := readResponseBody(resp.Body)
	if err != nil {
		return "", &Error{Kind: ErrorInvalidResponse, Err: fmt.Errorf("read chat response: %w", err)}
	}
	if isTruncated {
		return "", &Error{Kind: ErrorInvalidResponse, Err: errors.New("chat response exceeds size limit")}
	}

	var response chatResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return "", &Error{Kind: ErrorInvalidResponse, Err: fmt.Errorf("decode chat response: %w", err)}
	}
	if !response.Done {
		return "", &Error{Kind: ErrorInvalidResponse, Err: errors.New("chat response is not complete")}
	}
	text := strings.TrimSpace(response.Message.Content)
	if text == "" {
		return "", &Error{Kind: ErrorInvalidResponse, Err: errors.New("chat response is empty")}
	}

	return text, nil
}

func newChatRequest(model, prompt string) chatRequest {
	stream := false
	think := false
	return chatRequest{
		Model:    model,
		Messages: []chatMessage{{Role: "user", Content: prompt}},
		Stream:   &stream,
		Think:    &think,
	}
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
	Stream   *bool         `json:"stream"`
	Think    *bool         `json:"think"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Message chatMessage `json:"message"`
	Done    bool        `json:"done"`
}
