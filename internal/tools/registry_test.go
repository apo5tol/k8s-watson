package tools

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"k8s-watson/internal/agent"
)

type testTool struct {
	definition agent.ToolDefinition
}

func (t testTool) Definition() agent.ToolDefinition {
	return t.definition
}

func (testTool) Prepare(context.Context, agent.ToolCall) (PreparedCall, error) {
	return nil, nil
}

func TestRegistryDefinitionsAreStableCopies(t *testing.T) {
	registry, err := NewRegistry(
		testTool{definition: agent.ToolDefinition{Name: "first", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		testTool{definition: agent.ToolDefinition{Name: "second"}},
	)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	definitions := registry.Definitions()
	if len(definitions) != 2 || definitions[0].Name != "first" || definitions[1].Name != "second" {
		t.Fatalf("Definitions() = %#v, want registration order", definitions)
	}
	definitions[0].InputSchema[0] = '['
	if string(registry.Definitions()[0].InputSchema) != `{"type":"object"}` {
		t.Errorf("Definitions() shared input schema memory")
	}
	if _, err := registry.Find("first"); err != nil {
		t.Errorf("Find(first) error = %v", err)
	}
	if _, err := registry.Find("missing"); !errors.Is(err, ErrToolNotFound) {
		t.Errorf("Find(missing) error = %v, want ErrToolNotFound", err)
	}
}

func TestNewRegistryRejectsInvalidTools(t *testing.T) {
	valid := testTool{definition: agent.ToolDefinition{Name: "tool"}}
	tests := []struct {
		name  string
		tools []Tool
		err   error
	}{
		{name: "nil tool", tools: []Tool{nil}, err: ErrToolRequired},
		{name: "empty name", tools: []Tool{testTool{}}, err: ErrToolNameRequired},
		{name: "duplicate name", tools: []Tool{valid, valid}, err: ErrDuplicateToolName},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewRegistry(test.tools...); !errors.Is(err, test.err) {
				t.Errorf("NewRegistry() error = %v, want %v", err, test.err)
			}
		})
	}
}
