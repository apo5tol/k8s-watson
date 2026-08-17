package agent

import "encoding/json"

type ToolDefinition struct {
	Name        string
	Description string
	InputSchema json.RawMessage
}

type ToolCall struct {
	ID        string
	Name      string
	Arguments json.RawMessage
}

type ToolResult struct {
	ToolCallID string
	ToolName   string
	Content    string
}
