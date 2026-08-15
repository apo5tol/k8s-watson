package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

const testMaxHistoryChars = 65_536

func TestViewUsesFullScreenAndFillsViewport(t *testing.T) {
	m := newTestModel()
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
	m := newTestModel()

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
	m := newTestModel()
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

func TestSubmittingQuestionStartsSpinner(t *testing.T) {
	m := newTestModel()
	m.input.SetValue("show pods")

	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("submit command = nil, want spinner and request commands")
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		t.Fatalf("submit command message = %T, want tea.BatchMsg", msg)
	}

	var tick spinner.TickMsg
	for _, batchCmd := range batch {
		if msg, ok := batchCmd().(spinner.TickMsg); ok {
			tick = msg
			break
		}
	}
	if tick.ID == 0 {
		t.Fatal("submit batch does not contain spinner tick")
	}

	got := updated.(model)
	_, nextTick := got.Update(tick)
	if nextTick == nil {
		t.Fatal("processing spinner did not schedule next tick")
	}
}

func TestEnterIgnoresEmptyAndConcurrentQuestions(t *testing.T) {
	t.Run("empty input", func(t *testing.T) {
		m := newTestModel()

		got := updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
		if got.state != stateReady || len(got.history) != 1 {
			t.Errorf("after empty submit state = %q, history = %#v; want ready state and unchanged history", got.state, got.history)
		}
	})

	t.Run("processing input", func(t *testing.T) {
		m := newTestModel()
		m.state = stateProcessing
		m.input.SetValue("second question")

		got := updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
		if got.state != stateProcessing || len(got.history) != 1 || got.input.Value() != "second question" {
			t.Errorf("after concurrent submit state = %q, history = %#v, input = %q; want unchanged processing state", got.state, got.history, got.input.Value())
		}
	})
}

func TestCtrlJAddsNewline(t *testing.T) {
	m := newTestModel()
	m.input.SetValue("first")

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl})
	got := updated.(model)

	if got.input.Value() != "first\n" {
		t.Errorf("input = %q, want newline", got.input.Value())
	}
}

func TestShiftEnterAddsNewline(t *testing.T) {
	m := newTestModel()
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
			m := newTestModel()
			m.input.SetValue("draft")

			got := updateModel(t, m, key)
			if got.input.Value() != "" || got.state != stateReady || len(got.history) != 1 {
				t.Errorf("idle cancel input = %q, state = %q, history = %#v; want cleared ready input", got.input.Value(), got.state, got.history)
			}

			m = newTestModel()
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
	m := newTestModel()
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
			m := newTestModel()
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
			m := newTestModel()
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

func TestQuitCancelsAndWaitsForActiveRequest(t *testing.T) {
	requestStarted := make(chan struct{})
	requestCancelled := make(chan struct{})
	client := blockingClient{
		ask: func(ctx context.Context, _ string) (string, error) {
			close(requestStarted)
			<-ctx.Done()
			close(requestCancelled)
			return "", ctx.Err()
		},
	}
	m := newModel(client, testMaxHistoryChars)
	m.input.SetValue("show pods")

	updated, request := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(model)
	requestMsg := request()
	batch, ok := requestMsg.(tea.BatchMsg)
	if !ok {
		t.Fatalf("request command message = %T, want tea.BatchMsg", requestMsg)
	}
	for _, cmd := range batch {
		go cmd()
	}
	<-requestStarted

	updated, quit := m.Update(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl})
	m = updated.(model)
	if m.turnID != 2 {
		t.Errorf("turn ID = %d, want 2", m.turnID)
	}

	quitResult := make(chan tea.Msg, 1)
	go func() {
		quitResult <- quit()
	}()

	select {
	case <-requestCancelled:
	case <-time.After(time.Second):
		t.Fatal("active request was not cancelled")
	}

	select {
	case msg := <-quitResult:
		if _, ok := msg.(quitMsg); !ok {
			t.Fatalf("quit command message = %T, want tui.quitMsg", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("quit command did not wait for request completion")
	}

	_, cmd := m.Update(quitMsg{})
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("quit message command = %T, want tea.QuitMsg", cmd())
	}
}

func TestShutdownMessageCancelsAndWaitsForActiveRequest(t *testing.T) {
	m := newTestModel()
	m.state = stateProcessing
	cancelled := false
	m.cancelRequest = func() { cancelled = true }
	done := make(chan struct{})
	close(done)
	m.requestDone = done

	updated, cmd := m.Update(shutdownMsg{})
	got := updated.(model)
	if !cancelled {
		t.Fatal("shutdown did not cancel active request")
	}
	if got.turnID != 1 {
		t.Errorf("turn ID = %d, want 1", got.turnID)
	}
	msg := cmd()
	if _, ok := msg.(quitMsg); !ok {
		t.Errorf("shutdown command message = %T, want tui.quitMsg", msg)
	}
}

func TestRunRejectsInvalidDependencies(t *testing.T) {
	if err := Run(nil, testMaxHistoryChars); err == nil {
		t.Error("Run(nil) error = nil, want error")
	}
	client := blockingClient{ask: func(context.Context, string) (string, error) { return "", nil }}
	if err := Run(client, 0); err == nil {
		t.Error("Run() with zero history limit error = nil, want error")
	}
}

func TestResizeUpdatesComponentDimensions(t *testing.T) {
	m := newTestModel()
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
	m := newTestModel()
	if view := m.View().Content; !strings.Contains(view, "Status: ready") {
		t.Errorf("ready view = %q, want ready status", view)
	}

	m.state = stateProcessing
	if view := m.View().Content; !strings.Contains(view, "Status: processing") {
		t.Errorf("processing view = %q, want processing status", view)
	}
}

func TestLongResponseStaysWithinWindowAndHistoryCanScroll(t *testing.T) {
	m := updateModel(t, newTestModel(), tea.WindowSizeMsg{Width: 100, Height: 30})
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
	m := updateModel(t, newTestModel(), tea.WindowSizeMsg{Width: 100, Height: 30})
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

func TestHistoryDropsOldEntriesAndTruncatesLatestEntry(t *testing.T) {
	m := newModel(blockingClient{
		ask: func(context.Context, string) (string, error) {
			return "response", nil
		},
	}, 10)
	m.appendHistory(message{author: "You", content: "123456"})
	m.appendHistory(message{author: "Watson", content: "abcdefghijk"})

	if len(m.history) != 2 {
		t.Fatalf("history = %#v, want logo and latest entry", m.history)
	}
	if got := m.history[1].content; got != "abcdefghi…" {
		t.Errorf("latest history entry = %q, want truncated entry", got)
	}
}

func updateModel(t *testing.T, m model, msg tea.Msg) model {
	t.Helper()
	updated, _ := m.Update(msg)
	return updated.(model)
}

type blockingClient struct {
	ask func(context.Context, string) (string, error)
}

func (c blockingClient) Ask(ctx context.Context, question string) (string, error) {
	return c.ask(ctx, question)
}

func newTestModel() model {
	return newModel(blockingClient{
		ask: func(context.Context, string) (string, error) {
			return "response", nil
		},
	}, testMaxHistoryChars)
}
