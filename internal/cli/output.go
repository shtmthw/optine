package cli

import (
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/term"
	"github.com/mattthew/optine/internal/slashCommands"
)

// Launcher output goes to stderr with a status mark in front. While the
// agent interface is up, the same messages go through log instead, which
// the TUI turns into event lines; styling them there would show raw escape
// codes.

var stderrRenderer = lipgloss.NewRenderer(os.Stderr)

var (
	okMark   = stderrRenderer.NewStyle().Bold(true).Foreground(lipgloss.Color("42")).Render("✓")
	warnMark = stderrRenderer.NewStyle().Bold(true).Foreground(lipgloss.Color("11")).Render("!")
	failMark = stderrRenderer.NewStyle().Bold(true).Foreground(lipgloss.Color("196")).Render("✗")
	infoMark = stderrRenderer.NewStyle().Foreground(lipgloss.Color("99")).Render("•")

	cmdNameStyle = stderrRenderer.NewStyle().Bold(true).Foreground(lipgloss.Color("141"))
	faintStyle   = stderrRenderer.NewStyle().Foreground(lipgloss.Color("245"))
)

// printer writes user-facing status lines for the launcher, or for a
// command typed inside the agent interface when inSession is true.
type printer struct {
	inSession bool
}

var launcherOut = printer{}

func (p printer) line(mark, format string, args ...any) {
	text := fmt.Sprintf(format, args...)

	if p.inSession {
		log.Println(text)
		return
	}

	fmt.Fprintln(os.Stderr, mark+" "+text)
}

func (p printer) info(format string, args ...any) { p.line(infoMark, format, args...) }
func (p printer) ok(format string, args ...any)   { p.line(okMark, format, args...) }
func (p printer) warn(format string, args ...any) { p.line(warnMark, format, args...) }
func (p printer) fail(format string, args ...any) { p.line(failMark, format, args...) }

// help lists the commands for the launcher, or tells a session user where
// to find theirs (the agent interface answers /help itself).
func (p printer) help() {
	if p.inSession {
		log.Println("type / to see the commands for this session")
		return
	}

	commands := slashCommands.For(slashCommands.Launcher)

	width := 0
	for _, c := range commands {
		width = max(width, len(c.Name))
	}

	var b strings.Builder
	b.WriteString("Commands\n")
	for _, c := range commands {
		desc := c.Description
		if len(c.Aliases) > 0 {
			desc += " (" + strings.Join(c.Aliases, ", ") + ")"
		}
		fmt.Fprintf(&b, "  %s%s  %s\n", cmdNameStyle.Render(c.Name), strings.Repeat(" ", width-len(c.Name)), faintStyle.Render(desc))
	}
	b.WriteString(faintStyle.Render("Inside a session, type / to see that session's commands.") + "\n")

	fmt.Fprint(os.Stderr, b.String())
}

// interactive reports whether the launcher can run its Bubble Tea widgets:
// both stdin and stdout must be terminals. It is a variable so tests can
// force the plain line-based fallback.
var interactive = func() bool {
	return term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stdout.Fd())
}

// terminalWidth returns the stdout width, or 0 when it is unknown.
func terminalWidth() int {
	w, _, err := term.GetSize(os.Stdout.Fd())
	if err != nil || w <= 0 {
		return 0
	}

	return w
}
