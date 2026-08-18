package kubectl

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"time"

	"k8s-watson/internal/agent"
)

type ExecutorConfig struct {
	Path           string
	Timeout        time.Duration
	MaxOutputBytes int
	Logger         *slog.Logger
}

type ExecutionError struct {
	ExitCode  int
	Stderr    string
	Truncated bool
}

func (e *ExecutionError) Error() string {
	return fmt.Sprintf("kubectl exited with status %d", e.ExitCode)
}

type executor struct {
	path           string
	timeout        time.Duration
	maxOutputBytes int
	logger         *slog.Logger
}

func NewExecutor(config ExecutorConfig) (Executor, error) {
	if config.Path == "" || config.Timeout <= 0 || config.MaxOutputBytes <= 0 {
		return nil, ErrInvalidExecutor
	}
	if config.Logger == nil {
		config.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	return &executor{
		path:           config.Path,
		timeout:        config.Timeout,
		maxOutputBytes: config.MaxOutputBytes,
		logger:         config.Logger,
	}, nil
}

func (e *executor) Execute(ctx context.Context, argv []string) (agent.ToolResult, error) {
	if len(argv) == 0 {
		return agent.ToolResult{}, fmt.Errorf("%w: command arguments are required", ErrInvalidExecutor)
	}

	startedAt := time.Now()
	commandContext, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()

	//nolint:gosec // Arguments originate from a prepared call validated by Tool.Prepare.
	command := exec.CommandContext(commandContext, e.path, argv[1:]...)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return agent.ToolResult{}, fmt.Errorf("configure kubectl stdout: %w", err)
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		return agent.ToolResult{}, fmt.Errorf("configure kubectl stderr: %w", err)
	}

	if err := command.Start(); err != nil {
		return agent.ToolResult{}, fmt.Errorf("start kubectl: %w", err)
	}

	output := newCombinedOutput(e.maxOutputBytes)
	var readers sync.WaitGroup
	readers.Add(2)
	go copyOutput(&readers, output.stdoutWriter(), stdout)
	go copyOutput(&readers, output.stderrWriter(), stderr)

	err = command.Wait()
	readers.Wait()
	result := output.result()
	e.logExecution(startedAt, err, result.Truncated)

	if commandContext.Err() != nil {
		return agent.ToolResult{}, commandContext.Err()
	}
	if err == nil {
		return agent.ToolResult{Content: formatOutput(result)}, nil
	}

	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		return agent.ToolResult{}, &ExecutionError{
			ExitCode:  exitError.ExitCode(),
			Stderr:    result.Stderr,
			Truncated: result.Truncated,
		}
	}

	return agent.ToolResult{}, fmt.Errorf("wait for kubectl: %w", err)
}

func (e *executor) logExecution(startedAt time.Time, err error, truncated bool) {
	attributes := []any{
		"event", "kubectl_completed",
		"duration", time.Since(startedAt),
		"truncated", truncated,
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		attributes = append(attributes, "exit_code", exitError.ExitCode())
	}
	if errors.Is(err, context.DeadlineExceeded) {
		attributes = append(attributes, "timeout", true)
	}
	if errors.Is(err, context.Canceled) {
		attributes = append(attributes, "cancelled", true)
	}
	e.logger.Info("kubectl command completed", attributes...)
}

func copyOutput(readers *sync.WaitGroup, destination io.Writer, source io.Reader) {
	defer readers.Done()
	_, _ = io.Copy(destination, source)
}

type outputResult struct {
	Stdout    string
	Stderr    string
	Truncated bool
}

type combinedOutput struct {
	mu        sync.Mutex
	remaining int
	stdout    bytes.Buffer
	stderr    bytes.Buffer
	truncated bool
}

func newCombinedOutput(limit int) *combinedOutput {
	return &combinedOutput{remaining: limit}
}

func (o *combinedOutput) stdoutWriter() io.Writer {
	return outputWriter{output: o, buffer: &o.stdout}
}

func (o *combinedOutput) stderrWriter() io.Writer {
	return outputWriter{output: o, buffer: &o.stderr}
}

func (o *combinedOutput) result() outputResult {
	o.mu.Lock()
	defer o.mu.Unlock()

	return outputResult{
		Stdout:    o.stdout.String(),
		Stderr:    o.stderr.String(),
		Truncated: o.truncated,
	}
}

type outputWriter struct {
	output *combinedOutput
	buffer *bytes.Buffer
}

func (w outputWriter) Write(data []byte) (int, error) {
	w.output.mu.Lock()
	defer w.output.mu.Unlock()

	if w.output.remaining == 0 {
		if len(data) > 0 {
			w.output.truncated = true
		}
		return len(data), nil
	}

	stored := min(len(data), w.output.remaining)
	_, _ = w.buffer.Write(data[:stored])
	w.output.remaining -= stored
	if stored < len(data) {
		w.output.truncated = true
	}
	return len(data), nil
}

func formatOutput(result outputResult) string {
	var output strings.Builder
	output.WriteString("stdout:\n")
	output.WriteString(result.Stdout)
	output.WriteString("\nstderr:\n")
	output.WriteString(result.Stderr)
	if result.Truncated {
		output.WriteString("\n[output truncated]")
	}
	return output.String()
}
