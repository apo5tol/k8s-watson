package chat

import (
	"encoding/json"
	"unicode/utf8"

	"k8s-watson/internal/agent"
)

func contextMessages(turns []Turn, current TurnID, maxChars int) []agent.Message {
	messages := []agent.Message{{Role: agent.RoleSystem, Content: systemPrompt}}
	currentIndex := -1
	for index := range turns {
		if turns[index].ID == current {
			currentIndex = index
			break
		}
	}
	if currentIndex < 0 {
		return messages
	}

	selected := []Turn{turns[currentIndex]}
	usedChars := turnChars(turns[currentIndex])
	for index := currentIndex - 1; index >= 0; index-- {
		turn := turns[index]
		if turn.State != StateCompleted {
			continue
		}
		chars := turnChars(turn)
		if usedChars+chars > maxChars {
			break
		}
		selected = append([]Turn{turn}, selected...)
		usedChars += chars
	}
	for _, turn := range selected {
		for _, message := range turn.Messages {
			messages = append(messages, cloneMessage(message))
		}
	}

	return messages
}

func turnChars(turn Turn) int {
	chars := 0
	for _, message := range turn.Messages {
		chars += utf8.RuneCountInString(message.Content)
	}
	return chars
}

func cloneMessage(message agent.Message) agent.Message {
	clone := message
	clone.ToolCalls = make([]agent.ToolCall, len(message.ToolCalls))
	for index, call := range message.ToolCalls {
		clone.ToolCalls[index] = call
		clone.ToolCalls[index].Arguments = append(json.RawMessage{}, call.Arguments...)
	}
	if message.ToolResult != nil {
		result := *message.ToolResult
		clone.ToolResult = &result
	}

	return clone
}
