package chat

import (
	"testing"

	"k8s-watson/internal/agent"
)

func TestContextMessagesKeepsCompletedTurnsInOrder(t *testing.T) {
	turns := []Turn{
		{
			ID:    1,
			State: StateCompleted,
			Messages: []agent.Message{
				{Role: agent.RoleUser, Content: "first"},
				{Role: agent.RoleAssistant, Content: "answer"},
			},
		},
		{
			ID:       2,
			State:    StateFailed,
			Messages: []agent.Message{{Role: agent.RoleUser, Content: "failed"}},
		},
		{
			ID:       3,
			State:    StateCallingModel,
			Messages: []agent.Message{{Role: agent.RoleUser, Content: "current"}},
		},
	}

	messages := contextMessages(turns, 3, 100)
	if len(messages) != 4 {
		t.Fatalf("messages = %#v, want system and two completed messages plus current question", messages)
	}
	if messages[0].Role != agent.RoleSystem || messages[1].Content != "first" || messages[2].Content != "answer" || messages[3].Content != "current" {
		t.Errorf("messages = %#v, want ordered completed context and current question", messages)
	}
}

func TestContextMessagesTrimsToNewestContinuousSuffix(t *testing.T) {
	turns := []Turn{
		{
			ID:       1,
			State:    StateCompleted,
			Messages: []agent.Message{{Role: agent.RoleUser, Content: "oldturn"}},
		},
		{
			ID:       2,
			State:    StateCompleted,
			Messages: []agent.Message{{Role: agent.RoleUser, Content: "1234567890"}},
		},
		{
			ID:       3,
			State:    StateCompleted,
			Messages: []agent.Message{{Role: agent.RoleUser, Content: "new"}},
		},
		{
			ID:       4,
			State:    StateCallingModel,
			Messages: []agent.Message{{Role: agent.RoleUser, Content: "x"}},
		},
	}

	messages := contextMessages(turns, 4, 12)
	if len(messages) != 3 || messages[1].Content != "new" || messages[2].Content != "x" {
		t.Errorf("messages = %#v, want only newest fitting completed suffix and current turn", messages)
	}
}

func TestContextMessagesCountsRunesAndKeepsOversizedCurrentTurn(t *testing.T) {
	turns := []Turn{
		{
			ID:    1,
			State: StateCompleted,
			Messages: []agent.Message{
				{Role: agent.RoleUser, Content: "ёж"},
				{Role: agent.RoleAssistant, Content: "🙂"},
			},
		},
		{
			ID:       2,
			State:    StateCallingModel,
			Messages: []agent.Message{{Role: agent.RoleUser, Content: "current"}},
		},
	}

	messages := contextMessages(turns, 2, 10)
	if len(messages) != 4 {
		t.Fatalf("messages = %#v, want previous three-rune turn and current turn", messages)
	}
	oversizedCurrent := contextMessages(turns[1:], 2, 1)
	if len(oversizedCurrent) != 2 || oversizedCurrent[1].Content != "current" {
		t.Errorf("oversized current messages = %#v, want current turn retained", oversizedCurrent)
	}
}

func TestContextMessagesClonesMutableFields(t *testing.T) {
	arguments := []byte(`{"verb":"get"}`)
	turns := []Turn{{
		ID:    1,
		State: StateCallingModel,
		Messages: []agent.Message{{
			Role: agent.RoleAssistant,
			ToolCalls: []agent.ToolCall{{
				ID:        "call-1",
				Name:      "kubectl",
				Arguments: arguments,
			}},
			ToolResult: &agent.ToolResult{Content: "original"},
		}},
	}}

	messages := contextMessages(turns, 1, 100)
	messages[1].ToolCalls[0].Arguments[0] = '['
	messages[1].ToolResult.Content = "changed"
	if turns[0].Messages[0].ToolCalls[0].Arguments[0] != '{' {
		t.Errorf("original arguments = %q, want unchanged", turns[0].Messages[0].ToolCalls[0].Arguments)
	}
	if turns[0].Messages[0].ToolResult.Content != "original" {
		t.Errorf("original result = %q, want unchanged", turns[0].Messages[0].ToolResult.Content)
	}
}
