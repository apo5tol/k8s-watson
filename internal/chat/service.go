package chat

import (
	"context"
	"errors"
)

type Model interface {
	Chat(context.Context, string) (string, error)
}

type Service struct {
	model Model
}

func New(model Model) (*Service, error) {
	if model == nil {
		return nil, errors.New("chat model is required")
	}

	return &Service{model: model}, nil
}

func (s *Service) Ask(ctx context.Context, question string) (string, error) {
	return s.model.Chat(ctx, question)
}
