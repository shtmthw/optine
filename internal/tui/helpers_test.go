package tui

import (
	"os"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// TestMain pins the color profile so ANSI output does not depend on whether
// the test process happens to have a TTY attached.
func TestMain(m *testing.M) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	os.Exit(m.Run())
}

// newTestModelAt returns a test model sized like a real terminal frame.
func newTestModelAt(width, height int) Model {
	m := newTestModel()
	m, _ = update(m, tea.WindowSizeMsg{Width: width, Height: height})
	return m
}

// modelWithMessages replaces the model's history, keeping the rest of its state.
func modelWithMessages(m Model, messages ...Message) Model {
	m.messages = append([]Message(nil), messages...)
	return m
}

func escapeANSI(s string) string {
	return strings.ReplaceAll(s, "\x1b", "\\e")
}

var ansiSGR = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// plainText strips SGR styling so tests can assert on visible text while the
// package runs under a forced TrueColor profile.
func plainText(s string) string {
	return ansiSGR.ReplaceAllString(s, "")
}

// sampleMarkdown exercises every block construct the TUI renderer supports.
func sampleMarkdown() string {
	return strings.Join([]string{
		"# Heading with **bold**",
		"",
		"> a quote line with `code`",
		"> second quote line",
		"",
		"Paragraph with **bold**, *italic*, `inline code`, and a verylongwordthatmustbreakacrosscolumnsbecauseitislong.",
		"",
		"```go",
		"func main() {",
		"\tfmt.Println(\"hi\")",
		"}",
		"```",
		"",
		"| Name | Qty |",
		"| :--- | ---: |",
		"| apple | 3 |",
		"| pear | 12 |",
		"",
		"- list item one",
		"- list item two",
	}, "\n")
}

// approvalButtonRow returns the 0-based screen row of the first rendered
// approval button, or -1 when it is not on screen.
func approvalButtonRow(screen string) int {
	for i, line := range strings.Split(screen, "\n") {
		if strings.Contains(line, "[y] once") {
			return i
		}
	}
	return -1
}
