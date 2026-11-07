package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// logoGlyphs spells OPTINE, one six-row glyph per letter. Each glyph's rows
// share one width, so joining them row by row keeps the art aligned.
var logoGlyphs = [][]string{
	{ // O
		" ██████╗ ",
		"██╔═══██╗",
		"██║   ██║",
		"██║   ██║",
		"╚██████╔╝",
		" ╚═════╝ ",
	},
	{ // P
		"██████╗ ",
		"██╔══██╗",
		"██████╔╝",
		"██╔═══╝ ",
		"██║     ",
		"╚═╝     ",
	},
	{ // T
		"████████╗",
		"╚══██╔══╝",
		"   ██║   ",
		"   ██║   ",
		"   ██║   ",
		"   ╚═╝   ",
	},
	{ // I
		"██╗",
		"██║",
		"██║",
		"██║",
		"██║",
		"╚═╝",
	},
	{ // N
		"███╗   ██╗",
		"████╗  ██║",
		"██╔██╗ ██║",
		"██║╚██╗██║",
		"██║ ╚████║",
		"╚═╝  ╚═══╝",
	},
	{ // E
		"███████╗",
		"██╔════╝",
		"█████╗  ",
		"██╔══╝  ",
		"███████╗",
		"╚══════╝",
	},
}

// logoRows is the joined art; every row has the same display width.
var logoRows = joinGlyphs(logoGlyphs)

// logoWidth is the display width of one logo row.
var logoWidth = lipgloss.Width(logoRows[0])

// logoColors shade the logo top to bottom, from the accent purple used by
// the rest of the interface to a lighter lavender.
var logoColors = []lipgloss.Color{"#6B4BEA", "#7D56F4", "#8E68F6", "#9F7BF8", "#B08EFA", "#C1A2FC"}

const tagline = "An agent of various features."

var (
	taglineStyle = lipgloss.NewStyle().Italic(true).Foreground(lipgloss.Color("245"))
	wordmark     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#7D56F4"))
)

func joinGlyphs(glyphs [][]string) []string {
	rows := make([]string, len(glyphs[0]))

	for i := range rows {
		var b strings.Builder
		for _, glyph := range glyphs {
			b.WriteString(glyph[i])
		}
		rows[i] = b.String()
	}

	return rows
}

// logoLines renders the colored logo, or a one-word wordmark when the
// terminal is too narrow for the art. width <= 0 means unknown, which gets
// the full logo.
func logoLines(width int) []string {
	if width > 0 && width < logoWidth {
		return []string{wordmark.Render(cutRunes("optine", width))}
	}

	lines := make([]string, len(logoRows))
	for i, row := range logoRows {
		lines[i] = lipgloss.NewStyle().Foreground(logoColors[i%len(logoColors)]).Render(row)
	}

	return lines
}

// centerLine centers an already styled line within width.
func centerLine(width int, line string) string {
	if width <= 0 {
		return line
	}

	pad := (width - lipgloss.Width(line)) / 2
	if pad <= 0 {
		return line
	}

	return strings.Repeat(" ", pad) + line
}
