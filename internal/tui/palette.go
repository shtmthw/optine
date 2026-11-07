package tui

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattthew/optine/internal/slashCommands"
)

// commandPalette is the suggestion list shown while the user types a slash
// command. The agent interface and the launcher prompt share it; each keeps
// its own input line and asks the palette what to show and what Enter runs.
type commandPalette struct {
	commands []slashCommands.Command
	cursor   int
	// hidden is set by Esc and cleared by the next change to the input, so
	// the user can close the list without losing what they typed.
	hidden bool
}

// paletteMaxRows caps how many suggestions are on screen at once.
const paletteMaxRows = 8

const paletteHint = "↑/↓ select · tab complete · enter run · esc close"

var (
	paletteNameStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("141"))

	paletteDescStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("250"))

	paletteSelectedStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("231")).
				Background(lipgloss.Color("99"))

	hintStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241"))

	// The grey box for slash command output, the same grey as bash blocks.
	commandTitleStyle = editTitleBase.Background(lipgloss.Color("237"))
	commandBodyStyle  = editBodyBase.Background(lipgloss.Color("237"))
)

// matches returns the suggestions for input, or nil when the palette is
// closed or nothing matches.
func (p commandPalette) matches(input string) []slashCommands.Command {
	if p.hidden || len(p.commands) == 0 {
		return nil
	}

	return slashCommands.Match(p.commands, input)
}

// reset reopens the palette at the top; call it whenever the input changes.
func (p *commandPalette) reset() {
	p.cursor = 0
	p.hidden = false
}

// move shifts the highlight by delta, wrapping around n suggestions.
func (p *commandPalette) move(delta, n int) {
	if n <= 0 {
		p.cursor = 0
		return
	}

	p.cursor = ((p.cursor+delta)%n + n) % n
}

// highlighted returns the suggestion under the cursor.
func (p commandPalette) highlighted(input string) (slashCommands.Command, bool) {
	matches := p.matches(input)
	if len(matches) == 0 {
		return slashCommands.Command{}, false
	}

	return matches[clampInt(p.cursor, 0, len(matches)-1)], true
}

// choice is the command Enter runs: the typed text when it already spells a
// command and the user has not moved the highlight, else the highlighted
// suggestion.
func (p commandPalette) choice(input string) (slashCommands.Command, bool) {
	if p.cursor == 0 {
		if c, ok := slashCommands.Lookup(p.commands, input); ok {
			return c, true
		}
	}

	return p.highlighted(input)
}

// window returns the suggestions for input and the [start, start+rows) slice
// of them that fits in maxRows, scrolled so the cursor stays visible.
func (p commandPalette) window(input string, maxRows int) (matches []slashCommands.Command, start, rows int) {
	matches = p.matches(input)

	rows = min(len(matches), paletteMaxRows, maxRows)
	if rows <= 0 {
		return matches, 0, 0
	}

	cursor := clampInt(p.cursor, 0, len(matches)-1)
	if cursor >= rows {
		start = cursor - rows + 1
	}

	return matches, start, rows
}

// render draws up to maxRows suggestions plus a key hint, each line at most
// width cells. It returns nil when there is nothing to suggest.
func (p commandPalette) render(input string, width, maxRows int) []string {
	matches, start, rows := p.window(input, maxRows)
	if rows == 0 {
		return nil
	}

	nameWidth := 0
	for _, c := range matches {
		nameWidth = max(nameWidth, runeLen(c.Name))
	}

	cursor := clampInt(p.cursor, 0, len(matches)-1)
	lines := make([]string, 0, rows+1)

	for i := start; i < start+rows; i++ {
		lines = append(lines, renderPaletteRow(matches[i], nameWidth, width, i == cursor))
	}

	hint := paletteHint
	if len(matches) > rows {
		hint = strconv.Itoa(cursor+1) + "/" + strconv.Itoa(len(matches)) + " · " + hint
	}
	if width > 0 {
		hint = truncateRunes("  "+hint, width)
	} else {
		hint = "  " + hint
	}
	lines = append(lines, hintStyle.Render(hint))

	return lines
}

// renderPaletteRow draws one "  /name   description" row. The selected row is
// a full-width highlight bar.
func renderPaletteRow(c slashCommands.Command, nameWidth, width int, selected bool) string {
	name := c.Name + strings.Repeat(" ", max(0, nameWidth-runeLen(c.Name)))

	desc := c.Description
	if len(c.Aliases) > 0 {
		desc += " (" + strings.Join(c.Aliases, ", ") + ")"
	}

	const lead, gap = "  ", "   "
	plain := lead + name + gap + desc

	if width > 0 && runeLen(plain) > width {
		plain = truncateRunes(plain, width)
	}

	if selected {
		if width > 0 {
			plain += strings.Repeat(" ", max(0, width-runeLen(plain)))
		}
		return paletteSelectedStyle.Render(plain)
	}

	// Style the name and description separately on the (possibly
	// truncated) plain text so the colors never split a rune.
	runes := []rune(plain)
	split := min(len(runes), runeLen(lead+name))

	return paletteNameStyle.Render(string(runes[:split])) + paletteDescStyle.Render(string(runes[split:]))
}

// helpContent lists commands for the in-session /help message, one
// "name<TAB>description" line each.
func helpContent(commands []slashCommands.Command) string {
	lines := make([]string, 0, len(commands))

	for _, c := range commands {
		desc := c.Description
		if len(c.Aliases) > 0 {
			desc += " (" + strings.Join(c.Aliases, ", ") + ")"
		}
		lines = append(lines, c.Name+"\t"+desc)
	}

	return strings.Join(lines, "\n")
}

// renderHelp draws a /help message as an aligned two-column list in the
// grey command box.
func renderHelp(content string, width int) string {
	rows := strings.Split(content, "\n")

	nameWidth := 0
	for _, row := range rows {
		name, _, _ := strings.Cut(row, "\t")
		nameWidth = max(nameWidth, runeLen(name))
	}

	lines := make([]string, 0, len(rows)+1)
	for _, row := range rows {
		name, desc, _ := strings.Cut(row, "\t")
		if name == "" {
			continue
		}
		lines = append(lines, name+strings.Repeat(" ", nameWidth-runeLen(name))+"   "+desc)
	}
	lines = append(lines, "", "type / to pick one")

	return renderCommandBox("/help", lines, width)
}

// renderCommandBox draws a slash command's output as a grey box: the
// command as its title row, then what it printed.
func renderCommandBox(command string, lines []string, width int) string {
	return renderTitledPanel(command, lines, width, commandTitleStyle, commandBodyStyle)
}
