package kubectl

import "errors"

var (
	ErrInvalidCall            = errors.New("kubectl: invalid call")
	ErrForbiddenCall          = errors.New("kubectl: forbidden call")
	ErrExecutorRequired       = errors.New("kubectl: executor is required")
	ErrInvalidExecutor        = errors.New("kubectl: invalid executor configuration")
	ErrTargetResolverRequired = errors.New("kubectl: target resolver is required")
)
