package tools

import (
	"context"

	"k8s-watson/internal/agent"
)

type Tool interface {
	Definition() agent.ToolDefinition
	Prepare(context.Context, agent.ToolCall) (PreparedCall, error)
}

type PreparedCall interface {
	Display() string
	RequiresApproval() bool
	Metadata() map[string]string
	Execute(context.Context) (agent.ToolResult, error)
}
