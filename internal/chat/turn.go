package chat

import "k8s-watson/internal/agent"

type TurnID uint64

type EntryKind string

const (
	EntryUnknown   EntryKind = ""
	EntryUser      EntryKind = "user"
	EntryAssistant EntryKind = "assistant"
	EntryTool      EntryKind = "tool"
	EntryRejected  EntryKind = "rejected"
	EntryError     EntryKind = "error"
	EntryCancelled EntryKind = "cancelled"
)

type Entry struct {
	Kind   EntryKind
	Text   string
	TurnID TurnID
}

type Turn struct {
	ID       TurnID
	State    State
	Messages []agent.Message
	Entries  []Entry
	Err      error
}
