package tui

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"k8s-watson/internal/chat"
)

const (
	defaultWidth           = 80
	defaultHeight          = 24
	minimumInputHeight     = 1
	maximumInputHeight     = 3
	panelHorizontalMargin  = 2
	minimumComponentSize   = 1
	panelVerticalPadding   = 0
	panelHorizontalPadding = 1
	colorBackground        = "#2B2B2B"
	colorPanelBackground   = "#313335"
	colorPanelBorder       = "#515151"
	colorText              = "#A9B7C6"
	colorMutedText         = "#808080"
	colorAccent            = "#9876AA"
	colorLogo              = "#6897BB"
	colorUser              = "#6A8759"
	colorCursor            = "#CC7832"
	logo                   = ``
)

var (
	headerStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(colorLogo))
	panelStyle  = lipgloss.NewStyle().Background(lipgloss.Color(colorPanelBackground)).Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color(colorPanelBorder)).Padding(panelVerticalPadding, panelHorizontalPadding)
	userStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colorUser))
	botStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colorAccent))
	toolStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colorCursor))
	statusStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(colorMutedText))
	appStyle    = lipgloss.NewStyle().Background(lipgloss.Color(colorBackground)).Foreground(lipgloss.Color(colorText))
)

type Engine interface {
	Submit(string) error
	Cancel() bool
	Clear() error
	Approve() error
	Reject() error
	Snapshot() chat.Snapshot
	Events() <-chan chat.Event
	Close()
}

type eventMsg struct{ event chat.Event }
type quitMsg struct{}
type shutdownMsg struct{}

type notice struct {
	author  string
	content string
}

type model struct {
	engine   Engine
	snapshot chat.Snapshot
	notices  []notice
	input    textarea.Model
	viewport viewport.Model
	spinner  spinner.Model
	width    int
	height   int
}

func Run(engine Engine) error {
	if engine == nil {
		return errors.New("TUI engine is required")
	}

	program := tea.NewProgram(newModel(engine), tea.WithoutSignalHandler())
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)

	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-signals:
			program.Send(shutdownMsg{})
		case <-done:
		}
	}()

	if _, err := program.Run(); err != nil {
		return fmt.Errorf("run TUI: %w", err)
	}
	return nil
}

func newModel(engine Engine) model {
	input := textarea.New()
	input.Placeholder = "Ask a question about Kubernetes…"
	input.Focus()
	input.SetHeight(minimumInputHeight)
	input.ShowLineNumbers = false
	input.SetStyles(darculaInputStyles())

	spinnerModel := spinner.New()
	spinnerModel.Spinner = spinner.Dot
	spinnerModel.Style = lipgloss.NewStyle().Foreground(lipgloss.Color(colorAccent))

	m := model{
		engine:   engine,
		snapshot: engine.Snapshot(),
		notices:  []notice{},
		input:    input,
		viewport: viewport.New(viewport.WithWidth(panelContentWidth(defaultWidth)), viewport.WithHeight(minimumComponentSize)),
		spinner:  spinnerModel,
		width:    defaultWidth,
		height:   defaultHeight,
	}
	m.viewport.FillHeight = true
	m.refreshHistory()
	m.resize()
	return m
}

func (m model) Init() tea.Cmd {
	return tea.Batch(textarea.Blink, m.waitEvent())
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resize()
	case quitMsg:
		return m, tea.Quit
	case shutdownMsg:
		return m, m.quit()
	case eventMsg:
		follow := m.viewport.AtBottom()
		m.snapshot = msg.event.Snapshot
		m.refreshHistory()
		if follow {
			m.viewport.GotoBottom()
		}
		return m, m.waitEvent()
	case spinner.TickMsg:
		if m.snapshot.State.Active() {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}

	var inputCmd, viewportCmd tea.Cmd
	m.input, inputCmd = m.input.Update(msg)
	m.viewport, viewportCmd = m.viewport.Update(msg)
	return m, tea.Batch(inputCmd, viewportCmd)
}

func (m model) View() tea.View {
	history := panelStyle.Width(m.width - panelHorizontalMargin).Render(m.viewport.View())
	status := statusStyle.Render("Status: " + string(m.snapshot.State))
	if m.snapshot.Approval != nil {
		approval := m.snapshot.Approval
		status = statusStyle.Render(fmt.Sprintf(
			"Approve command? [y/n] %s (context: %s, namespace: %s)",
			approval.Command,
			approval.Context,
			approval.Namespace,
		))
	}
	if m.snapshot.State.Active() {
		status = statusStyle.Render(m.spinner.View() + " Status: " + string(m.snapshot.State))
	}
	input := panelStyle.Width(m.width - panelHorizontalMargin).Render(m.input.View())
	view := tea.NewView(appStyle.Render(lipgloss.JoinVertical(lipgloss.Left, history, status, input)))
	view.AltScreen = true
	view.BackgroundColor = lipgloss.Color(colorBackground)
	view.ForegroundColor = lipgloss.Color(colorText)
	view.KeyboardEnhancements.ReportAllKeysAsEscapeCodes = true
	view.MouseMode = tea.MouseModeCellMotion
	return view
}

func (m model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if handled, command := m.handleApprovalKey(msg.String()); handled {
		return m, command
	}

	switch msg.String() {
	case "ctrl+q":
		return m, m.quit()
	case "esc", "ctrl+c":
		if m.snapshot.State.Active() {
			m.engine.Cancel()
			m.snapshot = m.engine.Snapshot()
			m.refreshHistory()
			return m, nil
		}
		if m.input.Value() != "" {
			m.input.Reset()
			m.resize()
		}
		return m, nil
	case "shift+enter", "ctrl+j":
		m.input.InsertString("\n")
		m.resize()
		return m, nil
	case "enter":
		return m.submit()
	}

	var inputCmd, viewportCmd tea.Cmd
	m.input, inputCmd = m.input.Update(msg)
	m.viewport, viewportCmd = m.viewport.Update(msg)
	m.resize()
	return m, tea.Batch(inputCmd, viewportCmd)
}

func (m *model) handleApprovalKey(key string) (bool, tea.Cmd) {
	if m.snapshot.State != chat.StateAwaitingApproval {
		return false, nil
	}

	switch key {
	case "y":
		if err := m.engine.Approve(); err != nil {
			m.appendNotice("Cannot approve command: " + err.Error())
		}
		return true, m.spinner.Tick
	case "n":
		if err := m.engine.Reject(); err != nil {
			m.appendNotice("Cannot reject command: " + err.Error())
		}
		return true, nil
	default:
		return false, nil
	}
}

func (m model) submit() (tea.Model, tea.Cmd) {
	question := strings.TrimSpace(m.input.Value())
	if question == "" {
		return m, nil
	}
	if strings.HasPrefix(question, "/") {
		return m.runCommand(question)
	}
	if err := m.engine.Submit(question); err != nil {
		m.appendNotice("Request failed: " + err.Error())
		return m, nil
	}
	m.input.Reset()
	m.resize()
	m.snapshot = m.engine.Snapshot()
	m.refreshHistory()
	m.viewport.GotoBottom()
	return m, m.spinner.Tick
}

func (m model) runCommand(command string) (tea.Model, tea.Cmd) {
	m.input.Reset()
	m.resize()
	switch command {
	case "/help":
		m.appendNotice("Commands: /help, /clear, /quit\nKeys: Enter send; Ctrl+J newline; y approve; n reject; Esc cancel; Ctrl+Q quit.")
	case "/clear":
		if err := m.engine.Clear(); err != nil {
			m.appendNotice("Cannot clear history while a request is active. Cancel it first.")
			return m, nil
		}
		m.snapshot = m.engine.Snapshot()
		m.notices = []notice{}
		m.refreshHistory()
	case "/quit":
		return m, m.quit()
	default:
		m.appendNotice("Unknown command: " + command)
	}
	m.viewport.GotoBottom()
	return m, nil
}

func (m *model) appendNotice(content string) {
	follow := m.viewport.AtBottom()
	m.notices = append(m.notices, notice{author: "Watson", content: content})
	m.refreshHistory()
	if follow {
		m.viewport.GotoBottom()
	}
}

func (m model) quit() tea.Cmd {
	return func() tea.Msg {
		m.engine.Close()
		return quitMsg{}
	}
}

func (m model) waitEvent() tea.Cmd {
	return func() tea.Msg {
		event := <-m.engine.Events()
		return eventMsg{event: event}
	}
}

func (m *model) refreshHistory() {
	entries := []string{headerStyle.Render(logo)}
	for _, entry := range m.snapshot.Entries {
		author := "Watson"
		style := botStyle
		switch entry.Kind {
		case chat.EntryUser:
			author = "You"
			style = userStyle
		case chat.EntryTool:
			style = toolStyle
		}
		entries = append(entries, style.Render(author)+"\n"+entry.Text)
	}
	for _, entry := range m.notices {
		style := botStyle
		if entry.author == "You" {
			style = userStyle
		}
		entries = append(entries, style.Render(entry.author)+"\n"+entry.content)
	}
	m.viewport.SetContent(lipgloss.Wrap(strings.Join(entries, "\n\n"), m.viewport.Width(), ""))
}

func (m *model) resize() {
	panelWidth := panelContentWidth(m.width)
	isHistoryWidthChanged := m.viewport.Width() != panelWidth
	m.input.SetHeight(inputHeight(m.input.Value()))
	m.viewport.SetWidth(panelWidth)
	m.viewport.SetHeight(max(m.height-statusHeight()-panelStyle.GetVerticalFrameSize()*2-m.input.Height(), minimumComponentSize))
	m.input.SetWidth(panelWidth)
	if isHistoryWidthChanged {
		m.refreshHistory()
	}
}

func panelContentWidth(width int) int {
	return max(width-panelHorizontalMargin-panelStyle.GetHorizontalFrameSize(), minimumComponentSize)
}

func statusHeight() int { return 1 }

func inputHeight(value string) int {
	return min(maximumInputHeight, max(minimumInputHeight, strings.Count(value, "\n")+1))
}

func darculaInputStyles() textarea.Styles {
	styles := textarea.DefaultDarkStyles()
	background := lipgloss.Color(colorPanelBackground)
	text := lipgloss.Color(colorText)
	mutedText := lipgloss.Color(colorMutedText)
	accent := lipgloss.Color(colorAccent)
	styles.Focused.Base = styles.Focused.Base.Background(background)
	styles.Focused.Text = styles.Focused.Text.Foreground(text).Background(background)
	styles.Focused.Prompt = styles.Focused.Prompt.Foreground(accent).Background(background)
	styles.Focused.Placeholder = styles.Focused.Placeholder.Foreground(mutedText).Background(background)
	styles.Focused.CursorLine = styles.Focused.CursorLine.Background(background)
	styles.Blurred.Base = styles.Blurred.Base.Background(background)
	styles.Blurred.Text = styles.Blurred.Text.Foreground(text).Background(background)
	styles.Blurred.Prompt = styles.Blurred.Prompt.Foreground(accent).Background(background)
	styles.Blurred.Placeholder = styles.Blurred.Placeholder.Foreground(mutedText).Background(background)
	styles.Cursor.Color = lipgloss.Color(colorCursor)
	return styles
}
