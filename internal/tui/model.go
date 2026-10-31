package tui

import (
	"bufio"
	"context"
	"fmt"
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
) (AgentResult, error)

// AgentResult is one agent turn. Non-nil Messages replaces the visible
// conversation; /resume and /restore use it to swap sessions atomically.
type AgentResult struct {
	Response string
	Messages []Message
}

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
	ctx       context.Context
	provider  string
	modelName string
	workdir   string
	agent     AgentFunc
	reader    *bufio.Reader

	sessionUID string

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

	// cache is a pointer for the same reason: View writes the rendered
	// static history back into the model it was called on.
	cache *renderCache

	spinnerFrame int
	height       int
	width        int
	scroll       int // lines scrolled up from the bottom of the history

	modelPicker          bool
	modelChoices         []string
	allModelChoices      []string
	allModelChoicesLower []string
	modelSearch          string
	modelQuery           string
	searchQuery          string
	modelFilter          string
	modelCursor          int
	modelScroll          int
	modelResult          chan string
}

// clickTargets is written by View and read by Update.
type clickTargets struct {
	row       int // screen row of the approval buttons
	opts      []approvalOption
	visible   bool // whether the buttons are on screen right now
	maxScroll int  // largest valid Model.scroll for the current content

	modelVisible  bool // whether the model picker list is on screen
	modelFirstRow int  // screen row of the first model row
	modelCount    int  // number of visible model rows
	modelOffset   int  // index of the first visible model
	modelChoices  []string
	choices       []string
	models        []string

	modelBoxLeft  int // screen column of the picker box's left edge
	modelBoxWidth int // rendered width of the picker box
}

// renderCache holds the width-dependent render of the header and messages.
// Spinner and approval lines are appended per frame; they change too often to
// cache and are only a few lines.
type renderCache struct {
	width        int
	messageCount int
	dirty        bool
	lines        []string
}

func (c *renderCache) invalidate() {
	if c != nil {
		c.dirty = true
	}
}

type agentFinishedMsg struct {
	response string
	swap     []Message
	err      error
}

// logLineMsg carries text that used to be written to stdout via log.Println.
type logLineMsg struct {
	text string
}

// approvalRequestedMsg is sent when the harness asks for approval, routed
// from harnessPermissions.AskFunc through internal/approval.
type approvalRequestedMsg struct{}

// sessionChangedMsg carries the UID of the active session, set on the first
// run and whenever /resume swaps sessions.
type sessionChangedMsg struct {
	uid string
}

// modelChangedMsg carries the provider and model selected via /model so the
// status line can be updated mid-session.
type modelChangedMsg struct {
	provider string
	model    string
}

// NotifyModelChanged pushes a provider/model update into the active TUI so its
// status line stays in sync with a /model selection.
func NotifyModelChanged(provider, model string) {
	if activeProgram != nil {
		activeProgram.Send(modelChangedMsg{provider: provider, model: model})
	}
}

// modelPickerOpenMsg opens the interactive model picker in the TUI.
type modelPickerOpenMsg struct {
	models []string
	result chan string
}

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
	ctx context.Context,
	provider string,
	modelName string,
	workdir string,
	resume []Message,
	agent AgentFunc,
	reader *bufio.Reader,
	approvalCh chan<- string,
) Model {
	m := Model{
		ctx:        ctx,
		provider:   provider,
		modelName:  modelName,
		workdir:    workdir,
		agent:      agent,
		reader:     reader,
		approvalCh: approvalCh,
		clicks:     &clickTargets{},
		cache:      &renderCache{dirty: true},
	}

	if resume != nil {
		m.messages = append([]Message(nil), resume...)
	}

	return m
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

		if msg.swap != nil {
			// /resume and /restore replace the old conversation wholesale.
			m.messages = msg.swap
			m.scroll = 0
		} else if msg.err != nil {
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

		m.cache.invalidate()

	case logLineMsg:
		m.messages = append(m.messages, Message{
			Role:    roleEvent,
			Content: msg.text,
		})
		m.cache.invalidate()

	case approvalRequestedMsg:
		m.pendingApproval = true
		m.hovered = ""
		m.scroll = 0 // make sure the prompt is on screen

	case sessionChangedMsg:
		m.sessionUID = msg.uid
		m.cache.invalidate()

	case modelChangedMsg:
		m.provider = msg.provider
		m.modelName = msg.model
		m.cache.invalidate()

	case modelPickerOpenMsg:
		m.modelPicker = true
		m.modelChoices = msg.models
		m.allModelChoices = append([]string(nil), msg.models...)
		m.allModelChoicesLower = make([]string, len(m.allModelChoices))
		for i, v := range m.allModelChoices {
			m.allModelChoicesLower[i] = strings.ToLower(v)
		}
		m.modelSearch = ""
		m.modelQuery = ""
		m.searchQuery = ""
		m.modelFilter = ""
		m.input = ""
		m.cursor = 0
		m.modelCursor = 0
		m.modelScroll = 0
		m.modelResult = msg.result
		activePickerResult = msg.result
		if m.clicks != nil {
			m.clicks.modelVisible = false
			m.clicks.modelCount = 0
			m.clicks.modelOffset = 0
			m.clicks.modelBoxLeft = 0
			m.clicks.modelBoxWidth = 0
		}
		m.cache.invalidate()

	case tickMsg:
		if m.busy {
			m.spinnerFrame = (m.spinnerFrame + 1) % len(spinnerFrames)
			return m, tick()
		}
	}

	return m, nil
}

func (m Model) updateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.modelPicker {
		switch msg.String() {
		case "up", "k":
			if m.modelCursor > 0 {
				m.modelCursor--
			}
			m.ensureModelCursorVisible()

		case "down", "j":
			if m.modelCursor < len(m.modelChoices)-1 {
				m.modelCursor++
			}
			m.ensureModelCursorVisible()

		case "pgup":
			page := m.pageSize()
			m.modelCursor = clampInt(m.modelCursor-page, 0, len(m.modelChoices)-1)
			m.modelScroll = clampInt(m.modelScroll-page, 0, m.modelMaxScroll())
			m.ensureModelCursorVisible()

		case "pgdown":
			page := m.pageSize()
			m.modelCursor = clampInt(m.modelCursor+page, 0, len(m.modelChoices)-1)
			m.modelScroll = clampInt(m.modelScroll+page, 0, m.modelMaxScroll())
			m.ensureModelCursorVisible()

		case "enter":
			return m.submit()

		case "esc":
			if m.currentModelQuery() != "" {
				m.clearModelQuery()
				m.applyModelSearch()
				return m, nil
			}
			m = m.cancelModelPicker()
			return m, nil

		case "backspace", "ctrl+h":
			if m.cursor > 0 {
				runes := []rune(m.input)
				m.input = string(runes[:m.cursor-1]) + string(runes[m.cursor:])
				m.cursor--
			}
			m.syncModelQuery(m.input)
			m.applyModelSearch()

		case "ctrl+u":
			m.input = ""
			m.cursor = 0
			m.syncModelQuery("")
			m.applyModelSearch()

		case "ctrl+w":
			runes := []rune(m.input)
			before := deleteLastWord(string(runes[:m.cursor]))
			m.input = before + string(runes[m.cursor:])
			m.cursor = len([]rune(before))
			m.syncModelQuery(m.input)
			m.applyModelSearch()

		default:
			if len(msg.Runes) > 0 && !msg.Alt {
				runes := []rune(m.input)
				typed := promptSafe(msg.Runes)
				m.input = string(runes[:m.cursor]) + string(typed) + string(runes[m.cursor:])
				m.cursor += len(typed)
				m.syncModelQuery(m.input)
				m.applyModelSearch()
			}
		}

		return m, nil
	}

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
	if m.modelPicker {
		m = m.selectModel(m.modelCursor)
		return m, nil
	}

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

	if !strings.HasPrefix(userMessage, "/") {
		m.messages = append(m.messages, Message{
			Role:    roleUser,
			Content: userMessage,
		})
		m.cache.invalidate()
	}

	m.input = ""
	m.cursor = 0
	m.busy = true
	m.spinnerFrame = 0
	m.scroll = 0 // jump to the bottom so the new message is visible

	return m, tea.Batch(
		runAgent(
			m.ctx,
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
	if m.modelPicker {
		switch {
		case msg.Button == tea.MouseButtonWheelUp:
			m.modelScroll = clampInt(m.modelScroll-wheelStep, 0, m.modelMaxScroll())

		case msg.Button == tea.MouseButtonWheelDown:
			m.modelScroll = clampInt(m.modelScroll+wheelStep, 0, m.modelMaxScroll())

		case msg.Action == tea.MouseActionMotion:
			if idx := modelIndexAt(m.clicks, msg.X, msg.Y); idx >= 0 {
				m.modelCursor = idx
			}

		case msg.Action == tea.MouseActionPress &&
			msg.Button == tea.MouseButtonLeft:
			if idx := modelIndexAt(m.clicks, msg.X, msg.Y); idx >= 0 {
				m.modelCursor = idx
				m = m.selectModel(idx)
			} else {
				m = m.cancelModelPicker()
			}
		}

		return m, nil
	}

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
// Conversation helpers
// ---------------------------------------------------------------------------

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

// centeredMeta renders the provider/model/session line and the working
// directory line under the title. The session UID appears after the first run
// and follows /resume.
func centeredMeta(width int, providerName, modelName, sessionUID, workdir string) string {
	lines := make([]string, 0, 2)

	parts := make([]string, 0, 3)

	if providerName != "" {
		parts = append(parts, providerName)
	}

	if modelName != "" {
		parts = append(parts, modelName)
	}

	if sessionUID != "" {
		parts = append(parts, "sess "+sessionUID)
	}

	if len(parts) > 0 {
		lines = append(lines, strings.Join(parts, " · "))
	}

	if workdir != "" {
		lines = append(lines, "Directory: "+workdir)
	}

	if len(lines) == 0 {
		return ""
	}

	rendered := make([]string, 0, len(lines))

	for _, line := range lines {
		rendered = append(rendered, centeredStatus(width, line))
	}

	return strings.Join(rendered, "\n")
}

// centeredStatus centers one faint status line within the terminal width.
func centeredStatus(width int, text string) string {
	if width <= 0 {
		return statusStyle.Render(text)
	}

	if width < 4 {
		return statusStyle.Render(cutRunes(text, width))
	}

	inner := width - 2

	return statusStyle.Width(inner).Align(lipgloss.Center).Render(cutRunes(text, inner))
}

// modelPickerView renders the interactive model picker.
func (m Model) modelPickerView() string {
	c := m.clicks
	if c == nil {
		c = &clickTargets{}
		m.clicks = c
	}

	choices := m.modelChoices
	visibleCount := m.modelVisibleCount()
	maxScroll := len(choices) - visibleCount
	if maxScroll < 0 {
		maxScroll = 0
	}
	scroll := clampInt(m.modelScroll, 0, maxScroll)

	selectedStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("231")).
		Background(lipgloss.Color("99")).
		Padding(0, 1)
	unselectedStyle := lipgloss.NewStyle().
		Padding(0, 1)

	header := "Select a model (↑/↓, Enter, Esc, click):"

	start := scroll
	end := scroll + visibleCount
	if end > len(choices) {
		end = len(choices)
	}
	var modelLines []string
	for i := start; i < end; i++ {
		line := choices[i]
		if i == m.modelCursor {
			line = selectedStyle.Render("> " + line)
		} else {
			line = unselectedStyle.Render("  " + line)
		}
		modelLines = append(modelLines, line)
	}
	for len(modelLines) < 10 {
		modelLines = append(modelLines, "")
	}

	q := m.currentModelQuery()
	content := q
	if q == "" {
		content = "search a model by typing on your keyboard"
	}
	if len(choices) > visibleCount && len(choices) > 0 {
		content += "   " + fmt.Sprintf("%d/%d", m.modelCursor+1, len(choices))
	}

	innerWidth := lipgloss.Width(header)
	source := m.allModelChoices
	if len(source) == 0 {
		source = m.modelChoices
	}
	for _, name := range source {
		line := unselectedStyle.Render("  " + name)
		if w := lipgloss.Width(line); w > innerWidth {
			innerWidth = w
		}
	}
	// Ensure the fixed search box can fit the placeholder and the worst-case
	// position text without wrapping, independent of the active filter.
	placeholder := "search a model by typing on your keyboard"
	minContent := placeholder
	if len(source) > visibleCount {
		minContent += "   " + fmt.Sprintf("%d/%d", len(source), len(source))
	}
	if required := lipgloss.Width(minContent) + 4; required > innerWidth {
		innerWidth = required
	}
	searchWidth := innerWidth - 4
	if searchWidth < 1 {
		searchWidth = 1
	}
	searchBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("255")).
		Padding(0, 1).
		Width(searchWidth).
		Render(content)

	var inner []string
	inner = append(inner, header)
	inner = append(inner, modelLines...)
	for _, line := range strings.Split(searchBox, "\n") {
		inner = append(inner, line)
	}

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("99")).
		Padding(1, 2, 0, 2).
		Render(strings.Join(inner, "\n"))

	boxHeight := len(strings.Split(box, "\n"))
	top := (m.height - boxHeight) / 2
	if top < 0 {
		top = 0
	}
	left := (m.width - lipgloss.Width(box)) / 2
	if left < 0 {
		left = 0
	}

	c.modelVisible = true
	c.modelFirstRow = top + 3
	c.modelCount = visibleCount
	c.modelOffset = scroll
	c.modelChoices = append([]string{}, choices...)
	c.choices = append([]string{}, choices...)
	c.models = append([]string{}, choices...)
	c.modelBoxLeft = left
	c.modelBoxWidth = lipgloss.Width(box)
	c.maxScroll = maxScroll

	lines := strings.Split(box, "\n")
	var out []string
	for i := 0; i < top; i++ {
		out = append(out, "")
	}
	for _, line := range lines {
		out = append(out, strings.Repeat(" ", left)+line)
	}
	for len(out) < m.height {
		out = append(out, "")
	}
	return strings.Join(out, "\n")
}

func (m Model) View() string {
	if m.modelPicker {
		return m.modelPickerView()
	}

	lines, optionsRow := m.historyLines()
	visible := m.visibleLines(lines, optionsRow)

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

// historyLines returns the complete history, static and dynamic. The header
// and messages are cached per width; only spinner and approval rows are
// rebuilt on every frame, so typing and spinner ticks no longer re-render the
// whole conversation.
func (m Model) historyLines() ([]string, int) {
	cache := m.cache
	if cache == nil {
		cache = &renderCache{dirty: true}
	}

	if cache.dirty || cache.width != m.width || cache.messageCount != len(m.messages) {
		cache.rebuild(m)
	}

	lines := cache.lines

	// The static history ends with "\n\n". Its final split element is the
	// empty line that the first dynamic line will occupy, so drop it before
	// appending; without this, every spinner/approval frame gains a blank row.
	if m.busy || m.pendingApproval {
		lines = lines[:len(lines)-1]
	}

	if m.busy {
		spinner := statusStyle.Render(spinnerFrames[m.spinnerFrame] + " thinking...")
		lines = append(lines[:len(lines):len(lines)], spinner, "")
	}

	optionsRow := -1

	if m.pendingApproval {
		optionsRow = len(lines) + 1
		approval, opts := m.approvalLines()
		lines = append(lines[:len(lines):len(lines)], approval...)
		m.clicks.opts = opts
	}

	return lines, optionsRow
}

// rebuild renders the width-dependent part of the screen once.
func (c *renderCache) rebuild(m Model) {
	var b strings.Builder

	b.WriteString("\n")
	b.WriteString(centeredTitle(m.width))
	b.WriteString("\n")
	b.WriteString(centeredMeta(m.width, m.provider, m.modelName, m.sessionUID, m.workdir))
	b.WriteString("\n\n")

	for _, message := range m.messages {
		b.WriteString(m.renderMessage(message))
		b.WriteString("\n\n")
	}

	c.lines = strings.Split(b.String(), "\n")
	c.width = m.width
	c.messageCount = len(m.messages)
	c.dirty = false
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

// approvalLines renders the approval prompt and its clickable buttons,
// returning each button's column range for mouse hit-testing.
func (m Model) approvalLines() ([]string, []approvalOption) {
	var buttons strings.Builder

	opts := make([]approvalOption, 0, len(approvalChoices))

	col := 0
	for i, choice := range approvalChoices {
		if i > 0 {
			buttons.WriteString(buttonGap)
			col += len(buttonGap)
		}

		style := optionStyle
		if choice.answer == m.hovered {
			style = optionHoverStyle
		}

		button := style.Render(choice.label)
		width := lipgloss.Width(button)

		buttons.WriteString(button)
		opts = append(opts, approvalOption{
			start:  col,
			end:    col + width,
			answer: choice.answer,
		})
		col += width
	}

	return []string{
		approvalStyle.Render("approval required — type y/a/n, or click:"),
		buttons.String(),
		"",
	}, opts
}

// visibleLines picks the part of the history to show: the newest (height-1)
// lines, shifted up by the scroll offset; the prompt is pinned below them.
// It also records the scroll limit and whether the approval buttons (on
// optionsRow, or -1) are on screen, for the next Update.
func (m Model) visibleLines(lines []string, optionsRow int) []string {
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

// filterModels filters the picker's source choices by case-insensitive substring.
func (m Model) filterModels(query string) []string {
	source := m.allModelChoices
	lowers := m.allModelChoicesLower
	if len(source) == 0 {
		source = m.modelChoices
		lowers = nil
	}
	q := strings.ToLower(query)
	if q == "" {
		out := make([]string, len(source))
		copy(out, source)
		return out
	}
	out := []string{}
	for i, model := range source {
		lower := ""
		if i < len(lowers) {
			lower = lowers[i]
		} else {
			lower = strings.ToLower(model)
		}
		if strings.Contains(lower, q) {
			out = append(out, model)
		}
	}
	return out
}

func (m *Model) syncModelQuery(q string) {
	m.modelSearch = q
	m.modelQuery = q
	m.searchQuery = q
	m.modelFilter = q
	m.input = q
}

func (m Model) currentModelQuery() string {
	if m.modelSearch != "" {
		return m.modelSearch
	}
	if m.modelQuery != "" {
		return m.modelQuery
	}
	if m.searchQuery != "" {
		return m.searchQuery
	}
	if m.modelFilter != "" {
		return m.modelFilter
	}
	return m.input
}

func (m *Model) clearModelQuery() {
	m.syncModelQuery("")
}

func (m *Model) applyModelSearch() {
	q := m.modelSearch
	if q == "" {
		q = m.modelQuery
	}
	if q == "" {
		q = m.searchQuery
	}
	if q == "" {
		q = m.modelFilter
	}
	if q == "" {
		q = m.input
	}
	m.syncModelQuery(q)
	m.cursor = len([]rune(q))
	m.modelChoices = m.filterModels(q)
	m.modelCursor = 0
	m.modelScroll = 0
	m.ensureModelCursorVisible()
}

// modelMaxScroll returns the largest valid modelScroll for the current choices.
func (m Model) modelMaxScroll() int {
	count := m.modelVisibleCount()
	max := len(m.modelChoices) - count
	if max < 0 {
		max = 0
	}
	return max
}

// modelVisibleCount returns how many model rows fit on screen.
func (m Model) modelVisibleCount() int {
	n := 10
	if len(m.modelChoices) < n {
		n = len(m.modelChoices)
	}
	if n < 1 {
		n = 1
	}
	if max := m.height - 6; max > 0 && n > max {
		n = max
	}
	if n < 1 {
		n = 1
	}
	return n
}

// ensureModelCursorVisible keeps the cursor inside the scrolled window.
func (m *Model) ensureModelCursorVisible() {
	count := m.modelVisibleCount()
	if count < 1 {
		count = 1
	}
	if m.modelCursor < m.modelScroll {
		m.modelScroll = m.modelCursor
	}
	if m.modelCursor >= m.modelScroll+count {
		m.modelScroll = m.modelCursor - count + 1
	}
	m.modelScroll = clampInt(m.modelScroll, 0, m.modelMaxScroll())
}

// selectModel disables the picker, sends the chosen ID, and clears the result channel.
func (m *Model) selectModel(index int) Model {
	selected := ""
	if index >= 0 && index < len(m.modelChoices) {
		selected = m.modelChoices[index]
	}
	m.modelCursor = index
	m.modelPicker = false
	m.input = ""
	m.cursor = 0
	m.modelSearch = ""
	m.modelQuery = ""
	m.searchQuery = ""
	m.modelFilter = ""
	if selected != "" {
		m.modelName = selected
		m.cache.invalidate()
	}
	if m.modelResult != nil {
		select {
		case m.modelResult <- selected:
		default:
		}
		m.modelResult = nil
	}
	activePickerResult = nil
	return *m
}

// cancelModelPicker disables the picker and sends an empty selection.
func (m *Model) cancelModelPicker() Model {
	m.modelPicker = false
	m.input = ""
	m.cursor = 0
	m.modelSearch = ""
	m.modelQuery = ""
	m.searchQuery = ""
	m.modelFilter = ""
	if m.modelResult != nil {
		select {
		case m.modelResult <- "":
		default:
		}
		m.modelResult = nil
	}
	activePickerResult = nil
	return *m
}

// modelIndexAt maps a screen cell to a model index, or -1 if none.
// modelIndexAt maps a screen cell to a model index, or -1 if none.
func modelIndexAt(c *clickTargets, x, y int, choices ...[]string) int {
	if c == nil || !c.modelVisible {
		return -1
	}
	if c.modelBoxWidth > 0 && (x < c.modelBoxLeft || x >= c.modelBoxLeft+c.modelBoxWidth) {
		return -1
	}
	if y < c.modelFirstRow || y >= c.modelFirstRow+c.modelCount {
		return -1
	}
	idx := c.modelOffset + (y - c.modelFirstRow)
	if idx < c.modelOffset || idx >= c.modelOffset+c.modelCount {
		return -1
	}
	total := -1
	if len(choices) > 0 {
		total = len(choices[0])
	} else if c.modelChoices != nil {
		total = len(c.modelChoices)
	} else if c.choices != nil {
		total = len(c.choices)
	} else if c.models != nil {
		total = len(c.models)
	}
	if total < 0 {
		// No choices information provided: preserve legacy count-based bounds.
		total = c.modelOffset + c.modelCount
	}
	if idx >= total {
		return -1
	}
	return idx
}

func runAgent(
	ctx context.Context,
	agent AgentFunc,
	provider string,
	modelName string,
	userMessage string,
	reader *bufio.Reader,
) tea.Cmd {
	return func() tea.Msg {
		result, err := agent(
			ctx,
			provider,
			modelName,
			userMessage,
			reader,
		)

		return agentFinishedMsg{
			response: result.Response,
			swap:     result.Messages,
			err:      err,
		}
	}
}
