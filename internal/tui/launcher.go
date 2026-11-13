package tui

import (
	"errors"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattthew/optine/internal/slashCommands"
)

// The launcher is what optine shows before a session exists: a banner, then
// one small inline Bubble Tea program per question (the command prompt, a
// list pick, an API key). Each program leaves a one-line record of the
// answer behind, so the terminal reads like a transcript and anything the
// commands print in between stays visible.

// ErrNotInteractive is returned by the launcher widgets when the terminal
// cannot run them; callers fall back to plain line input.
var ErrNotInteractive = errors.New("terminal is not interactive")

var (
	launcherPromptStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("99"))
	launcherDoneStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	launcherTitleStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("231"))
	launcherNoticeStyle = lipgloss.NewStyle().Italic(true).Foreground(lipgloss.Color("11"))
	launcherCheckStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("42"))
)

// Banner renders the startup screen: the logo, the tagline and the commands
// available before a session starts. width <= 0 means unknown.
func Banner(width int, commands []slashCommands.Command) string {
	var b strings.Builder

	b.WriteString("\n")
	for _, line := range logoLines(width) {
		b.WriteString("  " + line + "\n")
	}
	b.WriteString("  " + taglineStyle.Render(tagline) + "\n\n")

	b.WriteString("  " + launcherTitleStyle.Render("Get started") + "\n")

	nameWidth := 0
	for _, c := range commands {
		nameWidth = max(nameWidth, runeLen(c.Name))
	}
	for _, c := range commands {
		b.WriteString(renderPaletteRow(c, nameWidth, width, false) + "\n")
	}

	b.WriteString("\n  " + hintStyle.Render("Type / to pick a command, or type it and press enter.") + "\n")

	return b.String()
}

// ---------------------------------------------------------------------------
// Command prompt
// ---------------------------------------------------------------------------

// commandPromptModel is the launcher's "> " line with the slash palette
// below it.
type commandPromptModel struct {
	input   string
	cursor  int
	palette commandPalette
	notice  string
	width   int
	height  int

	done bool // submitted; View shows the final record
	quit bool // ctrl+c or ctrl+d
}

func (m commandPromptModel) Init() tea.Cmd { return nil }

func (m commandPromptModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case tea.KeyMsg:
		return m.updateKey(msg)
	}

	return m, nil
}

func (m commandPromptModel) updateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	matches := m.palette.matches(m.input)

	switch msg.String() {
	case "ctrl+c", "ctrl+d":
		m.quit = true
		return m, tea.Quit

	case "up", "ctrl+p":
		m.palette.move(-1, len(matches))
		return m, nil

	case "down", "ctrl+n":
		m.palette.move(1, len(matches))
		return m, nil

	case "tab":
		if c, ok := m.palette.highlighted(m.input); ok {
			m.setInput(c.Name)
		}
		return m, nil

	case "esc":
		switch {
		case len(matches) > 0:
			m.palette.hidden = true
		case m.input != "":
			m.setInput("")
		}
		return m, nil

	case "enter":
		return m.submit()
	}

	before := m.input
	editInput(&m.input, &m.cursor, msg)
	if m.input != before {
		m.palette.reset()
		m.notice = ""
	}

	return m, nil
}

func (m *commandPromptModel) setInput(s string) {
	m.input = s
	m.cursor = runeLen(s)
	m.palette.reset()
	m.notice = ""
}

func (m commandPromptModel) submit() (tea.Model, tea.Cmd) {
	if c, ok := m.palette.choice(m.input); ok {
		m.input = c.Name
	}

	line := strings.TrimSpace(m.input)
	switch {
	case line == "":
		return m, nil

	case !strings.HasPrefix(line, "/"):
		m.notice = "optine takes commands here: start a session with /local or /remote, or type / to see them all"
		return m, nil
	}

	m.input = line
	m.done = true

	return m, tea.Quit
}

func (m commandPromptModel) View() string {
	if m.done || m.quit {
		if m.input == "" {
			return ""
		}
		return launcherPromptStyle.Render("❯ ") + launcherDoneStyle.Render(m.input) + "\n"
	}

	var b strings.Builder

	b.WriteString(launcherPromptStyle.Render("❯ ") + inputWithCursor(m.input, m.cursor, m.width-2))

	if m.notice != "" {
		b.WriteString("\n" + launcherNoticeStyle.Render(truncateRunes("  "+m.notice, max(m.width, 1))))
	}

	maxRows := paletteMaxRows
	if m.height > 0 {
		maxRows = min(maxRows, m.height-3)
	}

	suggestions := m.palette.render(m.input, m.width, maxRows)
	for _, line := range suggestions {
		b.WriteString("\n" + line)
	}

	// Without suggestions the frame still ends in a blank row. Bubble Tea's
	// inline renderer skips an unchanged last line and then erases below
	// it, which would wipe the prompt when the palette closes; a last line
	// that differs from the old frame avoids that.
	if len(suggestions) == 0 {
		b.WriteString("\n")
	}

	return b.String()
}

// PromptCommand shows the launcher prompt and returns the submitted command.
// ok is false when the user pressed ctrl+c or ctrl+d to leave.
func PromptCommand(commands []slashCommands.Command) (line string, ok bool, err error) {
	final, err := tea.NewProgram(commandPromptModel{palette: commandPalette{commands: commands}}).Run()
	if err != nil {
		return "", false, err
	}

	m, _ := final.(commandPromptModel)
	if m.quit || !m.done {
		return "", false, nil
	}

	return m.input, true, nil
}

// ---------------------------------------------------------------------------
// List picker
// ---------------------------------------------------------------------------

const selectMaxRows = 10

// selectModel is an inline, filterable single-choice list.
type selectModel struct {
	title   string
	all     []string
	choices []string
	query   string
	cursor  int
	scroll  int
	height  int
	width   int

	selected string
	done     bool
	canceled bool
}

func newSelectModel(title string, options []string) selectModel {
	return selectModel{
		title:   title,
		all:     append([]string(nil), options...),
		choices: append([]string(nil), options...),
	}
}

func (m selectModel) Init() tea.Cmd { return nil }

func (m selectModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.keepCursorVisible()
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			if msg.String() == "esc" && m.query != "" {
				m.setQuery("")
				return m, nil
			}
			m.canceled = true
			return m, tea.Quit

		case "up", "ctrl+p":
			if m.cursor > 0 {
				m.cursor--
			}
			m.keepCursorVisible()

		case "down", "ctrl+n":
			if m.cursor < len(m.choices)-1 {
				m.cursor++
			}
			m.keepCursorVisible()

		case "enter":
			if len(m.choices) == 0 {
				return m, nil
			}
			m.selected = m.choices[clampInt(m.cursor, 0, len(m.choices)-1)]
			m.done = true
			return m, tea.Quit

		case "backspace", "ctrl+h":
			if r := []rune(m.query); len(r) > 0 {
				m.setQuery(string(r[:len(r)-1]))
			}

		case "ctrl+u":
			m.setQuery("")

		default:
			if len(msg.Runes) > 0 && !msg.Alt {
				m.setQuery(m.query + string(promptSafe(msg.Runes)))
			}
		}
	}

	return m, nil
}

func (m *selectModel) setQuery(q string) {
	m.query = q

	lowers := make([]string, len(m.all))
	for i, choice := range m.all {
		lowers[i] = strings.ToLower(choice)
	}
	m.choices = pickRows(m.all, filterPicker(lowers, q))
	m.cursor = 0
	m.scroll = 0
}

func (m selectModel) visibleRows() int {
	rows := min(len(m.choices), selectMaxRows)
	if m.height > 0 {
		rows = min(rows, m.height-3)
	}

	return max(rows, 1)
}

func (m *selectModel) keepCursorVisible() {
	rows := m.visibleRows()
	if m.cursor < m.scroll {
		m.scroll = m.cursor
	}
	if m.cursor >= m.scroll+rows {
		m.scroll = m.cursor - rows + 1
	}
	m.scroll = clampInt(m.scroll, 0, max(0, len(m.choices)-rows))
}

func (m selectModel) View() string {
	if m.done {
		// Labels may carry a description after a column gap; the record
		// keeps only the choice itself.
		chosen, _, _ := strings.Cut(m.selected, "  ")
		return launcherCheckStyle.Render("✓ ") + launcherTitleStyle.Render(m.title) + " " + launcherDoneStyle.Render(strings.TrimSpace(chosen)) + "\n"
	}
	if m.canceled {
		return launcherDoneStyle.Render("✗ "+m.title+" (cancelled)") + "\n"
	}

	var b strings.Builder

	header := launcherPromptStyle.Render("? ") + launcherTitleStyle.Render(m.title)
	if m.query != "" {
		header += "  " + launcherDoneStyle.Render("filter: "+m.query)
	}
	b.WriteString(header)

	if len(m.choices) == 0 {
		b.WriteString("\n" + hintStyle.Render("  no match for "+m.query))
	}

	rows := m.visibleRows()
	end := min(len(m.choices), m.scroll+rows)
	for i := m.scroll; i < end; i++ {
		line := "  " + m.choices[i]
		if m.width > 0 {
			line = truncateRunes(line, m.width)
		}
		if i == m.cursor {
			line = "❯ " + strings.TrimPrefix(line, "  ")
			if m.width > 0 {
				line += strings.Repeat(" ", max(0, m.width-runeLen(line)))
			}
			line = paletteSelectedStyle.Render(line)
		}
		b.WriteString("\n" + line)
	}

	hint := "↑/↓ move · enter select · type to filter · esc cancel"
	if len(m.choices) > rows {
		hint = strings.Join([]string{pickerCounter(pickerState{choices: m.choices, cursor: m.cursor}), hint}, " · ")
	}
	b.WriteString("\n" + hintStyle.Render(truncateRunes("  "+hint, max(m.width, len(hint)+2))))

	return b.String()
}

// Select shows an inline list and returns the chosen option. ok is false
// when the user cancelled with esc or ctrl+c.
func Select(title string, options []string) (choice string, ok bool, err error) {
	if len(options) == 0 {
		return "", false, errors.New("nothing to choose from")
	}

	final, err := tea.NewProgram(newSelectModel(title, options)).Run()
	if err != nil {
		return "", false, err
	}

	m, _ := final.(selectModel)
	if !m.done {
		return "", false, nil
	}

	return m.selected, true, nil
}

// ---------------------------------------------------------------------------
// Secret input
// ---------------------------------------------------------------------------

// secretModel reads one line without echoing it: each rune shows as a dot.
type secretModel struct {
	label    string
	value    []rune
	done     bool
	canceled bool
}

func (m secretModel) Init() tea.Cmd { return nil }

func (m secretModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}

	switch key.String() {
	case "ctrl+c", "esc":
		m.canceled = true
		return m, tea.Quit

	case "enter":
		if strings.TrimSpace(string(m.value)) == "" {
			return m, nil
		}
		m.done = true
		return m, tea.Quit

	case "backspace", "ctrl+h":
		if len(m.value) > 0 {
			m.value = m.value[:len(m.value)-1]
		}

	case "ctrl+u":
		m.value = nil

	default:
		if len(key.Runes) > 0 && !key.Alt {
			m.value = append(m.value, promptSafe(key.Runes)...)
		}
	}

	return m, nil
}

func (m secretModel) View() string {
	switch {
	case m.done:
		return launcherCheckStyle.Render("✓ ") + launcherTitleStyle.Render(m.label) + " " + launcherDoneStyle.Render(strings.Repeat("•", min(len(m.value), 24))) + "\n"
	case m.canceled:
		return launcherDoneStyle.Render("✗ "+m.label+" (cancelled)") + "\n"
	}

	return launcherPromptStyle.Render("? ") + launcherTitleStyle.Render(m.label) + " " +
		strings.Repeat("•", len(m.value)) + cursorGlyph + "\n" +
		hintStyle.Render("  input is hidden · paste with your terminal's paste shortcut · enter save · esc cancel")
}

// PromptSecret reads a line without echoing it, for API keys. ok is false
// when the user cancelled.
func PromptSecret(label string) (value string, ok bool, err error) {
	final, err := tea.NewProgram(secretModel{label: label}).Run()
	if err != nil {
		return "", false, err
	}

	m, _ := final.(secretModel)
	if !m.done {
		return "", false, nil
	}

	return strings.TrimSpace(string(m.value)), true, nil
}

// ---------------------------------------------------------------------------
// Shared input helpers
// ---------------------------------------------------------------------------

// editInput applies one editing key to a single-line input.
func editInput(input *string, cursor *int, msg tea.KeyMsg) {
	runes := []rune(*input)
	pos := clampInt(*cursor, 0, len(runes))

	switch msg.String() {
	case "left":
		if pos > 0 {
			pos--
		}

	case "right":
		if pos < len(runes) {
			pos++
		}

	case "home", "ctrl+a":
		pos = 0

	case "end", "ctrl+e":
		pos = len(runes)

	case "backspace", "ctrl+h":
		if pos > 0 {
			runes = append(runes[:pos-1:pos-1], runes[pos:]...)
			pos--
		}

	case "delete":
		if pos < len(runes) {
			runes = append(runes[:pos:pos], runes[pos+1:]...)
		}

	case "ctrl+w":
		before := deleteLastWord(string(runes[:pos]))
		rest := runes[pos:]
		runes = append([]rune(before), rest...)
		pos = runeLen(before)

	case "ctrl+u":
		runes, pos = nil, 0

	default:
		if len(msg.Runes) > 0 && !msg.Alt {
			typed := promptSafe(msg.Runes)
			next := make([]rune, 0, len(runes)+len(typed))
			next = append(next, runes[:pos]...)
			next = append(next, typed...)
			next = append(next, runes[pos:]...)
			runes = next
			pos += len(typed)
		}
	}

	*input = string(runes)
	*cursor = pos
}

// inputWithCursor draws input with a block cursor, windowed to width cells
// around the cursor so long input never wraps. width <= 0 means no limit.
func inputWithCursor(input string, cursor, width int) string {
	runes := []rune(input)
	cursor = clampInt(cursor, 0, len(runes))
	body := []rune(string(runes[:cursor]) + cursorGlyph + string(runes[cursor:]))

	if width > 0 && len(body) > width {
		start := max(0, cursor+1-width)
		end := min(len(body), start+width)
		body = body[start:end]
	}

	return string(body)
}
