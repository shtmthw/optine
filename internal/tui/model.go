package tui

import (
	"bufio"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattthew/optine/internal/harnessSession"
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
	roleUser       = "user"
	roleAgent      = "agent"
	roleError      = "error"
	roleEvent      = "event"
	roleToolResult = "tool"
	roleToolCall   = "tool_call"
	// roleHelp is the /help command list. It is display-only: the agent
	// never sees it and it is not part of the saved transcript.
	roleHelp = "help"
	// roleCommand is the output of a slash command, shown in a grey box. Its
	// first line is the command, the rest is what the command printed.
	roleCommand = "command"
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
	// escWindow is how long the first Esc stays armed; a second Esc inside
	// it interrupts the running agent loop.
	escWindow = 3 * time.Second

	escHint          = "press esc again to interrupt"
	interruptingText = "interrupting... saving session"

	// promptPlaceholder fills the empty prompt with the key hints.
	promptPlaceholder = "/ for commands · esc twice to interrupt · alt+c copy reply · pgup/pgdn scroll"

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

	// runCancel cancels the context of the agent run in flight; nil when
	// idle. Each run gets its own context so an interrupt never touches the
	// program-wide one.
	runCancel context.CancelFunc

	// Double-Esc interrupt. The first Esc arms it and shows escHint; a
	// second Esc within escWindow cancels the run. escSeq tells a stale
	// expiry tick apart from the current arming.
	escArmed     bool
	escSeq       int
	interrupting bool // cancel sent, waiting for the harness to wind down and commit

	// modelLoading shows a yellow loader row while /model loads a model into
	// memory (Ollama) to read its context length.
	modelLoading bool
	loadingModel string

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

	picker pickerState

	// palette suggests slash commands while the input starts with "/".
	palette commandPalette

	// quitRequested is set by /quit so Run can tell the caller to exit
	// optine instead of returning to the launcher.
	quitRequested bool

	// commandRun is the slash command in flight, "" during a normal agent
	// run. Lines it prints are gathered into one roleCommand box, at
	// commandMsg in messages (-1 until the first line arrives).
	commandRun string
	commandMsg int
}

// clickTargets is written by View and read by Update.
type clickTargets struct {
	row       int // screen row of the approval buttons
	opts      []approvalOption
	visible   bool // whether the buttons are on screen right now
	maxScroll int  // largest valid Model.scroll for the current content

	pickerVisible  bool // whether the picker list is on screen
	pickerFirstRow int  // screen row of the first picker row
	pickerCount    int  // number of visible picker rows
	pickerOffset   int  // index of the first visible picker row
	pickerTotal    int  // number of rows in the current filter
	pickerBoxLeft  int  // screen column of the picker box's left edge
	pickerBoxWidth int  // rendered width of the picker box

	paletteVisible  bool // whether command suggestions are on screen
	paletteFirstRow int  // screen row of the first suggestion
	paletteStart    int  // index of the first visible suggestion
	paletteRows     int  // number of visible suggestions
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

// escExpiredMsg disarms the first Esc once escWindow has passed.
type escExpiredMsg struct {
	seq int
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

// transcriptEventMsg carries one live assistant or tool message.
type transcriptEventMsg struct {
	role    string
	content string
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
	UID      string
}

// NotifyModelChanged pushes a provider/model update into the active TUI so its
// status line stays in sync with a /model selection.
func NotifyModelChanged(provider, model, UID string) {
	if activeProgram != nil {
		activeProgram.Send(modelChangedMsg{provider: provider, model: model, UID: UID})
	}
}

// modelLoadingMsg toggles the yellow loader row shown while a model is being
// loaded into memory to read its context length.
type modelLoadingMsg struct {
	active bool
	model  string
}

// NotifyModelLoading toggles the yellow "loading <model>..." loader in the
// active TUI. It is used by /model while Ollama loads the selected model.
func NotifyModelLoading(active bool, model string) {
	if activeProgram != nil {
		activeProgram.Send(modelLoadingMsg{active: active, model: model})
	}
}

type tickMsg struct{}

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// Styles used by the chat view in this file.
var (
	// The header title takes the purple accent; the provider/model/session
	// and directory beside it take the light grey.
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Italic(true).
			Foreground(lipgloss.Color("99"))

	headerInfoStyle = lipgloss.NewStyle().
			Italic(true).
			Foreground(lipgloss.Color("#D3D3D3"))

	// The user stays bright white so it out-contrasts the colored agent text.
	userStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("231"))

	agentColor = lipgloss.Color("253")

	agentStyle = lipgloss.NewStyle().
			Foreground(agentColor)

	errorStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("196"))

	statusStyle = lipgloss.NewStyle().
			Italic(true).
			Foreground(lipgloss.Color("99"))

	// loadingStyle is the yellow variant of statusStyle, used while /model
	// loads a model into memory.
	loadingStyle = lipgloss.NewStyle().
			Italic(true).
			Foreground(lipgloss.Color("11"))

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
			Bold(true).
			Foreground(agentColor)

	agentTextBase = lipgloss.NewStyle().
			Foreground(agentColor)

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
		m.commandRun = ""
		m.commandMsg = -1
		m.busy = false
		m.pendingApproval = false
		m.hovered = ""
		m.escArmed = false
		m.interrupting = false
		if m.runCancel != nil {
			m.runCancel() // release the run's context
			m.runCancel = nil
		}

		if msg.swap != nil {
			// /resume and /restore replace the old conversation wholesale.
			m.messages = msg.swap
			m.scroll = 0
		} else if errors.Is(msg.err, context.Canceled) {
			// A user interrupt is not a failure. The harness has already
			// committed the partial run; show the same marker a resumed
			// transcript shows for it.
			m.messages = append(m.messages, Message{
				Role:    roleEvent,
				Content: harnessSession.InterruptedNotice,
			})
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
		switch {
		case m.commandRun != "" && m.commandMsg >= 0 && m.commandMsg < len(m.messages):
			m.messages[m.commandMsg].Content += "\n" + msg.text
		case m.commandRun != "":
			m.commandMsg = len(m.messages)
			m.messages = append(m.messages, Message{
				Role:    roleCommand,
				Content: m.commandRun + "\n" + msg.text,
			})
		default:
			m.messages = append(m.messages, Message{
				Role:    roleEvent,
				Content: msg.text,
			})
		}
		m.cache.invalidate()

	case transcriptEventMsg:
		m.messages = append(m.messages, Message{
			Role:    msg.role,
			Content: msg.content,
		})
		m.cache.invalidate()

	case approvalRequestedMsg:
		if m.interrupting {
			// The harness reached a prompt before it saw the cancel; deny
			// it so the tool never runs and the loop can wind down.
			return m, sendApproval(m.approvalCh, "n")
		}
		m.pendingApproval = true
		m.hovered = ""
		m.scroll = 0 // make sure the prompt is on screen

	case sessionChangedMsg:
		m.sessionUID = msg.uid
		m.cache.invalidate()

	case modelChangedMsg:
		m.provider = msg.provider
		m.modelName = msg.model
		if msg.UID != "" {
			m.sessionUID = msg.UID
		}
		m.cache.invalidate()

	case modelLoadingMsg:
		m.modelLoading = msg.active
		if msg.model != "" {
			m.loadingModel = msg.model
		}

	case pickerOpenMsg:
		m.openPicker(msg)

	case escExpiredMsg:
		if msg.seq == m.escSeq {
			m.escArmed = false
		}

	case tickMsg:
		if m.busy || m.modelLoading {
			m.spinnerFrame = (m.spinnerFrame + 1) % len(spinnerFrames)
			return m, tick()
		}
	}

	return m, nil
}

func (m Model) updateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.picker.active() {
		return m.updatePickerKey(msg)
	}

	if m.paletteShown() {
		if next, cmd, handled := m.updatePaletteKey(msg); handled {
			return next, cmd
		}
	}

	before := m.input
	next, cmd := m.updateInputKey(msg)

	// Any edit to the input reopens the palette at its first suggestion.
	if nm, ok := next.(Model); ok && nm.input != before {
		nm.palette.reset()
		next = nm
	}

	return next, cmd
}

// paletteShown reports whether command suggestions are on screen: the input
// is a lone slash word, nothing else owns the keyboard, and something matches.
func (m Model) paletteShown() bool {
	if m.busy || m.pendingApproval || m.picker.active() {
		return false
	}

	return len(m.palette.matches(m.input)) > 0
}

// updatePaletteKey handles the keys the palette owns. Anything else falls
// through to normal input editing.
func (m Model) updatePaletteKey(msg tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	n := len(m.palette.matches(m.input))

	switch msg.String() {
	case "up", "ctrl+p":
		m.palette.move(-1, n)
		return m, nil, true

	case "down", "ctrl+n":
		m.palette.move(1, n)
		return m, nil, true

	case "tab":
		if c, ok := m.palette.highlighted(m.input); ok {
			m.input = c.Name
			m.cursor = runeLen(m.input)
			m.palette.reset()
		}
		return m, nil, true

	case "enter":
		if c, ok := m.palette.choice(m.input); ok {
			m.input = c.Name
			m.cursor = runeLen(m.input)
			m.palette.reset()
		}
		next, cmd := m.submit()
		return next, cmd, true

	case "esc":
		m.palette.hidden = true
		return m, nil, true
	}

	return m, nil, false
}

// runPaletteRow runs the suggestion at index i, as if it was typed.
func (m Model) runPaletteRow(i int) (tea.Model, tea.Cmd) {
	matches := m.palette.matches(m.input)
	if i < 0 || i >= len(matches) {
		return m, nil
	}

	m.input = matches[i].Name
	m.cursor = runeLen(m.input)
	m.palette.reset()

	return m.submit()
}

// updateInputKey edits the prompt and handles the global shortcuts.
func (m Model) updateInputKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {

	case "ctrl+c":
		return m, tea.Quit

	case "esc":
		return m.pressEsc()

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

// pressEsc implements the double-Esc interrupt: the first press while a run
// is in flight arms it and tells the user a second press will interrupt; the
// second press within escWindow cancels the run.
func (m Model) pressEsc() (tea.Model, tea.Cmd) {
	if !m.busy || m.interrupting || m.runCancel == nil {
		return m, nil
	}

	if !m.escArmed {
		m.escArmed = true
		m.escSeq++
		m.scroll = 0 // make sure the hint is on screen
		seq := m.escSeq

		return m, tea.Tick(escWindow, func(time.Time) tea.Msg {
			return escExpiredMsg{seq: seq}
		})
	}

	return m.interrupt()
}

// interrupt cancels the running agent loop. The harness sees the cancelled
// context, stops at the next safe point (in-flight HTTP request aborted,
// running bash process group killed, remaining tool calls skipped), commits
// the session and returns; agentFinishedMsg then resets the UI.
func (m Model) interrupt() (tea.Model, tea.Cmd) {
	m.escArmed = false
	m.interrupting = true
	m.runCancel()

	if m.pendingApproval {
		// The harness is blocked reading the approval answer and cannot see
		// the cancel until it gets one. Deny: an interrupt must never run a
		// tool.
		return m.answerApproval("n")
	}

	return m, nil
}

// submit handles Enter: it answers a pending approval, or sends the typed
// text to the agent as a new chat message.
func (m Model) submit() (tea.Model, tea.Cmd) {
	if m.picker.active() {
		m = m.selectPicker(m.picker.cursor)
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

	// /quit and /help need nothing from the agent, so the interface answers
	// them itself instead of starting a run.
	switch userMessage {
	case "/quit", "/exit", "/q":
		m.quitRequested = true
		return m, tea.Quit

	case "/help", "/h":
		m.messages = append(m.messages, Message{
			Role:    roleHelp,
			Content: helpContent(m.palette.commands),
		})
		m.input = ""
		m.cursor = 0
		m.scroll = 0
		m.palette.reset()
		m.cache.invalidate()
		return m, nil
	}

	m.commandRun, m.commandMsg = "", -1
	if strings.HasPrefix(userMessage, "/") {
		m.commandRun = strings.Fields(userMessage)[0]
	} else {
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

	parent := m.ctx
	if parent == nil {
		parent = context.Background()
	}
	runCtx, cancel := context.WithCancel(parent)
	m.runCancel = cancel
	m.escArmed = false
	m.interrupting = false

	return m, tea.Batch(
		runAgent(
			runCtx,
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
	if m.picker.active() {
		return m.updatePickerMouse(msg)
	}

	if m.paletteShown() {
		if idx := paletteIndexAt(m.clicks, msg.Y); idx >= 0 {
			switch {
			case msg.Action == tea.MouseActionMotion:
				m.palette.cursor = idx
				return m, nil

			case msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft:
				return m.runPaletteRow(idx)
			}
		}
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

// titleSpaced is the header title, letter-spaced so it reads a size larger
// in a terminal, where the font size is fixed. It is plain ASCII on purpose:
// fullwidth letters (Ｏｐｔｉｎｅ) are counted as two cells but some
// terminals draw them wider, which pushed the directory off the screen.
// titleSmall is the same word unspaced, for screens too narrow for it.
const (
	titleSpaced = "O p t i n e"
	titleSmall  = "Optine."
)

const (
	headerMargin = 2 // cells kept free at each screen edge
	headerGap    = 4 // minimum cells between neighbouring fields
)

// userHome is replaced by ~ in the directory; a variable so tests can pin it.
var userHome, _ = os.UserHomeDir()

// headerSides lists the center text (provider, model, session) and the
// right text (directory) from longest to shortest form.
func headerSides(providerName, modelName, sessionUID, workdir string) (center, right []string) {
	meta := func(sess string) string {
		parts := make([]string, 0, 3)
		for _, p := range []string{providerName, modelName, sess} {
			if p != "" {
				parts = append(parts, p)
			}
		}
		return strings.Join(parts, " · ")
	}

	if sessionUID != "" {
		center = append(center, meta("sess "+sessionUID), meta("sess "+shortSessionUID(sessionUID)))
	}
	center = append(center, meta(""))
	if modelName != "" && providerName != "" {
		center = append(center, modelName)
	}

	if workdir != "" {
		right = []string{"Directory: " + workdir, tildePath(workdir), filepath.Base(workdir)}
	}

	return center, right
}

// headerBar renders the header row at width: the title at the top left,
// provider, model and session exactly centered, the directory at the top
// right. The center and the directory take their longest forms that fit;
// the large title stays until even the shortest forms do not fit, then the
// small one is used, and past that the center and directory are cut.
// width <= 0 means unknown.
func headerBar(width int, providerName, modelName, sessionUID, workdir string) string {
	center, right := headerSides(providerName, modelName, sessionUID, workdir)

	if width <= 0 {
		return strings.TrimSpace(titleStyle.Render(titleSpaced) + "  " + headerInfoStyle.Render(first(center)) + "  " + headerInfoStyle.Render(first(right)))
	}

	for _, title := range []string{titleSpaced, titleSmall} {
		for _, c := range orEmpty(center) {
			leftRoom, rightRoom := headerRooms(width, c)
			if lipgloss.Width(title) > leftRoom {
				continue
			}
			if r, ok := longestFitting(right, rightRoom); ok {
				return layoutHeader(width, title, c, r)
			}
		}
	}

	// Nothing fits whole: small title at the left, the center cut so it keeps
	// the same clearance on both sides, and the directory cut to what is left.
	titleWidth := lipgloss.Width(titleSmall)
	if titleWidth+headerMargin > width {
		return titleStyle.Render(cutRunes(titleSmall, width))
	}

	c := fitHeaderLeft(last(center), width-2*(headerMargin+titleWidth+headerGap))
	_, rightRoom := headerRooms(width, c)
	if c == "" {
		rightRoom = width - headerMargin - titleWidth - headerGap - headerMargin
	}

	r, ok := longestFitting(right, rightRoom)
	if !ok {
		r = fitHeaderRight(last(right), rightRoom)
	}

	return layoutHeader(width, titleSmall, c, r)
}

// headerRooms is the space left and right of a centered text.
func headerRooms(width int, center string) (leftRoom, rightRoom int) {
	centerWidth := lipgloss.Width(center)
	start := (width - centerWidth) / 2

	return start - headerMargin - headerGap, width - start - centerWidth - headerGap - headerMargin
}

// orEmpty lets a header with no center text still be laid out once.
func orEmpty(forms []string) []string {
	if len(forms) == 0 {
		return []string{""}
	}

	return forms
}

// longestFitting returns the first form that fits room. An empty list fits
// anywhere, as nothing.
func longestFitting(forms []string, room int) (string, bool) {
	if len(forms) == 0 {
		return "", true
	}

	for _, f := range forms {
		if lipgloss.Width(f) <= room {
			return f, true
		}
	}

	return "", false
}

// layoutHeader places the title at the left edge, the center text centered
// on the screen and the directory at the right edge.
func layoutHeader(width int, title, center, right string) string {
	var b strings.Builder

	b.WriteString(strings.Repeat(" ", headerMargin) + titleStyle.Render(title))
	used := headerMargin + lipgloss.Width(title)

	if center != "" {
		centerWidth := lipgloss.Width(center)
		start := (width - centerWidth) / 2
		b.WriteString(strings.Repeat(" ", max(0, start-used)) + headerInfoStyle.Render(center))
		used = max(used, start) + centerWidth
	}

	if right != "" {
		pad := width - headerMargin - lipgloss.Width(right) - used
		b.WriteString(strings.Repeat(" ", max(0, pad)) + headerInfoStyle.Render(right))
	}

	return b.String()
}

func first(forms []string) string {
	if len(forms) == 0 {
		return ""
	}

	return forms[0]
}

func last(forms []string) string {
	if len(forms) == 0 {
		return ""
	}

	return forms[len(forms)-1]
}

// minHeaderSide is the narrowest room worth showing a cut side in.
const minHeaderSide = 4

// fitHeaderLeft cuts the left side's end to room cells.
func fitHeaderLeft(text string, room int) string {
	if lipgloss.Width(text) <= room {
		return text
	}
	if room < minHeaderSide {
		return ""
	}

	runes := []rune(text)
	for len(runes) > 0 && lipgloss.Width(string(runes))+1 > room {
		runes = runes[:len(runes)-1]
	}

	return string(runes) + "…"
}

// fitHeaderRight keeps the end of the directory, which names the project.
func fitHeaderRight(text string, room int) string {
	if lipgloss.Width(text) <= room {
		return text
	}
	if room < minHeaderSide {
		return ""
	}

	runes := []rune(text)
	for len(runes) > 0 && lipgloss.Width(string(runes))+1 > room {
		runes = runes[1:]
	}

	return "…" + string(runes)
}

// shortSessionUID keeps the random suffix of a session UID, which is what
// tells sessions started on the same day apart.
func shortSessionUID(uid string) string {
	if i := strings.LastIndex(uid, "-"); i >= 0 && i < len(uid)-1 {
		return uid[i+1:]
	}

	return uid
}

// tildePath writes a path under the home directory as ~/...
func tildePath(p string) string {
	if userHome == "" {
		return p
	}
	if p == userHome {
		return "~"
	}
	if rest, ok := strings.CutPrefix(p, userHome+string(filepath.Separator)); ok {
		return "~/" + rest
	}

	return p
}

func (m Model) View() string {
	if m.picker.active() {
		return m.pickerView()
	}

	palette := m.paletteLines()

	lines, optionsRow := m.historyLines()
	visible := m.visibleLines(lines, optionsRow, len(palette))

	var out strings.Builder
	out.WriteString(strings.Join(visible, "\n"))

	// Lock the prompt to the bottom-left: pad with blank rows so it lands
	// on the last screen row at any resolution, with the command
	// suggestions (if any) directly above it.
	pad := m.height - 1 - len(palette) - len(visible)
	if pad > 0 {
		out.WriteString(strings.Repeat("\n", pad))
	}

	if len(palette) > 0 {
		m.clicks.paletteFirstRow = len(visible) + max(pad, 0)
		for _, line := range palette {
			out.WriteString("\n" + line)
		}
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
	if m.busy || m.pendingApproval || m.modelLoading {
		lines = lines[:len(lines)-1]
	}

	if m.modelLoading {
		spinner := loadingStyle.Render(spinnerFrames[m.spinnerFrame] + " loading " + m.loadingModel + "...")
		lines = append(lines[:len(lines):len(lines)], spinner, "")
	} else if m.busy {
		var spinner string
		switch {
		case m.interrupting:
			spinner = loadingStyle.Render(spinnerFrames[m.spinnerFrame] + " " + interruptingText)
		case m.escArmed:
			spinner = statusStyle.Render(spinnerFrames[m.spinnerFrame]+" thinking...") +
				"  " + loadingStyle.Render(escHint)
		default:
			spinner = statusStyle.Render(spinnerFrames[m.spinnerFrame] + " thinking...")
		}
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

	// Two blank rows of padding above the header bar and two below it.
	b.WriteString("\n\n")
	b.WriteString(headerBar(m.width, m.provider, m.modelName, m.sessionUID, m.workdir))
	b.WriteString("\n\n\n")

	// A tool call joins its directly following result into one block.
	var open *toolCall
	for i, message := range m.messages {
		rendered, next, header := m.renderHistoryMessage(message, open)
		open = next
		if rendered == "" {
			continue
		}
		b.WriteString(rendered)

		if header && i+1 < len(m.messages) && m.messages[i+1].Role == roleToolResult {
			b.WriteString("\n")
		} else {
			b.WriteString("\n\n")
		}
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

	case roleEvent, roleToolResult:
		return m.renderEventContent(message.Content)

	case roleToolCall:
		return renderPlainWrapped(message.Content, m.width, eventStyle, "", "")

	case roleHelp:
		return renderHelp(message.Content, m.width)

	case roleCommand:
		title, body, _ := strings.Cut(message.Content, "\n")
		return renderCommandBox(title, strings.Split(body, "\n"), m.width)
	}

	return ""
}

// paletteLines renders the command suggestions shown above the prompt and
// records where they are for mouse hit-testing. Short terminals keep at
// least one history row and the prompt visible.
func (m Model) paletteLines() []string {
	c := m.clicks
	if c != nil {
		c.paletteVisible = false
	}

	if !m.paletteShown() || m.height < 4 {
		return nil
	}

	// prompt row + hint row + one history row stay free.
	maxRows := m.height - 3
	lines := m.palette.render(m.input, m.width, maxRows)

	if c != nil && len(lines) > 0 {
		_, start, rows := m.palette.window(m.input, maxRows)
		c.paletteVisible = true
		c.paletteStart = start
		c.paletteRows = rows
	}

	return lines
}

// paletteIndexAt maps a screen row to a suggestion index, or -1 if none.
func paletteIndexAt(c *clickTargets, y int) int {
	if c == nil || !c.paletteVisible {
		return -1
	}

	if y < c.paletteFirstRow || y >= c.paletteFirstRow+c.paletteRows {
		return -1
	}

	return c.paletteStart + (y - c.paletteFirstRow)
}

func (m Model) renderEventContent(content string) string {
	if info, ok := parseEditInfo(content); ok {
		return renderEditInfo(info, m.width)
	}
	if info, ok := parseWriteInfo(content); ok {
		return renderWriteInfo(info, m.width)
	}

	return renderPlainWrapped(content, m.width, eventStyle, "", "")
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
// lines, shifted up by the scroll offset; the prompt (and reserved rows of
// command suggestions) are pinned below them.
// It also records the scroll limit and whether the approval buttons (on
// optionsRow, or -1) are on screen, for the next Update.
func (m Model) visibleLines(lines []string, optionsRow int, reserved int) []string {
	contentHeight := m.height - 1 - reserved
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
// around the cursor so "> " always starts at column 0. An empty prompt shows
// the key hints as a faint placeholder, except while an approval is pending,
// when the prompt takes the y/a/n answer.
func (m Model) renderPrompt() string {
	if m.input == "" && !m.pendingApproval {
		return renderPlaceholderPrompt(m.width)
	}

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

// renderPlaceholderPrompt draws "> " and the cursor followed by the faint
// placeholder, cut to fit the row.
func renderPlaceholderPrompt(width int) string {
	prompt := "> " + cursorGlyph
	if width <= 0 {
		return prompt + hintStyle.Render(promptPlaceholder)
	}

	room := width - runeLen(prompt)
	if room <= 0 {
		return cutRunes(prompt, width)
	}

	return prompt + hintStyle.Render(truncateRunes(promptPlaceholder, room))
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
