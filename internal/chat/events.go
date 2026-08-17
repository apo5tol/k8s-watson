package chat

type EventKind string

const (
	EventUnknown        EventKind = ""
	EventStateChanged   EventKind = "state changed"
	EventHistoryChanged EventKind = "history changed"
	EventTurnCompleted  EventKind = "turn completed"
	EventTurnFailed     EventKind = "turn failed"
	EventTurnCancelled  EventKind = "turn cancelled"
	EventHistoryCleared EventKind = "history cleared"
)

type Snapshot struct {
	State   State
	TurnID  TurnID
	Entries []Entry
}

type Event struct {
	Kind     EventKind
	TurnID   TurnID
	Snapshot Snapshot
	Err      error
}
