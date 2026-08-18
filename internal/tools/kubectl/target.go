package kubectl

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const defaultNamespace = "default"

type Target struct {
	Context   string
	Namespace string
}

type TargetResolver interface {
	Resolve(context.Context) (Target, error)
}

type targetResolver struct {
	path    string
	timeout time.Duration
}

func NewTargetResolver(path string, timeout time.Duration) (TargetResolver, error) {
	if path == "" || timeout <= 0 {
		return nil, ErrInvalidExecutor
	}

	return &targetResolver{path: path, timeout: timeout}, nil
}

func (r *targetResolver) Resolve(ctx context.Context) (Target, error) {
	contextName, err := r.run(ctx, "config", "current-context")
	if err != nil {
		return Target{}, fmt.Errorf("get current context: %w", err)
	}
	if contextName == "" {
		return Target{}, errors.New("get current context: empty result")
	}

	namespace, err := r.run(ctx, "config", "view", "--minify", "--output=jsonpath={..namespace}")
	if err != nil {
		return Target{}, fmt.Errorf("get current namespace: %w", err)
	}
	if namespace == "" {
		namespace = defaultNamespace
	}

	return Target{Context: contextName, Namespace: namespace}, nil
}

func (r *targetResolver) run(ctx context.Context, args ...string) (string, error) {
	commandContext, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	//nolint:gosec // The binary path is found at startup and arguments are fixed kubectl config queries.
	output, err := exec.CommandContext(commandContext, r.path, args...).Output()
	if err != nil {
		if commandContext.Err() != nil {
			return "", commandContext.Err()
		}
		return "", err
	}

	return strings.TrimSpace(string(output)), nil
}
