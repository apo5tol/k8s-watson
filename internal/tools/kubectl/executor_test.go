package kubectl

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestNewExecutorRejectsInvalidConfig(t *testing.T) {
	valid := ExecutorConfig{
		Path:           os.Args[0],
		Timeout:        time.Second,
		MaxOutputBytes: 1,
	}
	tests := []struct {
		name   string
		config ExecutorConfig
	}{
		{name: "missing path", config: ExecutorConfig{Timeout: valid.Timeout, MaxOutputBytes: valid.MaxOutputBytes}},
		{name: "zero timeout", config: ExecutorConfig{Path: valid.Path, MaxOutputBytes: valid.MaxOutputBytes}},
		{name: "zero output limit", config: ExecutorConfig{Path: valid.Path, Timeout: valid.Timeout}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewExecutor(test.config)
			if !errors.Is(err, ErrInvalidExecutor) {
				t.Errorf("NewExecutor() error = %v, want ErrInvalidExecutor", err)
			}
		})
	}
}

func TestExecutorCapturesOutput(t *testing.T) {
	executor := testExecutor(t, time.Second, 1024)

	result, err := executor.Execute(context.Background(), helperArguments("output"))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got, expected := result.Content, "stdout:\nstdout\nstderr:\nstderr"; got != expected {
		t.Errorf("Execute() content = %q, want %q", got, expected)
	}
}

func TestExecutorRejectsEmptyArguments(t *testing.T) {
	executor := testExecutor(t, time.Second, 1024)

	_, err := executor.Execute(context.Background(), nil)
	if !errors.Is(err, ErrInvalidExecutor) {
		t.Errorf("Execute() error = %v, want ErrInvalidExecutor", err)
	}
}

func TestExecutorTruncatesCombinedOutput(t *testing.T) {
	executor := testExecutor(t, time.Second, 5)

	result, err := executor.Execute(context.Background(), helperArguments("large-output"))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.HasSuffix(result.Content, "[output truncated]") {
		t.Errorf("Execute() content = %q, want truncation marker", result.Content)
	}
}

func TestCombinedOutputRespectsLimitBoundary(t *testing.T) {
	output := newCombinedOutput(5)
	if _, err := output.stdoutWriter().Write([]byte("abc")); err != nil {
		t.Fatalf("write stdout: %v", err)
	}
	if _, err := output.stderrWriter().Write([]byte("de")); err != nil {
		t.Fatalf("write stderr: %v", err)
	}
	result := output.result()
	if result.Truncated {
		t.Error("result marked truncated at exact output limit")
	}
	if result.Stdout != "abc" || result.Stderr != "de" {
		t.Errorf("result = %#v, want stdout abc and stderr de", result)
	}

	if _, err := output.stdoutWriter().Write([]byte("f")); err != nil {
		t.Fatalf("write excess stdout: %v", err)
	}
	if result := output.result(); !result.Truncated || result.Stdout != "abc" || result.Stderr != "de" {
		t.Errorf("result after excess = %#v, want preserved output with truncation", result)
	}
}

func TestExecutorReturnsExitError(t *testing.T) {
	executor := testExecutor(t, time.Second, 1024)

	_, err := executor.Execute(context.Background(), helperArguments("failure"))
	var executionError *ExecutionError
	if !errors.As(err, &executionError) {
		t.Fatalf("Execute() error = %v, want ExecutionError", err)
	}
	if executionError.ExitCode != 3 {
		t.Errorf("ExecutionError.ExitCode = %d, want 3", executionError.ExitCode)
	}
	if executionError.Stderr != "failed" {
		t.Errorf("ExecutionError.Stderr = %q, want %q", executionError.Stderr, "failed")
	}
}

func TestExecutorReturnsTruncatedExitError(t *testing.T) {
	executor := testExecutor(t, time.Second, 3)

	_, err := executor.Execute(context.Background(), helperArguments("large-failure"))
	var executionError *ExecutionError
	if !errors.As(err, &executionError) {
		t.Fatalf("Execute() error = %v, want ExecutionError", err)
	}
	if !executionError.Truncated {
		t.Error("ExecutionError.Truncated = false, want true")
	}
	if len(executionError.Stderr) > 3 {
		t.Errorf("ExecutionError.Stderr = %q, exceeds output limit", executionError.Stderr)
	}
}

func TestExecutorHonorsTimeout(t *testing.T) {
	executor := testExecutor(t, 10*time.Millisecond, 1024)

	startedAt := time.Now()
	_, err := executor.Execute(context.Background(), helperArguments("sleep"))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Execute() error = %v, want context deadline exceeded", err)
	}
	if elapsed := time.Since(startedAt); elapsed >= 500*time.Millisecond {
		t.Errorf("Execute() returned after %s, want before helper sleep completes", elapsed)
	}
}

func TestExecutorHonorsCancelledContext(t *testing.T) {
	executor := testExecutor(t, time.Second, 1024)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)
	startedAt := time.Now()

	_, err := executor.Execute(ctx, helperArguments("sleep"))
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Execute() error = %v, want context canceled", err)
	}
	if elapsed := time.Since(startedAt); elapsed >= 500*time.Millisecond {
		t.Errorf("Execute() returned after %s, want before helper sleep completes", elapsed)
	}
}

func testExecutor(t *testing.T, timeout time.Duration, maxOutputBytes int) Executor {
	t.Helper()

	executor, err := NewExecutor(ExecutorConfig{
		Path:           os.Args[0],
		Timeout:        timeout,
		MaxOutputBytes: maxOutputBytes,
	})
	if err != nil {
		t.Fatalf("NewExecutor() error = %v", err)
	}
	return executor
}

func helperArguments(scenario string) []string {
	return []string{"kubectl", "-test.run=^TestExecutorHelperProcess$", "--", scenario}
}

func TestExecutorHelperProcess(t *testing.T) {
	t.Helper()
	for index, argument := range os.Args {
		if argument != "--" || index+1 >= len(os.Args) {
			continue
		}

		switch os.Args[index+1] {
		case "output":
			_, _ = fmt.Fprint(os.Stdout, "stdout")
			_, _ = fmt.Fprint(os.Stderr, "stderr")
		case "large-output":
			_, _ = fmt.Fprint(os.Stdout, "abcdef")
			_, _ = fmt.Fprint(os.Stderr, "ghijkl")
		case "failure":
			_, _ = fmt.Fprint(os.Stderr, "failed")
			os.Exit(3)
		case "large-failure":
			_, _ = fmt.Fprint(os.Stderr, "abcdef")
			os.Exit(3)
		case "sleep":
			time.Sleep(time.Second)
		default:
			t.Fatalf("unknown helper scenario %q", os.Args[index+1])
		}
		os.Exit(0)
	}
}
