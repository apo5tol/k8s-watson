package chat

import (
	"context"
	"errors"

	"k8s-watson/internal/agent"
	"k8s-watson/internal/models"
)

type Service struct {
	model models.Model
}

func New(model models.Model) (*Service, error) {
	if model == nil {
		return nil, errors.New("chat model is required")
	}

	return &Service{model: model}, nil
}

func (s *Service) Ask(ctx context.Context, question string) (string, error) {
	response, err := s.model.Chat(ctx, models.Request{
		Messages: []agent.Message{{
			Role:    agent.RoleUser,
			Content: question,
		}},
		Tools: []agent.ToolDefinition{},
	})
	if err != nil {
		return "", err
	}
	if len(response.ToolCalls) != 0 {
		return "", errors.New("chat service does not support tool calls")
	}

	return response.Text, nil
}
