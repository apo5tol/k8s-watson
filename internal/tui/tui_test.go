package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func TestViewUsesFullScreenAndFillsViewport(t *testing.T) {
	m := newModel()
	view := m.View()

	if !view.AltScreen {
		t.Error("View().AltScreen = false, want true")
	}
	if !m.viewport.FillHeight {
		t.Error("viewport.FillHeight = false, want true")
	}
	if view.MouseMode != tea.MouseModeCellMotion {
		t.Errorf("View().MouseMode = %v, want %v", view.MouseMode, tea.MouseModeCellMotion)
	}
}

func TestInputHeightGrowsToThreeLines(t *testing.T) {
	m := newModel()

	if got := m.input.Height(); got != minimumInputHeight {
		t.Errorf("empty input height = %d, want %d", got, minimumInputHeight)
	}

	m.input.SetValue("one\ntwo\nthree\nfour")
	m.resize()
	if got := m.input.Height(); got != maximumInputHeight {
		t.Errorf("multiline input height = %d, want %d", got, maximumInputHeight)
	}
}

func TestEnterSubmitsQuestionFromReadyState(t *testing.T) {
	m := newModel()
	m.input.SetValue("show pods")

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	got := updated.(model)

	if got.state != stateProcessing {
		t.Errorf("state = %q, want %q", got.state, stateProcessing)
	}
	if len(got.history) != 2 || got.history[1] != (message{author: "You", content: "show pods"}) {
		t.Errorf("history = %#v, want submitted question", got.history)
	}
}

func TestEnterIgnoresEmptyAndConcurrentQuestions(t *testing.T) {
	t.Run("empty input", func(t *testing.T) {
		m := newModel()

		got := updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
		if got.state != stateReady || len(got.history) != 1 {
			t.Errorf("after empty submit state = %q, history = %#v; want ready state and unchanged history", got.state, got.history)
		}
	})

	t.Run("processing input", func(t *testing.T) {
		m := newModel()
		m.state = stateProcessing
		m.input.SetValue("second question")

		got := updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
		if got.state != stateProcessing || len(got.history) != 1 || got.input.Value() != "second question" {
			t.Errorf("after concurrent submit state = %q, history = %#v, input = %q; want unchanged processing state", got.state, got.history, got.input.Value())
		}
	})
}

func TestCtrlJAddsNewline(t *testing.T) {
	m := newModel()
	m.input.SetValue("first")

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl})
	got := updated.(model)

	if got.input.Value() != "first\n" {
		t.Errorf("input = %q, want newline", got.input.Value())
	}
}

func TestShiftEnterAddsNewline(t *testing.T) {
	m := newModel()
	m.input.SetValue("first")

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift})
	got := updated.(model)

	if got.input.Value() != "first\n" {
		t.Errorf("input = %q, want newline", got.input.Value())
	}
}

func TestCancelKeysClearInputOrCancelRequest(t *testing.T) {
	for _, key := range []tea.KeyPressMsg{
		{Code: tea.KeyEscape},
		{Code: 'c', Mod: tea.ModCtrl},
	} {
		t.Run(key.String(), func(t *testing.T) {
			m := newModel()
			m.input.SetValue("draft")

			got := updateModel(t, m, key)
			if got.input.Value() != "" || got.state != stateReady || len(got.history) != 1 {
				t.Errorf("idle cancel input = %q, state = %q, history = %#v; want cleared ready input", got.input.Value(), got.state, got.history)
			}

			m = newModel()
			m.state = stateProcessing
			m.turnID = 4
			got = updateModel(t, m, key)
			if got.state != stateReady || got.turnID != 5 || got.history[len(got.history)-1] != (message{author: "Watson", content: "Request cancelled."}) {
				t.Errorf("processing cancel state = %q, turn ID = %d, history = %#v; want cancelled request", got.state, got.turnID, got.history)
			}
		})
	}
}

func TestResponseCompletesOnlyCurrentRequest(t *testing.T) {
	m := newModel()
	m.state = stateProcessing
	m.turnID = 3

	stale := updateModel(t, m, responseMsg{id: 2, response: "old"})
	if stale.state != stateProcessing || len(stale.history) != 1 {
		t.Errorf("stale response state = %q, history = %#v; want unchanged processing request", stale.state, stale.history)
	}

	got := updateModel(t, stale, responseMsg{id: 3, response: "Echo: pods"})
	if got.state != stateReady || got.history[len(got.history)-1] != (message{author: "Watson", content: "Echo: pods"}) {
		t.Errorf("current response state = %q, history = %#v; want echoed response in ready state", got.state, got.history)
	}
}

func TestSlashCommands(t *testing.T) {
	tests := []struct {
		name         string
		command      string
		state        turnState
		wantHistory  int
		wantLastText string
	}{
		{name: "help", command: "/help", state: stateReady, wantHistory: 2, wantLastText: "Commands: /help, /clear, /quit"},
		{name: "clear", command: "/clear", state: stateReady, wantHistory: 1},
		{name: "unknown", command: "/invalid", state: stateReady, wantHistory: 2, wantLastText: "Unknown command: /invalid"},
		{name: "clear while processing", command: "/clear", state: stateProcessing, wantHistory: 2, wantLastText: "Cannot clear history while a request is active. Cancel it first."},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m := newModel()
			m.state = test.state
			m.input.SetValue(test.command)

			got := updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
			if len(got.history) != test.wantHistory {
				t.Fatalf("history = %#v, want %d entries", got.history, test.wantHistory)
			}
			if test.wantLastText != "" && !strings.Contains(got.history[len(got.history)-1].content, test.wantLastText) {
				t.Errorf("last history entry = %q, want it to contain %q", got.history[len(got.history)-1].content, test.wantLastText)
			}
		})
	}
}

func TestQuitCommandsReturnQuitMessage(t *testing.T) {
	for _, key := range []tea.KeyPressMsg{
		{Code: 'q', Mod: tea.ModCtrl},
		{Code: tea.KeyEnter},
	} {
		t.Run(key.String(), func(t *testing.T) {
			m := newModel()
			if key.Code == tea.KeyEnter {
				m.input.SetValue("/quit")
			}

			_, cmd := m.Update(key)
			if cmd == nil {
				t.Fatal("quit command returned nil")
			}
			if _, ok := cmd().(tea.QuitMsg); !ok {
				t.Errorf("quit command message = %T, want tea.QuitMsg", cmd())
			}
		})
	}
}

func TestResizeUpdatesComponentDimensions(t *testing.T) {
	m := newModel()
	m.input.SetValue("one\ntwo\nthree")

	got := updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	if got.width != 100 || got.height != 30 {
		t.Errorf("model size = %dx%d, want 100x30", got.width, got.height)
	}
	if got.input.Width() != 92 || got.input.Height() != 3 || got.viewport.Width() != 94 || got.viewport.Height() != 22 {
		t.Errorf("component sizes input = %dx%d, viewport = %dx%d; want input 92x3 and viewport 94x22", got.input.Width(), got.input.Height(), got.viewport.Width(), got.viewport.Height())
	}

	got = updateModel(t, got, tea.WindowSizeMsg{Width: 1, Height: 1})
	if got.input.Width() != minimumComponentSize || got.viewport.Width() != minimumComponentSize || got.viewport.Height() != minimumComponentSize {
		t.Errorf("small-window dimensions input width = %d, viewport = %dx%d; want minimum component sizes", got.input.Width(), got.viewport.Width(), got.viewport.Height())
	}
}

func TestViewShowsCurrentStatus(t *testing.T) {
	m := newModel()
	if view := m.View().Content; !strings.Contains(view, "Status: ready") {
		t.Errorf("ready view = %q, want ready status", view)
	}

	m.state = stateProcessing
	if view := m.View().Content; !strings.Contains(view, "Status: processing") {
		t.Errorf("processing view = %q, want processing status", view)
	}
}

func TestLongResponseStaysWithinWindowAndHistoryCanScroll(t *testing.T) {
	m := updateModel(t, newModel(), tea.WindowSizeMsg{Width: 100, Height: 30})
	m.state = stateProcessing
	m.turnID = 1

	got := updateModel(t, m, responseMsg{id: 1, response: strings.Repeat("a long response ", 200)})
	if height := lipgloss.Height(got.View().Content); height != 30 {
		t.Errorf("view height = %d, want 30", height)
	}
	if got.viewport.TotalLineCount() <= got.viewport.Height() {
		t.Fatalf("history lines = %d, want more than viewport height %d", got.viewport.TotalLineCount(), got.viewport.Height())
	}

	got = updateModel(t, got, tea.KeyPressMsg{Code: tea.KeyPgUp})
	if got.viewport.AtBottom() {
		t.Error("viewport remains at bottom after PageUp")
	}

	got.viewport.GotoBottom()
	got = updateModel(t, got, tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	if got.viewport.AtBottom() {
		t.Error("viewport remains at bottom after mouse wheel up")
	}
}

func TestLongResponseRemainsScrollableAfterResize(t *testing.T) {
	m := updateModel(t, newModel(), tea.WindowSizeMsg{Width: 100, Height: 30})
	m.state = stateProcessing
	m.turnID = 1

	got := updateModel(t, m, responseMsg{id: 1, response: strings.Repeat("a long response ", 200)})
	got = updateModel(t, got, tea.WindowSizeMsg{Width: 60, Height: 20})
	if height := lipgloss.Height(got.View().Content); height != 20 {
		t.Errorf("view height after resize = %d, want 20", height)
	}
	if got.viewport.TotalLineCount() <= got.viewport.Height() {
		t.Fatalf("history lines after resize = %d, want more than viewport height %d", got.viewport.TotalLineCount(), got.viewport.Height())
	}

	got.viewport.GotoBottom()
	got = updateModel(t, got, tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	if got.viewport.AtBottom() {
		t.Error("viewport remains at bottom after resize and mouse wheel up")
	}
}

func updateModel(t *testing.T, m model, msg tea.Msg) model {
	t.Helper()
	updated, _ := m.Update(msg)
	return updated.(model)
}
