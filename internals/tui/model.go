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

// Roles a Message can have.
const (
	roleUser  = "user"
	roleAgent = "agent"
	roleError = "error"
	roleEvent = "event"
)

type Message struct {
	Role    string
	Content string
}

// approvalOption is one clickable "[y] once" style hotspot.
type approvalOption struct {
	start, end int // column range [start, end) on the options row
	answer     string
}

// approvalChoices are the buttons shown while an approval is pending.
var approvalChoices = []struct{ label, answer string }{
	{"[y] once", "y"},
	{"[a] always", "a"},
	{"[N] no", "n"},
}

const (
	wheelStep   = 3    // lines scrolled per mouse-wheel notch
	buttonGap   = "  " // spacing between approval buttons
	cursorGlyph = "█"
)

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
	scroll       int // lines scrolled up from the bottom of the history
}

// clickTargets is written by View and read by Update.
type clickTargets struct {
	row       int // screen row of the approval buttons
	opts      []approvalOption
	visible   bool // whether the buttons are on screen right now
	maxScroll int  // largest valid Model.scroll for the current content
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

// Styles used by the chat view in this file.
var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Italic(true).
			Foreground(lipgloss.Color("#D3D3D3")).
			Border(lipgloss.HiddenBorder())

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

// Markdown rendering (wrapping, inline spans, agent layout) lives in
// markdown.go, which uses these styles.
var (
	headingStyle = lipgloss.NewStyle().
			Bold(true)

	agentTextBase = lipgloss.NewStyle()

	quoteTextBase = lipgloss.NewStyle().
			Italic(true).
			Faint(true).
			Foreground(lipgloss.Color("99"))

	codeBlockStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#D3D3D3")).
			Background(lipgloss.Color("235")).
			Padding(0, 1)

	codeBlockTitleStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("99")).
				Background(lipgloss.Color("235")).
				Padding(0, 1)
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

// sendApproval hands the user's answer to the harness from a command, so
// Update never blocks on the channel (and can't freeze the UI if the harness
// isn't reading at that moment).
func sendApproval(ch chan<- string, answer string) tea.Cmd {
	return func() tea.Msg {
		ch <- answer
		return nil
	}
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

// ---------------------------------------------------------------------------
// Update
// ---------------------------------------------------------------------------

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.KeyMsg:
		return m.updateKey(msg)

	case tea.MouseMsg:
		return m.updateMouse(msg)

	case tea.WindowSizeMsg:
		m.height = msg.Height
		m.width = msg.Width

	case agentFinishedMsg:
		m.busy = false
		m.pendingApproval = false
		m.hovered = ""

		if msg.err != nil {
			m.messages = append(m.messages, Message{
				Role:    roleError,
				Content: msg.err.Error(),
			})
		} else if strings.TrimSpace(msg.response) != "" {
			m.messages = append(m.messages, Message{
				Role:    roleAgent,
				Content: msg.response,
			})
		}

	case logLineMsg:
		m.messages = append(m.messages, Message{
			Role:    roleEvent,
			Content: msg.text,
		})

	case approvalRequestedMsg:
		m.pendingApproval = true
		m.hovered = ""
		m.scroll = 0 // make sure the prompt is on screen

	case tickMsg:
		if m.busy {
			m.spinnerFrame = (m.spinnerFrame + 1) % len(spinnerFrames)
			return m, tick()
		}
	}

	return m, nil
}

func (m Model) updateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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
		return m.submit()

	case "backspace", "ctrl+h":
		if m.cursor > 0 {
			runes := []rune(m.input)
			m.input = string(runes[:m.cursor-1]) + string(runes[m.cursor:])
			m.cursor--
		}

	case "delete":
		if runes := []rune(m.input); m.cursor < len(runes) {
			m.input = string(runes[:m.cursor]) + string(runes[m.cursor+1:])
		}

	case "ctrl+w":
		runes := []rune(m.input)
		before := deleteLastWord(string(runes[:m.cursor]))

		m.input = before + string(runes[m.cursor:])
		m.cursor = len([]rune(before))

	case "ctrl+u":
		m.input = ""
		m.cursor = 0

	// Keyboard scroll: mouse reporting (and with it the wheel) is off
	// outside approval prompts, so history needs keys.
	case "pgup":
		m.scroll = m.scrolledBy(m.pageSize())

	case "pgdown":
		m.scroll = m.scrolledBy(-m.pageSize())

	default:
		// Alt+<key> is a shortcut, not text.
		if len(msg.Runes) > 0 && !msg.Alt {
			runes := []rune(m.input)
			typed := promptSafe(msg.Runes)

			m.input = string(runes[:m.cursor]) + string(typed) + string(runes[m.cursor:])
			m.cursor += len(typed)
		}
	}

	return m, nil
}

// submit handles Enter: it answers a pending approval, or sends the typed
// text to the agent as a new chat message.
func (m Model) submit() (tea.Model, tea.Cmd) {
	// While an approval prompt is pending, whatever the user typed is an
	// answer to that prompt, not a chat message.
	if m.busy && m.pendingApproval {
		return m.answerApproval(strings.TrimSpace(m.input))
	}

	if m.busy {
		return m, nil
	}

	userMessage := strings.TrimSpace(m.input)
	if userMessage == "" {
		return m, nil
	}

	m.messages = append(m.messages, Message{
		Role:    roleUser,
		Content: userMessage,
	})

	m.input = ""
	m.cursor = 0
	m.busy = true
	m.spinnerFrame = 0
	m.scroll = 0 // jump to the bottom so the new message is visible

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
}

// answerApproval clears the prompt and forwards the answer to the harness.
func (m Model) answerApproval(answer string) (tea.Model, tea.Cmd) {
	m.input = ""
	m.cursor = 0
	m.pendingApproval = false
	m.hovered = ""

	return m, sendApproval(m.approvalCh, answer)
}

func (m Model) updateMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	switch {
	case msg.Button == tea.MouseButtonWheelUp:
		m.scroll = m.scrolledBy(wheelStep)

	case msg.Button == tea.MouseButtonWheelDown:
		m.scroll = m.scrolledBy(-wheelStep)

	case msg.Action == tea.MouseActionMotion:
		m.hovered = ""

		if m.pendingApproval {
			m.hovered = optionAt(m.clicks, msg.X, msg.Y)
		}

	case msg.Action == tea.MouseActionPress &&
		msg.Button == tea.MouseButtonLeft &&
		m.pendingApproval:
		if answer := optionAt(m.clicks, msg.X, msg.Y); answer != "" {
			return m.answerApproval(answer)
		}
	}

	return m, nil
}

// scrolledBy returns the scroll offset after moving delta lines up (positive)
// or down (negative), kept within what the current content allows.
func (m Model) scrolledBy(delta int) int {
	limit := m.clicks.maxScroll
	return clampInt(clampInt(m.scroll, 0, limit)+delta, 0, limit)
}

// pageSize is the number of lines PgUp/PgDn move: half a screen, at least one.
func (m Model) pageSize() int {
	if page := m.height / 2; page > 1 {
		return page
	}

	return 1
}

// ---------------------------------------------------------------------------
// Text helpers
// ---------------------------------------------------------------------------

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}

	return v
}

// cutRunes truncates s to at most n runes.
func cutRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}

	return s
}

// promptSafe makes typed or pasted text fit the single-row prompt by turning
// line breaks and tabs into spaces.
func promptSafe(runes []rune) []rune {
	out := make([]rune, len(runes))

	for i, r := range runes {
		if r == '\n' || r == '\r' || r == '\t' {
			r = ' '
		}
		out[i] = r
	}

	return out
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
		if messages[i].Role == roleAgent {
			return messages[i].Content
		}
	}

	return ""
}

// ---------------------------------------------------------------------------
// View
// ---------------------------------------------------------------------------

// centeredTitle renders the title centered in the terminal width so it
// always stays in the middle at any resolution.
func centeredTitle(width int) string {
	const text = "You are now running Optine."

	if width <= 0 {
		return titleStyle.Render(text)
	}

	if width < 4 {
		return cutRunes(text, width)
	}

	// The hidden border still occupies a cell on each side, so the
	// content width is the terminal width minus 2.
	inner := width - 2

	return titleStyle.Width(inner).Align(lipgloss.Center).Render(cutRunes(text, inner))
}

func (m Model) View() string {
	var b strings.Builder

	b.WriteString("\n")
	b.WriteString(centeredTitle(m.width))
	b.WriteString("\n\n")

	for _, message := range m.messages {
		b.WriteString(m.renderMessage(message))
		b.WriteString("\n\n")
	}

	if m.busy {
		b.WriteString(
			statusStyle.Render(spinnerFrames[m.spinnerFrame] + " thinking..."),
		)
		b.WriteString("\n\n")
	}

	optionsRow := -1
	if m.pendingApproval {
		optionsRow = m.writeApproval(&b)
	}

	visible := m.visibleLines(b.String(), optionsRow)

	var out strings.Builder
	out.WriteString(strings.Join(visible, "\n"))

	// Lock the prompt to the bottom-left: pad with blank rows so it lands
	// on the last screen row at any resolution.
	if pad := m.height - 1 - len(visible); pad > 0 {
		out.WriteString(strings.Repeat("\n", pad))
	}

	out.WriteString("\n" + m.renderPrompt())

	return out.String()
}

func (m Model) renderMessage(message Message) string {
	switch message.Role {

	case roleUser:
		return renderPlainWrapped(message.Content, m.width, userStyle, "> ", "  ")

	case roleAgent:
		return renderAgentContent(message.Content, m.width)

	case roleError:
		return renderPlainWrapped("error: "+message.Content, m.width, errorStyle, "", "")

	case roleEvent:
		if info, ok := parseEditInfo(message.Content); ok {
			return renderEditInfo(info, m.width)
		}
		if info, ok := parseWriteInfo(message.Content); ok {
			return renderWriteInfo(info, m.width)
		}
		return renderPlainWrapped(message.Content, m.width, eventStyle, "", "")
	}

	return ""
}

// writeApproval appends the approval prompt and its clickable buttons to b.
// It records each button's column range for mouse hit-testing and returns the
// row (within b's output) that the buttons are on.
func (m Model) writeApproval(b *strings.Builder) int {
	b.WriteString(
		approvalStyle.Render("approval required — type y/a/n, or click:"),
	)
	b.WriteString("\n")

	row := strings.Count(b.String(), "\n")
	m.clicks.opts = nil

	col := 0
	for i, choice := range approvalChoices {
		if i > 0 {
			b.WriteString(buttonGap)
			col += len(buttonGap)
		}

		style := optionStyle
		if choice.answer == m.hovered {
			style = optionHoverStyle
		}

		button := style.Render(choice.label)
		width := lipgloss.Width(button)

		b.WriteString(button)
		m.clicks.opts = append(m.clicks.opts, approvalOption{
			start:  col,
			end:    col + width,
			answer: choice.answer,
		})
		col += width
	}

	b.WriteString("\n\n")

	return row
}

// visibleLines picks the part of the history to show: the newest (height-1)
// lines, shifted up by the scroll offset; the prompt is pinned below them.
// It also records the scroll limit and whether the approval buttons (on
// optionsRow, or -1) are on screen, for the next Update.
func (m Model) visibleLines(history string, optionsRow int) []string {
	lines := strings.Split(history, "\n")

	contentHeight := m.height - 1
	if contentHeight < 1 {
		contentHeight = 1
	}

	maxScroll := len(lines) - contentHeight
	if maxScroll < 0 {
		maxScroll = 0
	}
	m.clicks.maxScroll = maxScroll

	end := len(lines) - clampInt(m.scroll, 0, maxScroll)
	start := end - contentHeight
	if start < 0 {
		start = 0
	}

	m.clicks.visible = optionsRow >= start && optionsRow < end
	if m.clicks.visible {
		m.clicks.row = optionsRow - start
	}

	return lines[start:end]
}

// renderPrompt draws the single-row "> input" prompt. The input is windowed
// around the cursor so "> " always starts at column 0.
func (m Model) renderPrompt() string {
	runes := []rune(m.input)
	cursor := clampInt(m.cursor, 0, len(runes))

	body := []rune(string(runes[:cursor]) + cursorGlyph + string(runes[cursor:]))

	if maxBody := m.width - 2; m.width > 2 && len(body) > maxBody { // room for "> "
		// Show a maxBody-wide window that ends at the cursor, or starts at
		// the beginning of the input if the cursor is near it.
		start := cursor + 1 - maxBody
		if start < 0 {
			start = 0
		}

		end := start + maxBody
		if end > len(body) {
			end = len(body)
		}

		body = body[start:end]
	}

	prompt := "> " + string(body)
	if m.width > 0 {
		prompt = cutRunes(prompt, m.width)
	}

	return prompt
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
