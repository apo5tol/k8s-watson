package kubectl

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"k8s-watson/internal/agent"
)

type fakeExecutor struct {
	calls  [][]string
	result agent.ToolResult
	err    error
}

func (e *fakeExecutor) Execute(_ context.Context, argv []string) (agent.ToolResult, error) {
	e.calls = append(e.calls, argv)
	return e.result, e.err
}

func TestDefinition(t *testing.T) {
	definition := New(nil).Definition()
	if definition.Name != toolName {
		t.Errorf("Name = %q, want %q", definition.Name, toolName)
	}

	var schema struct {
		Type                 string                     `json:"type"`
		Required             []string                   `json:"required"`
		AdditionalProperties bool                       `json:"additionalProperties"`
		Properties           map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(definition.InputSchema, &schema); err != nil {
		t.Fatalf("InputSchema is not valid JSON: %v", err)
	}
	if schema.Type != "object" || !reflect.DeepEqual(schema.Required, []string{"verb"}) || schema.AdditionalProperties || len(schema.Properties) != 2 {
		t.Errorf("schema = %#v, want object with required verb and no additional properties", schema)
	}
}

func TestPrepareRejectsMalformedCalls(t *testing.T) {
	tool := New(nil)
	tests := []struct {
		name      string
		arguments string
	}{
		{name: "empty arguments", arguments: ""},
		{name: "unknown field", arguments: `{"verb":"get","unknown":true}`},
		{name: "trailing data", arguments: `{"verb":"get"} {}`},
		{name: "missing verb", arguments: `{"args":[]}`},
		{name: "whitespace verb", arguments: `{"verb":"get pods"}`},
		{name: "null args", arguments: `{"verb":"get","args":null}`},
		{name: "non-array args", arguments: `{"verb":"get","args":"pods"}`},
		{name: "non-string arg", arguments: `{"verb":"get","args":[1]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := tool.Prepare(context.Background(), agent.ToolCall{Arguments: json.RawMessage(test.arguments)})
			if !errors.Is(err, ErrInvalidCall) {
				t.Errorf("Prepare() error = %v, want ErrInvalidCall", err)
			}
		})
	}
}

func TestPrepareRejectsForbiddenCalls(t *testing.T) {
	tool := New(nil)
	tests := []struct {
		name string
		verb string
		args []string
	}{
		{name: "forbidden verb", verb: "exec"},
		{name: "forbidden verb case insensitive", verb: "Edit"},
		{name: "shell pipe", verb: "get", args: []string{"pods|cat"}},
		{name: "shell redirect", verb: "get", args: []string{"pods>output"}},
		{name: "shell substitution", verb: "get", args: []string{"$(whoami)"}},
		{name: "backtick substitution", verb: "get", args: []string{"`whoami`"}},
		{name: "context separate", verb: "get", args: []string{"--context", "other"}},
		{name: "context equals", verb: "get", args: []string{"--context=other"}},
		{name: "kubeconfig separate", verb: "get", args: []string{"--kubeconfig", "config"}},
		{name: "kubeconfig equals", verb: "get", args: []string{"--kubeconfig=config"}},
		{name: "config mutation", verb: "config", args: []string{"set-context", "other"}},
		{name: "interactive shorthand", verb: "run", args: []string{"-it"}},
		{name: "interactive attach", verb: "run", args: []string{"--attach=true"}},
		{name: "interactive tty", verb: "run", args: []string{"--tty=true"}},
		{name: "interactive stdin", verb: "run", args: []string{"--stdin=true"}},
		{name: "interactive flag", verb: "run", args: []string{"--interactive=true"}},
		{name: "follow shorthand", verb: "logs", args: []string{"-f"}},
		{name: "follow", verb: "logs", args: []string{"--follow=true"}},
		{name: "watch shorthand", verb: "get", args: []string{"-w"}},
		{name: "watch", verb: "get", args: []string{"--watch=true"}},
		{name: "watch only", verb: "get", args: []string{"--watch-only=true"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := tool.Prepare(context.Background(), toolCall(test.verb, test.args))
			if !errors.Is(err, ErrForbiddenCall) {
				t.Errorf("Prepare() error = %v, want ErrForbiddenCall", err)
			}
		})
	}
}

func TestPrepareAllowsDisabledStreamingFlags(t *testing.T) {
	tests := []struct {
		name string
		verb string
		args []string
	}{
		{name: "follow false", verb: "logs", args: []string{"--follow=false"}},
		{name: "watch false", verb: "get", args: []string{"--watch=false"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := New(nil).Prepare(context.Background(), toolCall(test.verb, test.args)); err != nil {
				t.Errorf("Prepare() error = %v, want nil", err)
			}
		})
	}
}

func TestPrepareParsesNamespace(t *testing.T) {
	tests := []struct {
		name              string
		args              []string
		expectedNamespace string
		err               error
	}{
		{name: "short", args: []string{"pods", "-n", "monitoring"}, expectedNamespace: "monitoring"},
		{name: "long", args: []string{"pods", "--namespace", "monitoring"}, expectedNamespace: "monitoring"},
		{name: "equals", args: []string{"pods", "--namespace=monitoring"}, expectedNamespace: "monitoring"},
		{name: "missing", args: []string{"pods", "-n"}, err: ErrInvalidCall},
		{name: "empty", args: []string{"pods", "--namespace="}, err: ErrInvalidCall},
		{name: "flag value", args: []string{"pods", "--namespace", "--all-namespaces"}, err: ErrInvalidCall},
		{name: "duplicate", args: []string{"pods", "-n", "one", "--namespace=two"}, err: ErrInvalidCall},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			prepared, err := New(nil).Prepare(context.Background(), toolCall("get", test.args))
			if !errors.Is(err, test.err) {
				t.Fatalf("Prepare() error = %v, want %v", err, test.err)
			}
			if test.err != nil {
				return
			}
			if got := prepared.Metadata()[MetadataNamespace]; got != test.expectedNamespace {
				t.Errorf("Metadata()[%q] = %q, want %q", MetadataNamespace, got, test.expectedNamespace)
			}
		})
	}
}

func TestPrepareSetsApprovalAndDisplay(t *testing.T) {
	tests := []struct {
		verb             string
		requiresApproval bool
	}{
		{verb: "get"},
		{verb: "describe"},
		{verb: "list", requiresApproval: true},
		{verb: "logs", requiresApproval: true},
		{verb: "GET", requiresApproval: true},
	}
	for _, test := range tests {
		t.Run(test.verb, func(t *testing.T) {
			prepared, err := New(nil).Prepare(context.Background(), toolCall(test.verb, []string{}))
			if err != nil {
				t.Fatalf("Prepare() error = %v", err)
			}
			if got := prepared.RequiresApproval(); got != test.requiresApproval {
				t.Errorf("RequiresApproval() = %t, want %t", got, test.requiresApproval)
			}
		})
	}

	prepared, err := New(nil).Prepare(context.Background(), toolCall("get", []string{"pod name", "it's"}))
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if got, expected := prepared.Display(), `kubectl get 'pod name' 'it'"'"'s'`; got != expected {
		t.Errorf("Display() = %q, want %q", got, expected)
	}
}

func TestPreparedCallDefensivelyCopiesData(t *testing.T) {
	executor := &fakeExecutor{result: agent.ToolResult{Content: "ok"}}
	args := []string{"pods", "--namespace=default"}
	prepared, err := New(executor).Prepare(context.Background(), toolCall("get", args))
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	args[0] = "services"

	metadata := prepared.Metadata()
	metadata[MetadataNamespace] = "changed"
	if got := prepared.Metadata()[MetadataNamespace]; got != "default" {
		t.Errorf("Metadata()[%q] = %q, want default", MetadataNamespace, got)
	}
	if len(executor.calls) != 0 {
		t.Errorf("Prepare() executed %d calls, want none", len(executor.calls))
	}

	if _, err := prepared.Execute(context.Background()); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	executor.calls[0][1] = "changed"
	if _, err := prepared.Execute(context.Background()); err != nil {
		t.Fatalf("second Execute() error = %v", err)
	}
	if got, expected := executor.calls[1], []string{"kubectl", "get", "pods", "--namespace=default"}; !reflect.DeepEqual(got, expected) {
		t.Errorf("second Execute() argv = %#v, want %#v", got, expected)
	}
}

func TestPreparedCallRequiresExecutor(t *testing.T) {
	prepared, err := New(nil).Prepare(context.Background(), toolCall("get", []string{}))
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if _, err := prepared.Execute(context.Background()); !errors.Is(err, ErrExecutorRequired) {
		t.Errorf("Execute() error = %v, want ErrExecutorRequired", err)
	}
}

func toolCall(verb string, args []string) agent.ToolCall {
	if args == nil {
		args = []string{}
	}
	arguments, err := json.Marshal(struct {
		Verb string   `json:"verb"`
		Args []string `json:"args"`
	}{
		Verb: verb,
		Args: args,
	})
	if err != nil {
		panic(err)
	}
	return agent.ToolCall{Arguments: arguments}
}
