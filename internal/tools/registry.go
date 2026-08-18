package tools

import (
	"encoding/json"
	"fmt"
	"strings"

	"k8s-watson/internal/agent"
)

type Registry struct {
	tools       map[string]Tool
	definitions []agent.ToolDefinition
}

func NewRegistry(registeredTools ...Tool) (*Registry, error) {
	registry := &Registry{
		tools:       make(map[string]Tool, len(registeredTools)),
		definitions: make([]agent.ToolDefinition, 0, len(registeredTools)),
	}
	for _, tool := range registeredTools {
		if tool == nil {
			return nil, ErrToolRequired
		}

		definition := tool.Definition()
		name := strings.TrimSpace(definition.Name)
		if name == "" {
			return nil, ErrToolNameRequired
		}
		if _, exists := registry.tools[name]; exists {
			return nil, fmt.Errorf("%w: %q", ErrDuplicateToolName, name)
		}

		definition.Name = name
		definition.InputSchema = append(json.RawMessage{}, definition.InputSchema...)
		registry.tools[name] = tool
		registry.definitions = append(registry.definitions, definition)
	}
	return registry, nil
}

func (r *Registry) Definitions() []agent.ToolDefinition {
	definitions := make([]agent.ToolDefinition, len(r.definitions))
	for index, definition := range r.definitions {
		definitions[index] = definition
		definitions[index].InputSchema = append(json.RawMessage{}, definition.InputSchema...)
	}
	return definitions
}

func (r *Registry) Find(name string) (Tool, error) {
	tool, exists := r.tools[name]
	if !exists {
		return nil, fmt.Errorf("%w: %q", ErrToolNotFound, name)
	}
	return tool, nil
}
