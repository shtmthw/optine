package tui

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattthew/optine/internal/harnessSession"
)

// The /model, /resume, /rewind and /effort commands share one interactive
// list: a searchable picker rendered as a centered box. pickerKind selects the
// labels and the row format; filtering, scrolling, keys and mouse handling are
// shared, so the commands cannot drift apart again.

type pickerKind int

const (
	pickerNone pickerKind = iota
	pickerModel
	pickerSession
	pickerRewind
	pickerEffort
)

// pickerOpenMsg opens the shared picker in the TUI.
type pickerOpenMsg struct {
	kind    pickerKind
	choices []string                  // pickerModel: model IDs
	metas   []harnessSession.Meta     // pickerSession: saved sessions
	turns   []harnessSession.UserTurn // pickerRewind: the user's messages
	labels  []string                  // pickerEffort: the row shown for each choice
	current string                    // pickerEffort: the choice marked <current>
	result  chan string
}

// pickerState is the picker's whole state. all/allLower/allMetas/allTurns/
// allLabels hold the unfiltered source; choices/metas/turns/labels are the
// filtered rows on screen. allLower is the lowered text the search matches
// against.
type pickerState struct {
	kind      pickerKind
	all       []string
	allLower  []string
	allMetas  []harnessSession.Meta
	allTurns  []harnessSession.UserTurn
	allLabels []string
	choices   []string
	metas     []harnessSession.Meta
	turns     []harnessSession.UserTurn
	labels    []string
	current   string
	query     string
	cursor    int
	scroll    int
	result    chan string
}

// rewindPreviewMax caps a /rewind row's message preview, in runes, so a long
// message cannot widen the box past the screen.
const rewindPreviewMax = 60

func (p pickerState) active() bool { return p.kind != pickerNone }

var (
	pickerSelectedStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("231")).
				Background(lipgloss.Color("99")).
				Padding(0, 1)

	pickerRowStyle = lipgloss.NewStyle().
			Padding(0, 1)

	// pickerCurrentStyle colors the <current> marker on rows that are not
	// selected; on the selected row it takes the row's white text.
	pickerCurrentStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("99"))
)

// pickerCurrentMark follows the row of the current /effort level.
const pickerCurrentMark = " <current>"

// isCurrent reports whether choice is the row marked <current>.
func (p pickerState) isCurrent(choice string) bool {
	return p.kind == pickerEffort && p.current != "" && choice == p.current
}

// openPicker resets the shared picker from an open message. Model IDs and
// effort levels become the rows directly; sessions contribute their UIDs and
// messages their numbers as rows, with the meta, message or effort label kept
// in a parallel slice for rendering. Message and effort rows are searched by
// their text, the others by their ID.
func (m *Model) openPicker(msg pickerOpenMsg) {
	p := &m.picker

	p.kind = msg.kind
	p.allMetas, p.allTurns, p.allLabels = nil, nil, nil
	switch msg.kind {
	case pickerSession:
		p.allMetas = append([]harnessSession.Meta(nil), msg.metas...)
		p.all = make([]string, len(msg.metas))
		for i, s := range msg.metas {
			p.all[i] = s.UID
		}

	case pickerRewind:
		p.allTurns = append([]harnessSession.UserTurn(nil), msg.turns...)
		p.all = make([]string, len(msg.turns))
		for i, t := range msg.turns {
			p.all[i] = strconv.Itoa(t.Number)
		}

	case pickerEffort:
		p.all = append([]string(nil), msg.choices...)
		p.allLabels = append([]string(nil), msg.labels...)

	default:
		p.all = append([]string(nil), msg.choices...)
	}

	p.allLower = make([]string, len(p.all))
	for i, choice := range p.all {
		switch p.kind {
		case pickerRewind:
			choice = rewindRowText(p.allTurns[i])
		case pickerEffort:
			choice = pickerRowText(p.kind, choice, harnessSession.Meta{}, harnessSession.UserTurn{}, rowAt(p.allLabels, i))
			if p.all[i] == msg.current {
				choice += pickerCurrentMark
			}
		}
		p.allLower[i] = strings.ToLower(choice)
	}

	p.choices = append([]string(nil), p.all...)
	p.metas = append([]harnessSession.Meta(nil), p.allMetas...)
	p.turns = append([]harnessSession.UserTurn(nil), p.allTurns...)
	p.labels = append([]string(nil), p.allLabels...)
	p.current = msg.current
	p.query = ""
	p.cursor = 0
	p.scroll = 0
	p.result = msg.result
	activePickerResult = msg.result

	m.input = ""
	m.cursor = 0

	if m.clicks != nil {
		m.clicks.pickerVisible = false
		m.clicks.pickerCount = 0
		m.clicks.pickerOffset = 0
		m.clicks.pickerTotal = 0
		m.clicks.pickerBoxLeft = 0
		m.clicks.pickerBoxWidth = 0
	}

	m.cache.invalidate()
}

// updatePickerKey handles every key while the picker is open.
func (m Model) updatePickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := &m.picker

	switch msg.String() {
	case "up", "k":
		if p.cursor > 0 {
			p.cursor--
		}
		m.ensurePickerCursorVisible()

	case "down", "j":
		if p.cursor < len(p.choices)-1 {
			p.cursor++
		}
		m.ensurePickerCursorVisible()

	case "pgup":
		page := m.pageSize()
		p.cursor = clampInt(p.cursor-page, 0, len(p.choices)-1)
		p.scroll = clampInt(p.scroll-page, 0, m.pickerMaxScroll())
		m.ensurePickerCursorVisible()

	case "pgdown":
		page := m.pageSize()
		p.cursor = clampInt(p.cursor+page, 0, len(p.choices)-1)
		p.scroll = clampInt(p.scroll+page, 0, m.pickerMaxScroll())
		m.ensurePickerCursorVisible()

	case "enter":
		return m.submit()

	case "esc":
		if p.query != "" {
			m.setPickerQuery("")
			return m, nil
		}
		m = m.cancelPicker()
		return m, nil

	case "backspace", "ctrl+h":
		if m.cursor > 0 {
			runes := []rune(m.input)
			m.input = string(runes[:m.cursor-1]) + string(runes[m.cursor:])
			m.cursor--
		}
		m.setPickerQuery(m.input)

	case "ctrl+u":
		m.input = ""
		m.cursor = 0
		m.setPickerQuery("")

	case "ctrl+w":
		runes := []rune(m.input)
		before := deleteLastWord(string(runes[:m.cursor]))
		m.input = before + string(runes[m.cursor:])
		m.cursor = len([]rune(before))
		m.setPickerQuery(m.input)

	default:
		if len(msg.Runes) > 0 && !msg.Alt {
			runes := []rune(m.input)
			typed := promptSafe(msg.Runes)
			m.input = string(runes[:m.cursor]) + string(typed) + string(runes[m.cursor:])
			m.cursor += len(typed)
			m.setPickerQuery(m.input)
		}
	}

	return m, nil
}

// updatePickerMouse handles wheel scrolling, hover and click selection.
func (m Model) updatePickerMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	switch {
	case msg.Button == tea.MouseButtonWheelUp:
		m.picker.scroll = clampInt(m.picker.scroll-wheelStep, 0, m.pickerMaxScroll())

	case msg.Button == tea.MouseButtonWheelDown:
		m.picker.scroll = clampInt(m.picker.scroll+wheelStep, 0, m.pickerMaxScroll())

	case msg.Action == tea.MouseActionMotion:
		if idx := pickerIndexAt(m.clicks, msg.X, msg.Y); idx >= 0 {
			m.picker.cursor = idx
		}

	case msg.Action == tea.MouseActionPress &&
		msg.Button == tea.MouseButtonLeft:
		if idx := pickerIndexAt(m.clicks, msg.X, msg.Y); idx >= 0 {
			m.picker.cursor = idx
			m = m.selectPicker(idx)
		} else {
			m = m.cancelPicker()
		}
	}

	return m, nil
}

// setPickerQuery updates the search text, re-filters the rows and resets the
// list cursor and scroll, exactly like the old per-picker apply functions.
func (m *Model) setPickerQuery(q string) {
	m.picker.query = q
	m.input = q
	m.cursor = len([]rune(q))

	keep := filterPicker(m.picker.allLower, q)
	m.picker.choices = pickRows(m.picker.all, keep)
	m.picker.metas = pickRows(m.picker.allMetas, keep)
	m.picker.turns = pickRows(m.picker.allTurns, keep)
	m.picker.labels = pickRows(m.picker.allLabels, keep)

	m.picker.cursor = 0
	m.picker.scroll = 0
	m.ensurePickerCursorVisible()
}

// filterPicker returns the indexes of the rows whose lowered search text
// contains the lowered query.
func filterPicker(lowers []string, query string) []int {
	q := strings.ToLower(query)
	keep := make([]int, 0, len(lowers))

	for i, lower := range lowers {
		if q == "" || strings.Contains(lower, q) {
			keep = append(keep, i)
		}
	}

	return keep
}

// pickRows returns rows[i] for each kept index, so the parallel row slices
// stay aligned; indexes past the end of rows (no metas for models) are skipped.
func pickRows[T any](rows []T, keep []int) []T {
	out := make([]T, 0, len(keep))
	for _, i := range keep {
		if i < len(rows) {
			out = append(out, rows[i])
		}
	}

	return out
}

// pickerMaxScroll returns the largest valid picker scroll for the current rows.
func (m Model) pickerMaxScroll() int {
	count := m.pickerVisibleCount()
	max := len(m.picker.choices) - count
	if max < 0 {
		max = 0
	}
	return max
}

// pickerVisibleCount returns how many picker rows fit on screen.
func (m Model) pickerVisibleCount() int {
	n := 10
	if len(m.picker.choices) < n {
		n = len(m.picker.choices)
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

// ensurePickerCursorVisible keeps the cursor inside the scrolled window.
func (m *Model) ensurePickerCursorVisible() {
	count := m.pickerVisibleCount()
	if count < 1 {
		count = 1
	}
	if m.picker.cursor < m.picker.scroll {
		m.picker.scroll = m.picker.cursor
	}
	if m.picker.cursor >= m.picker.scroll+count {
		m.picker.scroll = m.picker.cursor - count + 1
	}
	m.picker.scroll = clampInt(m.picker.scroll, 0, m.pickerMaxScroll())
}

// selectPicker closes the picker, sending the chosen row ("" if none). A
// selected model is also reflected in the status line immediately.
func (m *Model) selectPicker(index int) Model {
	selected := ""
	if index >= 0 && index < len(m.picker.choices) {
		selected = m.picker.choices[index]
	}
	if m.picker.kind == pickerModel && selected != "" {
		m.modelName = selected
		m.cache.invalidate()
	}
	m.closePicker(selected)
	return *m
}

// cancelPicker closes the picker and sends an empty selection.
func (m *Model) cancelPicker() Model {
	m.closePicker("")
	return *m
}

// closePicker clears the picker state and delivers the selection without ever
// blocking Update.
func (m *Model) closePicker(selected string) {
	if m.picker.result != nil {
		select {
		case m.picker.result <- selected:
		default:
		}
		m.picker.result = nil
	}

	m.picker = pickerState{}
	m.input = ""
	m.cursor = 0
	activePickerResult = nil
}

// pickerIndexAt maps a screen cell to a picker row index, or -1 if none.
func pickerIndexAt(c *clickTargets, x, y int) int {
	if c == nil || !c.pickerVisible {
		return -1
	}
	if c.pickerBoxWidth > 0 && (x < c.pickerBoxLeft || x >= c.pickerBoxLeft+c.pickerBoxWidth) {
		return -1
	}
	if y < c.pickerFirstRow || y >= c.pickerFirstRow+c.pickerCount {
		return -1
	}

	idx := c.pickerOffset + (y - c.pickerFirstRow)
	if idx < c.pickerOffset || idx >= c.pickerOffset+c.pickerCount {
		return -1
	}
	if idx >= c.pickerTotal {
		return -1
	}

	return idx
}

// pickerRowText formats one row for the active picker kind.
func pickerRowText(kind pickerKind, choice string, meta harnessSession.Meta, turn harnessSession.UserTurn, label string) string {
	switch kind {
	case pickerSession:
		return fmt.Sprintf(
			"%s  %s  %d messages  saved %s",
			choice, meta.Model, meta.TotalUserMessages, meta.UpdatedAt.Format("2006-01-02 15:04"),
		)

	case pickerRewind:
		return rewindRowText(turn)

	case pickerEffort:
		if label != "" {
			return label
		}
	}

	return choice
}

// rewindRowText is a /rewind row: the message number, then the message on
// one line, cut to rewindPreviewMax runes.
func rewindRowText(turn harnessSession.UserTurn) string {
	preview := strings.Join(strings.Fields(turn.Content), " ")
	if preview == "" {
		preview = "(empty message)"
	}

	return fmt.Sprintf("#%d  %s", turn.Number, truncateRunes(preview, rewindPreviewMax))
}

// rowAt returns rows[i], or the zero value when the slice is shorter.
func rowAt[T any](rows []T, i int) T {
	if i >= 0 && i < len(rows) {
		return rows[i]
	}

	var zero T
	return zero
}

// pickerCounter renders the position text shown at the right edge of the
// search box. An empty list shows 0/0 rather than a phantom selection.
func pickerCounter(p pickerState) string {
	total := len(p.choices)
	current := p.cursor + 1
	if total == 0 || current < 1 {
		current = 0
	}
	if current > total {
		current = total
	}

	return fmt.Sprintf("%d/%d", current, total)
}

// pinPickerCounter pins the counter to the right edge of the search box's
// text area by padding the query. When the query leaves no room for a gap the
// old concatenation is kept, so input is never truncated.
func pinPickerCounter(content, counter string, width int) string {
	if width < 1 {
		width = 1
	}

	gap := width - lipgloss.Width(content) - lipgloss.Width(counter)
	if gap >= 1 {
		return content + strings.Repeat(" ", gap) + counter
	}

	return content + "   " + counter
}

// pickerView renders the shared model/session picker.
func (m Model) pickerView() string {
	c := m.clicks
	if c == nil {
		c = &clickTargets{}
		m.clicks = c
	}

	p := m.picker
	choices := p.choices
	visibleCount := m.pickerVisibleCount()
	maxScroll := len(choices) - visibleCount
	if maxScroll < 0 {
		maxScroll = 0
	}
	scroll := clampInt(p.scroll, 0, maxScroll)

	header := "Select a model (↑/↓, Enter, Esc, click):"
	placeholder := "search a model by typing on your keyboard"
	switch p.kind {
	case pickerSession:
		header = "Select a session (↑/↓, Enter, Esc, click):"
		placeholder = "search a session by typing on your keyboard"

	case pickerRewind:
		header = "Select a message to rewind to (↑/↓, Enter, Esc, click):"
		placeholder = "search a message by typing on your keyboard"

	case pickerEffort:
		header = "Select an effort level (↑/↓, Enter, Esc, click):"
		placeholder = "search a level by typing on your keyboard"
	}

	start := scroll
	end := scroll + visibleCount
	if end > len(choices) {
		end = len(choices)
	}
	var rows []string
	for i := start; i < end; i++ {
		line := pickerRowText(p.kind, choices[i], rowAt(p.metas, i), rowAt(p.turns, i), rowAt(p.labels, i))
		current := p.isCurrent(choices[i])
		switch {
		case i == p.cursor && current:
			line = pickerSelectedStyle.Render("> " + line + pickerCurrentMark)
		case i == p.cursor:
			line = pickerSelectedStyle.Render("> " + line)
		case current:
			line = pickerRowStyle.Render("  " + line + pickerCurrentStyle.Render(pickerCurrentMark))
		default:
			line = pickerRowStyle.Render("  " + line)
		}
		rows = append(rows, line)
	}
	for len(rows) < 10 {
		rows = append(rows, "")
	}

	innerWidth := lipgloss.Width(header)
	source := p.all
	if len(source) == 0 {
		source = p.choices
	}
	for i, choice := range source {
		text := pickerRowText(p.kind, choice, rowAt(p.allMetas, i), rowAt(p.allTurns, i), rowAt(p.allLabels, i))
		if p.isCurrent(choice) {
			text += pickerCurrentMark
		}
		line := pickerRowStyle.Render("  " + text)
		if w := lipgloss.Width(line); w > innerWidth {
			innerWidth = w
		}
	}
	// The search box has a fixed width: reserve room for the placeholder and
	// the worst-case "n/n" counter so the pinned row never wraps.
	minContent := placeholder + "   " + fmt.Sprintf("%d/%d", len(source), len(source))
	if required := lipgloss.Width(minContent) + 4; required > innerWidth {
		innerWidth = required
	}
	searchWidth := innerWidth - 4
	if searchWidth < 1 {
		searchWidth = 1
	}

	content := p.query
	if content == "" {
		content = placeholder
	}
	content = pinPickerCounter(content, pickerCounter(p), searchWidth-2)

	searchBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("255")).
		Padding(0, 1).
		Width(searchWidth).
		Render(content)

	var inner []string
	inner = append(inner, header)
	inner = append(inner, rows...)
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

	c.pickerVisible = true
	c.pickerFirstRow = top + 3
	c.pickerCount = visibleCount
	c.pickerOffset = scroll
	c.pickerTotal = len(choices)
	c.pickerBoxLeft = left
	c.pickerBoxWidth = lipgloss.Width(box)

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
