package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
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
	responseDelay          = 150 * time.Millisecond
	logo                   = ``
)

var (
	headerStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(colorLogo))
	panelStyle  = lipgloss.NewStyle().Background(lipgloss.Color(colorPanelBackground)).Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color(colorPanelBorder)).Padding(panelVerticalPadding, panelHorizontalPadding)
	userStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colorUser))
	botStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colorAccent))
	statusStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(colorMutedText))
	appStyle    = lipgloss.NewStyle().Background(lipgloss.Color(colorBackground)).Foreground(lipgloss.Color(colorText))
)

type message struct {
	author  string
	content string
}

type turnState string

const (
	stateReady      turnState = "ready"
	stateProcessing turnState = "processing"
)

type responseMsg struct {
	id       int
	response string
	err      error
}

type Client interface {
	Ask(context.Context, string) (string, error)
}

type model struct {
	history       []message
	client        Client
	cancelRequest context.CancelFunc
	input         textarea.Model
	viewport      viewport.Model
	spinner       spinner.Model
	width         int
	height        int
	state         turnState
	turnID        int
}

func Run(client Client) error {
	if _, err := tea.NewProgram(newModel(client)).Run(); err != nil {
		return fmt.Errorf("run TUI: %w", err)
	}

	return nil
}

func newModel(clients ...Client) model {
	var client Client
	if len(clients) > 0 {
		client = clients[0]
	}

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
		history: []message{{content: logo}},
		client:  client,
		input:   input,
		viewport: viewport.New(
			viewport.WithWidth(panelContentWidth(defaultWidth)),
			viewport.WithHeight(minimumComponentSize),
		),
		spinner: spinnerModel,
		width:   defaultWidth,
		height:  defaultHeight,
		state:   stateReady,
	}
	m.viewport.FillHeight = true
	m.refreshHistory()
	m.resize()

	return m
}

func (m model) Init() tea.Cmd {
	return tea.Batch(textarea.Blink, m.spinner.Tick)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resize()
	case responseMsg:
		if msg.id != m.turnID || m.state != stateProcessing {
			return m, nil
		}
		m.cancelRequest = nil
		if msg.err != nil {
			m.appendHistory(message{author: "Watson", content: "Request failed: " + msg.err.Error()})
		} else {
			m.appendHistory(message{author: "Watson", content: msg.response})
		}
		m.state = stateReady
		return m, nil
	case spinner.TickMsg:
		if m.state == stateProcessing {
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
	status := statusStyle.Render("Status: " + string(m.state))
	if m.state == stateProcessing {
		status = statusStyle.Render(m.spinner.View() + " Status: " + string(m.state))
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
	switch msg.String() {
	case "ctrl+q":
		return m, tea.Quit
	case "esc", "ctrl+c":
		if m.state == stateProcessing {
			m.cancel()
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

func (m model) submit() (tea.Model, tea.Cmd) {
	question := strings.TrimSpace(m.input.Value())
	if question == "" {
		return m, nil
	}
	if m.state != stateReady {
		if question == "/clear" {
			m.input.Reset()
			m.appendHistory(message{author: "Watson", content: "Cannot clear history while a request is active. Cancel it first."})
		}
		return m, nil
	}

	if strings.HasPrefix(question, "/") {
		return m.runCommand(question)
	}

	m.history = append(m.history, message{author: "You", content: question})
	m.input.Reset()
	m.resize()
	m.state = stateProcessing
	m.turnID++
	m.refreshHistory()
	m.viewport.GotoBottom()
	turnID := m.turnID

	return m, m.request(turnID, question)
}

func (m model) runCommand(command string) (tea.Model, tea.Cmd) {
	m.input.Reset()
	m.resize()
	switch command {
	case "/help":
		m.history = append(m.history, message{author: "Watson", content: "Commands: /help, /clear, /quit\nKeys: Enter send; Ctrl+J newline; Esc cancel; Ctrl+Q quit."})
	case "/clear":
		m.history = []message{{content: logo}}
	case "/quit":
		return m, tea.Quit
	default:
		m.history = append(m.history, message{author: "Watson", content: "Unknown command: " + command})
	}
	m.refreshHistory()
	m.viewport.GotoBottom()

	return m, nil
}

func (m *model) cancel() {
	if m.cancelRequest != nil {
		m.cancelRequest()
		m.cancelRequest = nil
	}
	m.turnID++
	m.state = stateReady
	m.appendHistory(message{author: "Watson", content: "Request cancelled."})
}

func (m *model) appendHistory(entry message) {
	follow := m.viewport.AtBottom()
	m.history = append(m.history, entry)
	m.refreshHistory()
	if follow {
		m.viewport.GotoBottom()
	}
}

func (m *model) refreshHistory() {
	entries := make([]string, 0, len(m.history))
	for _, entry := range m.history {
		if entry.author == "" {
			entries = append(entries, headerStyle.Render(entry.content))
			continue
		}
		author := botStyle.Render(entry.author)
		if entry.author == "You" {
			author = userStyle.Render(entry.author)
		}
		entries = append(entries, author+"\n"+entry.content)
	}
	m.viewport.SetContent(lipgloss.Wrap(strings.Join(entries, "\n\n"), m.viewport.Width(), ""))
}

func (m *model) resize() {
	panelWidth := panelContentWidth(m.width)
	m.input.SetHeight(inputHeight(m.input.Value()))
	m.viewport.SetWidth(panelWidth)
	m.viewport.SetHeight(max(m.height-statusHeight()-panelStyle.GetVerticalFrameSize()*2-m.input.Height(), minimumComponentSize))
	m.input.SetWidth(panelWidth)
	m.refreshHistory()
}

func panelContentWidth(width int) int {
	return max(width-panelHorizontalMargin-panelStyle.GetHorizontalFrameSize(), minimumComponentSize)
}

func (m *model) request(turnID int, question string) tea.Cmd {
	if m.client == nil {
		return tea.Tick(responseDelay, func(time.Time) tea.Msg {
			return responseMsg{id: turnID, response: "Echo: " + question}
		})
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.cancelRequest = cancel
	return func() tea.Msg {
		response, err := m.client.Ask(ctx, question)
		return responseMsg{id: turnID, response: response, err: err}
	}
}

func statusHeight() int {
	return 1
}

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
