package tui

import (
	"bufio"
	"context"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// AgentFunc is the interface between the TUI and the agent.
// The TUI does not care how the agent works internally.
// reader is the approval/input source the harness should block on; the TUI
// feeds it from the approval channel when the user answers a prompt.
type AgentFunc func(
	ctx context.Context,
	provider string,
	modelName string,
	userMessage string,
	reader *bufio.Reader,
) (string, error)

type Message struct {
	Role    string
	Content string
}

// approvalOption is one clickable "[y] once" style hotspot.
type approvalOption struct {
	start, end int
	answer     string
}

type Model struct {
	provider  string
	modelName string
	agent     AgentFunc
	reader    *bufio.Reader

	input    string
	cursor   int // rune index into input
	messages []Message
	busy     bool

	approvalCh      chan<- string
	pendingApproval bool
	hovered         string // answer of the approval button under the cursor, "" if none

	// clicks is a pointer so View (value receiver) can record hotspots
	// that the next Update can read.
	clicks *clickTargets

	spinnerFrame int
	height       int
	width        int
	scroll       int
}

type clickTargets struct {
	row       int
	opts      []approvalOption
	visible   bool
	maxScroll int
}

type agentFinishedMsg struct {
	response string
	err      error
}

// logLineMsg carries text that used to be written to stdout via log.Println.
type logLineMsg struct {
	text string
}

// approvalRequestedMsg is sent when the harness prints its "Allow?" prompt.
type approvalRequestedMsg struct{}

type tickMsg struct{}

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Italic(true).
			Foreground(lipgloss.Color("#D3D3D3")).
			Border(lipgloss.HiddenBorder()).
			BorderForeground(lipgloss.Color("99"))

	userStyle = lipgloss.NewStyle().
			Bold(true)

	agentStyle = lipgloss.NewStyle()

	errorStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("196"))

	statusStyle = lipgloss.NewStyle().
			Italic(true).
			Foreground(lipgloss.Color("99"))

	eventStyle = lipgloss.NewStyle().
			Italic(true).
			Faint(true)

	approvalStyle = lipgloss.NewStyle().
			Bold(true)

	optionStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("231")).
			Background(lipgloss.Color("99")).
			Padding(0, 1)

	optionHoverStyle = optionStyle.
				Background(lipgloss.Color("141"))
)

func New(
	provider string,
	modelName string,
	agent AgentFunc,
	reader *bufio.Reader,
	approvalCh chan<- string,
) Model {
	return Model{
		provider:   provider,
		modelName:  modelName,
		agent:      agent,
		reader:     reader,
		approvalCh: approvalCh,
		messages:   make([]Message, 0),
		clicks:     &clickTargets{},
	}
}

func (m Model) Init() tea.Cmd {
	return nil
}

func tick() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg {
		return tickMsg{}
	})
}

// optionAt returns the answer of the approval button at cell (x, y), or "" if none.
func optionAt(c *clickTargets, x, y int) string {
	if !c.visible || y != c.row {
		return ""
	}

	for _, opt := range c.opts {
		if x >= opt.start && x < opt.end {
			return opt.answer
		}
	}

	return ""
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.KeyMsg:
		switch msg.String() {

		case "ctrl+c":
			return m, tea.Quit

		case "alt+c":
			if text := lastAgentMessage(m.messages); text != "" {
				_ = clipboard.WriteAll(text)
			}

		case "left":
			if m.cursor > 0 {
				m.cursor--
			}

		case "right":
			if m.cursor < len([]rune(m.input)) {
				m.cursor++
			}

		case "enter":
			// While an approval prompt is pending, whatever the user
			// typed is an answer to that prompt, not a chat message.
			if m.busy && m.pendingApproval {
				answer := strings.TrimSpace(m.input)
				m.input = ""
				m.cursor = 0
				m.pendingApproval = false
				m.approvalCh <- answer
				return m, nil
			}

			if m.busy {
				return m, nil
			}

			userMessage := strings.TrimSpace(m.input)

			if userMessage == "" {
				return m, nil
			}

			m.messages = append(m.messages, Message{
				Role:    "user",
				Content: userMessage,
			})

			m.input = ""
			m.cursor = 0
			m.busy = true
			m.spinnerFrame = 0

			return m, tea.Batch(
				runAgent(
					m.agent,
					m.provider,
					m.modelName,
					userMessage,
					m.reader,
				),
				tick(),
			)

		case "backspace", "ctrl+h":
			if m.cursor == 0 {
				return m, nil
			}

			runes := []rune(m.input)
			m.input = string(runes[:m.cursor-1]) + string(runes[m.cursor:])
			m.cursor--

		case "delete":
			runes := []rune(m.input)

			if m.cursor >= len(runes) {
				return m, nil
			}

			m.input = string(runes[:m.cursor]) + string(runes[m.cursor+1:])

		case "ctrl+w":
			runes := []rune(m.input)
			before := deleteLastWord(string(runes[:m.cursor]))

			m.input = before + string(runes[m.cursor:])
			m.cursor = len([]rune(before))

		case "ctrl+u":
			m.input = ""
			m.cursor = 0

		default:
			if len(msg.Runes) > 0 {
				runes := []rune(m.input)
				m.input = string(runes[:m.cursor]) + string(msg.Runes) + string(runes[m.cursor:])
				m.cursor += len(msg.Runes)
			}
		}

	case tea.WindowSizeMsg:
		m.height = msg.Height
		m.width = msg.Width

	case tea.MouseMsg:
		switch {
		case msg.Button == tea.MouseButtonWheelUp:
			m.scroll += 3
			if m.scroll > m.clicks.maxScroll {
				m.scroll = m.clicks.maxScroll
			}

		case msg.Button == tea.MouseButtonWheelDown:
			m.scroll -= 3
			if m.scroll < 0 {
				m.scroll = 0
			}

		case msg.Action == tea.MouseActionMotion:
			m.hovered = ""

			if m.pendingApproval {
				m.hovered = optionAt(m.clicks, msg.X, msg.Y)
			}

		case msg.Action == tea.MouseActionPress &&
			msg.Button == tea.MouseButtonLeft &&
			m.pendingApproval:
			if answer := optionAt(m.clicks, msg.X, msg.Y); answer != "" {
				m.input = ""
				m.cursor = 0
				m.pendingApproval = false
				m.hovered = ""
				m.approvalCh <- answer
				return m, nil
			}
		}

	case agentFinishedMsg:
		m.busy = false
		m.pendingApproval = false
		m.hovered = ""

		if msg.err != nil {
			m.messages = append(m.messages, Message{
				Role:    "error",
				Content: msg.err.Error(),
			})

			return m, nil
		}

		if strings.TrimSpace(msg.response) != "" {
			m.messages = append(m.messages, Message{
				Role:    "agent",
				Content: msg.response,
			})
		}

	case logLineMsg:
		m.messages = append(m.messages, Message{
			Role:    "event",
			Content: msg.text,
		})

	case approvalRequestedMsg:
		m.pendingApproval = true
		m.hovered = ""

	case tickMsg:
		if m.busy {
			m.spinnerFrame = (m.spinnerFrame + 1) % len(spinnerFrames)
			return m, tick()
		}
	}

	return m, nil
}

func deleteLastWord(s string) string {
	s = strings.TrimRight(s, " ")

	if i := strings.LastIndex(s, " "); i >= 0 {
		return s[:i+1]
	}

	return ""
}

func lastAgentMessage(messages []Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "agent" {
			return messages[i].Content
		}
	}

	return ""
}

// centeredTitle renders the title centered in the terminal width so it
// always stays in the middle at any resolution.
func centeredTitle(width int) string {
	const text = "You are now running Optine."
	if width <= 0 {
		return titleStyle.Render(text)
	}
	if width < 4 {
		if r := []rune(text); len(r) > width {
			return string(r[:width])
		}
		return text
	}
	// The hidden border still occupies a cell on each side, so the
	// content width is the terminal width minus 2.
	inner := width - 2
	t := text
	if r := []rune(text); len(r) > inner {
		t = string(r[:inner])
	}
	return titleStyle.Width(inner).Align(lipgloss.Center).Render(t)
}

func (m Model) View() string {
	var b strings.Builder

	b.WriteString("\n")
	b.WriteString(centeredTitle(m.width))
	b.WriteString("\n\n")

	optionsRow := -1

	for _, message := range m.messages {
		switch message.Role {

		case "user":
			b.WriteString(
				userStyle.Render("> " + message.Content),
			)

		case "agent":
			b.WriteString(
				agentStyle.Render(message.Content),
			)

		case "error":
			b.WriteString(
				errorStyle.Render("error: " + message.Content),
			)

		case "event":
			b.WriteString(
				eventStyle.Render(message.Content),
			)
		}

		b.WriteString("\n\n")
	}

	if m.busy {
		b.WriteString(
			statusStyle.Render(spinnerFrames[m.spinnerFrame] + " thinking..."),
		)
		b.WriteString("\n\n")
	}

	if m.pendingApproval {
		b.WriteString(
			approvalStyle.Render("approval required — type y/a/n, or click:"),
		)
		b.WriteString("\n")

		optionsRow = strings.Count(b.String(), "\n")
		m.clicks.opts = nil

		options := []struct {
			label  string
			answer string
		}{
			{"[y] once", "y"},
			{"[a] always", "a"},
			{"[N] no", "n"},
		}

		col := 0
		for i, opt := range options {
			if i > 0 {
				b.WriteString("  ")
				col += 2
			}

			style := optionStyle
			if opt.answer == m.hovered {
				style = optionHoverStyle
			}

			// the style adds 1 space of padding on each side.
			b.WriteString(style.Render(opt.label))
			m.clicks.opts = append(m.clicks.opts, approvalOption{
				start:  col,
				end:    col + len(opt.label) + 2,
				answer: opt.answer,
			})
			col += len(opt.label) + 2
		}

		b.WriteString("\n\n")
	}

	// Scroll window: keep the newest (height-1) lines of history visible,
	// shifted up by the user's wheel scroll offset; the prompt is pinned
	// at the bottom.
	lines := strings.Split(b.String(), "\n")

	contentHeight := m.height - 1
	if contentHeight < 1 {
		contentHeight = 1
	}

	maxScroll := len(lines) - contentHeight
	if maxScroll < 0 {
		maxScroll = 0
	}
	m.clicks.maxScroll = maxScroll

	if m.scroll > maxScroll {
		m.scroll = maxScroll
	}

	end := len(lines) - m.scroll
	start := end - contentHeight
	if start < 0 {
		start = 0
	}

	visible := lines[start:end]

	m.clicks.visible = false
	if optionsRow >= start && optionsRow < end {
		m.clicks.row = optionsRow - start
		m.clicks.visible = true
	}

	runes := []rune(m.input)
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor > len(runes) {
		m.cursor = len(runes)
	}

	var out strings.Builder
	out.WriteString(strings.Join(visible, "\n"))

	// Lock the prompt to the bottom-left: pad with blank rows so it lands
	// on the last screen row at any resolution.
	if m.height > 1 {
		if pad := m.height - 1 - len(visible); pad > 0 {
			out.WriteString(strings.Repeat("\n", pad))
		}
	}

	// Keep the prompt on a single row: window the input around the cursor
	// so "> " always starts at column 0.
	body := string(runes[:m.cursor]) + "█" + string(runes[m.cursor:])
	if m.width > 2 {
		maxBody := m.width - 2 // room for "> "
		bodyRunes := []rune(body)
		if len(bodyRunes) > maxBody {
			end := m.cursor + 1
			if end > len(bodyRunes) {
				end = len(bodyRunes)
			}
			start := end - maxBody
			if start < 0 {
				start = 0
			}
			body = string(bodyRunes[start:end])
		}
	}
	prompt := "> " + body
	if m.width > 0 {
		if pr := []rune(prompt); len(pr) > m.width {
			prompt = string(pr[:m.width])
		}
	}
	out.WriteString("\n" + prompt)

	return out.String()
}

func runAgent(
	agent AgentFunc,
	provider string,
	modelName string,
	userMessage string,
	reader *bufio.Reader,
) tea.Cmd {
	return func() tea.Msg {
		response, err := agent(
			context.Background(),
			provider,
			modelName,
			userMessage,
			reader,
		)

		return agentFinishedMsg{
			response: response,
			err:      err,
		}
	}
}
