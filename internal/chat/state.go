package chat

type State string

const (
	StateUnknown          State = ""
	StateIdle             State = "idle"
	StateCallingModel     State = "calling model"
	StateToolProposed     State = "tool proposed"
	StateAwaitingApproval State = "awaiting approval"
	StateRunningTool      State = "running tool"
	StateCompleted        State = "completed"
	StateFailed           State = "failed"
	StateCancelled        State = "cancelled"
)

func (s State) Active() bool {
	switch s {
	case StateCallingModel, StateToolProposed, StateAwaitingApproval, StateRunningTool:
		return true
	default:
		return false
	}
}

func (s State) AcceptsInput() bool {
	return !s.Active()
}
