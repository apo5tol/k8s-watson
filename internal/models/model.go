package models

import (
	"context"

	"k8s-watson/internal/agent"
)

type Model interface {
	Chat(context.Context, Request) (Response, error)
}

type Request struct {
	Messages []agent.Message
	Tools    []agent.ToolDefinition
}

type Response struct {
	Text      string
	ToolCalls []agent.ToolCall
}
