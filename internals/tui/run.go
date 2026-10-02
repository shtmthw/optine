package tui

import (
	"bufio"
	"io"
	"log"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// Run launches the Bubble Tea front-end for the agent interface.
//
// While the TUI is up, everything the agent harness used to print through
// log.Println (provider replies, tool call requests, approval prompts, ...)
// is forwarded into the TUI, and approval answers typed by the user are
// handed back to the harness through the shared reader.
func Run(provider string, modelName string, agent AgentFunc) error {
	approvalCh := make(chan string)
	reader := bufio.NewReader(newLineSource(approvalCh))

	program := tea.NewProgram(
		New(provider, modelName, agent, reader, approvalCh),
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)

	// Mirror the old stdout/log behaviour inside the TUI without touching
	// the harness loop itself.
	oldOutput := log.Writer()
	oldFlags := log.Flags()
	log.SetOutput(&tuiLogSink{p: program})
	log.SetFlags(0)

	defer func() {
		log.SetOutput(oldOutput)
		log.SetFlags(oldFlags)
	}()

	_, err := program.Run()
	return err
}

// tuiLogSink is an io.Writer that pushes log output into the Bubble Tea
// program as messages instead of writing to stdout.
type tuiLogSink struct {
	p *tea.Program
}

func (s *tuiLogSink) Write(b []byte) (int, error) {
	text := strings.TrimRight(string(b), "\n")

	if text != "" {
		s.p.Send(logLineMsg{text: text})
	}

	if strings.Contains(string(b), "Allow?") {
		s.p.Send(approvalRequestedMsg{})
	}

	return len(b), nil
}

// lineSource adapts a channel of user-typed lines into an io.Reader, so the
// harness' bufio.Reader.ReadString calls can be answered from the TUI.
type lineSource struct {
	ch   <-chan string
	rest []byte
}

func newLineSource(ch <-chan string) *lineSource {
	return &lineSource{ch: ch}
}

func (l *lineSource) Read(p []byte) (int, error) {
	if len(l.rest) == 0 {
		line, ok := <-l.ch
		if !ok {
			return 0, io.EOF
		}

		if !strings.HasSuffix(line, "\n") {
			line += "\n"
		}

		l.rest = []byte(line)
	}

	n := copy(p, l.rest)
	l.rest = l.rest[n:]

	return n, nil
}
