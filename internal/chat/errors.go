package chat

import "errors"

var (
	ErrModelRequired = errors.New("chat: model is required")
	ErrInvalidConfig = errors.New("chat: invalid configuration")
	ErrBusy          = errors.New("chat: engine is busy")
	ErrActiveTurn    = errors.New("chat: active turn")
	ErrInputTooLong  = errors.New("chat: input exceeds size limit")
	ErrMaxIterations = errors.New("chat: maximum iterations reached")
	ErrClosed        = errors.New("chat: engine is closed")
)
