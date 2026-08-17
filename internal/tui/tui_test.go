package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"k8s-watson/internal/chat"
)

type fakeEngine struct {
	snapshot  chat.Snapshot
	events    chan chat.Event
	submits   []string
	clearErr  error
	cancelled bool
	closed    bool
}

func newFakeEngine() *fakeEngine {
	return &fakeEngine{snapshot: chat.Snapshot{State: chat.StateIdle, Entries: []chat.Entry{}}, events: make(chan chat.Event, 8), submits: []string{}}
}

func (e *fakeEngine) Submit(question string) error {
	e.submits = append(e.submits, question)
	e.snapshot.State = chat.StateCallingModel
	return nil
}

func (e *fakeEngine) Cancel() bool {
	if !e.snapshot.State.Active() {
		return false
	}
	e.cancelled = true
	e.snapshot.State = chat.StateCancelled
	return true
}

func (e *fakeEngine) Clear() error {
	if e.clearErr != nil {
		return e.clearErr
	}
	e.snapshot = chat.Snapshot{State: chat.StateIdle, Entries: []chat.Entry{}}
	return nil
}

func (e *fakeEngine) Snapshot() chat.Snapshot   { return e.snapshot }
func (e *fakeEngine) Events() <-chan chat.Event { return e.events }
func (e *fakeEngine) Close()                    { e.closed = true }

func TestViewUsesFullScreenAndFillsViewport(t *testing.T) {
	m := newModel(newFakeEngine())
	view := m.View()
	if !view.AltScreen || !m.viewport.FillHeight || view.MouseMode != tea.MouseModeCellMotion {
		t.Errorf("view = %#v, want fullscreen viewport with mouse support", view)
	}
}

func TestEnterSubmitsQuestionThroughEngine(t *testing.T) {
	engine := newFakeEngine()
	m := newModel(engine)
	m.input.SetValue("show pods")
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	got := updated.(model)
	if len(engine.submits) != 1 || engine.submits[0] != "show pods" || got.snapshot.State != chat.StateCallingModel {
		t.Errorf("submits = %#v, snapshot = %#v; want question submitted through engine", engine.submits, got.snapshot)
	}
}

func TestEventUpdatesSnapshot(t *testing.T) {
	engine := newFakeEngine()
	m := newModel(engine)
	event := chat.Event{Kind: chat.EventTurnCompleted, Snapshot: chat.Snapshot{State: chat.StateCompleted, Entries: []chat.Entry{{Kind: chat.EntryUser, Text: "show pods"}, {Kind: chat.EntryAssistant, Text: "healthy"}}}}
	updated, _ := m.Update(eventMsg{event: event})
	got := updated.(model)
	if got.snapshot.State != chat.StateCompleted || !strings.Contains(got.viewport.View(), "healthy") {
		t.Errorf("snapshot = %#v, history = %q; want completed answer", got.snapshot, got.viewport.View())
	}
}

func TestCancelDelegatesToEngine(t *testing.T) {
	engine := newFakeEngine()
	engine.snapshot.State = chat.StateCallingModel
	m := newModel(engine)
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	got := updated.(model)
	if !engine.cancelled || got.snapshot.State != chat.StateCancelled {
		t.Errorf("cancelled = %t, snapshot = %#v; want engine cancellation", engine.cancelled, got.snapshot)
	}
}

func TestSlashCommands(t *testing.T) {
	t.Run("clear", func(t *testing.T) {
		engine := newFakeEngine()
		engine.snapshot.Entries = []chat.Entry{{Kind: chat.EntryUser, Text: "old"}}
		m := newModel(engine)
		m.input.SetValue("/clear")
		got := updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
		if len(got.snapshot.Entries) != 0 {
			t.Errorf("entries = %#v, want cleared history", got.snapshot.Entries)
		}
	})
	t.Run("active clear", func(t *testing.T) {
		engine := newFakeEngine()
		engine.clearErr = errors.New("active turn")
		m := newModel(engine)
		m.input.SetValue("/clear")
		got := updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
		if len(got.notices) != 1 || !strings.Contains(got.notices[0].content, "Cannot clear") {
			t.Errorf("notices = %#v, want clear failure", got.notices)
		}
	})
}

func TestQuitClosesEngine(t *testing.T) {
	engine := newFakeEngine()
	m := newModel(engine)
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl})
	result := cmd()
	if _, ok := result.(quitMsg); !ok || !engine.closed {
		t.Errorf("quit result = %T, closed = %t; want close and quit", result, engine.closed)
	}
}

func TestResizeAndScrolling(t *testing.T) {
	engine := newFakeEngine()
	engine.snapshot.Entries = []chat.Entry{{Kind: chat.EntryAssistant, Text: strings.Repeat("a long response ", 200)}}
	m := newModel(engine)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	if m.input.Width() != 92 || m.viewport.Width() != 94 || lipgloss.Height(m.View().Content) != 30 {
		t.Errorf("unexpected resized layout: input %dx%d viewport %dx%d", m.input.Width(), m.input.Height(), m.viewport.Width(), m.viewport.Height())
	}
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyPgUp})
	if m.viewport.AtBottom() {
		t.Error("viewport remains at bottom after PageUp")
	}
}

func TestRunRejectsNilEngine(t *testing.T) {
	if err := Run(nil); err == nil {
		t.Error("Run(nil) error = nil, want error")
	}
}

func updateModel(t *testing.T, m model, msg tea.Msg) model {
	t.Helper()
	updated, _ := m.Update(msg)
	return updated.(model)
}
