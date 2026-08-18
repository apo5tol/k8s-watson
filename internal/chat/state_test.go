package chat

import "testing"

func TestStateActivityAndInputAcceptance(t *testing.T) {
	tests := []struct {
		name         string
		state        State
		active       bool
		acceptsInput bool
	}{
		{name: "unknown", state: StateUnknown, acceptsInput: true},
		{name: "idle", state: StateIdle, acceptsInput: true},
		{name: "calling model", state: StateCallingModel, active: true},
		{name: "tool proposed", state: StateToolProposed, active: true},
		{name: "awaiting approval", state: StateAwaitingApproval, active: true},
		{name: "running tool", state: StateRunningTool, active: true},
		{name: "completed", state: StateCompleted, acceptsInput: true},
		{name: "failed", state: StateFailed, acceptsInput: true},
		{name: "cancelled", state: StateCancelled, acceptsInput: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.state.Active(); got != test.active {
				t.Errorf("Active() = %t, want %t", got, test.active)
			}
			if got := test.state.AcceptsInput(); got != test.acceptsInput {
				t.Errorf("AcceptsInput() = %t, want %t", got, test.acceptsInput)
			}
		})
	}
}
