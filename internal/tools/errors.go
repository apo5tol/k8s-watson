package tools

import "errors"

var (
	ErrToolRequired      = errors.New("tools: tool is required")
	ErrRegistryRequired  = errors.New("tools: registry is required")
	ErrToolNameRequired  = errors.New("tools: tool name is required")
	ErrDuplicateToolName = errors.New("tools: duplicate tool name")
	ErrToolNotFound      = errors.New("tools: tool not found")
)
