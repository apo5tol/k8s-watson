package kubectl

import "errors"

var (
	ErrInvalidCall      = errors.New("kubectl: invalid call")
	ErrForbiddenCall    = errors.New("kubectl: forbidden call")
	ErrExecutorRequired = errors.New("kubectl: executor is required")
)
