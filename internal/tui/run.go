package tui

import (
	"bufio"
	"io"
	"log"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattthew/optine/internal/approval"
)

// Run launches the Bubble Tea front-end for the agent interface.
//
// While the TUI is up, everything the agent harness used to print through
// log.Println (provider replies, tool call requests, approval prompts, ...)
// is forwarded into the TUI, and approval answers typed by the user are
// handed back to the harness through the shared reader.
func Run(provider string, modelName string, agent AgentFunc) error {
	approvalCh := make(chan string)

	// done is closed when the TUI exits, so an approval prompt that is
	// still blocked in the harness' ReadString sees EOF and fails closed
	// instead of hanging the agent goroutine forever. Nothing is ever sent
	// on it: closing it can never race a sender the way closing approvalCh
	// (which sendApproval still pushes to) would.
	doneCh := make(chan struct{})
	reader := bufio.NewReader(newLineSource(approvalCh, doneCh))

	program := tea.NewProgram(
		New(provider, modelName, agent, reader, approvalCh),
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)

	// The harness asks for a human answer; route that to the program. This
	// replaces an old substring match on the log text ("Allow?"), which
	// welded three packages to one string inside a prompt message.
	approval.Set(approvalListener{p: program})

	// Mirror the old stdout/log behaviour inside the TUI without touching
	// the harness loop itself.
	oldOutput := log.Writer()
	oldFlags := log.Flags()
	log.SetOutput(&tuiLogSink{p: program})
	log.SetFlags(0)

	defer func() {
		log.SetOutput(oldOutput)
		log.SetFlags(oldFlags)
		close(doneCh)
		approval.Set(nil)
	}()

	_, err := program.Run()
	return err
}

// approvalListener is the TUI's side of internal/approval.
type approvalListener struct {
	p *tea.Program
}

func (a approvalListener) ApprovalRequested() {
	a.p.Send(approvalRequestedMsg{})
}

// tuiLogSink forwards log lines into the Bubble Tea program as messages
// instead of writing to stdout. It is only a display bridge: the approval
// signal travels through internal/approval, not through this text.
type tuiLogSink struct {
	p *tea.Program
}

func (s *tuiLogSink) Write(b []byte) (int, error) {
	text := strings.TrimRight(string(b), "\n")

	if text != "" {
		s.p.Send(logLineMsg{text: text})
	}

	return len(b), nil
}

// lineSource adapts a channel of user-typed lines into an io.Reader, so the
// harness' bufio.Reader.ReadString calls can be answered from the TUI. The
// done channel is closed on TUI exit; a read then returns EOF rather than
// waiting for an answer that can never arrive.
type lineSource struct {
	ch   <-chan string
	done <-chan struct{}
	rest []byte
}

func newLineSource(ch <-chan string, done <-chan struct{}) *lineSource {
	return &lineSource{ch: ch, done: done}
}

func (l *lineSource) Read(p []byte) (int, error) {
	if len(l.rest) == 0 {
		var line string
		var ok bool

		select {
		case line, ok = <-l.ch:
			if !ok {
				return 0, io.EOF
			}

		case <-l.done:
			// The TUI is gone; nobody will answer, so fail closed.
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
