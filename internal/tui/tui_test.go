package tui

import (
	"bufio"
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// newTestModel builds a Model with a no-op agent and an empty approval reader,
// the shape every Update/View test starts from.
func newTestModel() Model {
	return New(
		"Ollama",
		"test-model",
		"/work",
		func(context.Context, string, string, string, *bufio.Reader) (string, error) {
			return "", nil
		},
		bufio.NewReader(strings.NewReader("")),
		make(chan string, 1),
	)
}

// typeKey presses each rune as an independent KeyMsg, mirroring real typing.
func typeKey(m Model, s string) Model {
	for _, r := range s {
		m, _ = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return m
}

// update drives Update with a concrete Model and returns the concrete type,
// since Update itself returns the tea.Model interface.
func update(m Model, msg tea.Msg) (Model, tea.Cmd) {
	next, cmd := m.Update(msg)
	typed, ok := next.(Model)
	if !ok {
		panic("Update returned a non-Model type")
	}
	return typed, cmd
}

// ---------------------------------------------------------------- plain helpers

func TestClampIntAndCutRunes(t *testing.T) {
	if clampInt(5, 0, 3) != 3 || clampInt(-1, 0, 3) != 0 || clampInt(2, 0, 3) != 2 {
		t.Fatal("clampInt is wrong")
	}
	if cutRunes("héllo", 3) != "hél" {
		t.Fatalf("cutRunes counted bytes, got %q", cutRunes("héllo", 3))
	}
	if cutRunes("hi", 5) != "hi" {
		t.Fatal("cutRunes should pass through short strings")
	}
}

func TestPromptSafeAndDeleteLastWord(t *testing.T) {
	if got := string(promptSafe([]rune("a\nb\tc\rd"))); got != "a b c d" {
		t.Fatalf("promptSafe = %q", got)
	}
	if got := deleteLastWord("foo bar"); got != "foo " {
		t.Fatalf("deleteLastWord = %q", got)
	}
	if got := deleteLastWord("single"); got != "" {
		t.Fatalf("deleteLastWord single word = %q", got)
	}
}

func TestLastAgentMessage(t *testing.T) {
	messages := []Message{
		{Role: roleUser, Content: "hi"},
		{Role: roleAgent, Content: "first"},
		{Role: roleEvent, Content: "ev"},
		{Role: roleAgent, Content: "second"},
	}
	if got := lastAgentMessage(messages); got != "second" {
		t.Fatalf("lastAgentMessage = %q", got)
	}
	if got := lastAgentMessage([]Message{{Role: roleUser}}); got != "" {
		t.Fatalf("no agent message should be empty, got %q", got)
	}
}

func TestPageSize(t *testing.T) {
	if got := (Model{height: 20}).pageSize(); got != 10 {
		t.Fatalf("pageSize = %d, want 10", got)
	}
	if got := (Model{height: 0}).pageSize(); got != 1 {
		t.Fatalf("pageSize floor = %d, want 1", got)
	}
}

// ---------------------------------------------------------------- typing / input

func TestTypeAndCursorMovement(t *testing.T) {
	m := typeKey(newTestModel(), "abc")
	if m.input != "abc" || m.cursor != 3 {
		t.Fatalf("after typing: input=%q cursor=%d", m.input, m.cursor)
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyLeft})
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyLeft})
	if m.cursor != 1 {
		t.Fatalf("cursor after two lefts = %d, want 1", m.cursor)
	}

	m = typeKey(m, "X") // insert at cursor
	if m.input != "aXbc" {
		t.Fatalf("insert at cursor = %q, want aXbc", m.input)
	}
}

func TestBackspaceDeleteAndWordOps(t *testing.T) {
	m := typeKey(newTestModel(), "hello world")

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlW})
	if m.input != "hello " {
		t.Fatalf("ctrl+w = %q, want 'hello '", m.input)
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyCtrlU})
	if m.input != "" || m.cursor != 0 {
		t.Fatalf("ctrl+u should clear, got %q", m.input)
	}

	m = typeKey(newTestModel(), "abc")
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyBackspace})
	if m.input != "ab" {
		t.Fatalf("backspace = %q", m.input)
	}

	// delete at the end of the line is a no-op
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDelete})
	if m.input != "ab" {
		t.Fatalf("delete at end changed input: %q", m.input)
	}
}

// ---------------------------------------------------------------- submit / agent

// Submitting non-empty input appends a user message, clears the prompt, marks
// the model busy and returns a command (the agent run).
func TestSubmitStartsRun(t *testing.T) {
	m := typeKey(newTestModel(), "do the thing")
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})

	if !m.busy {
		t.Fatal("submit should mark the model busy")
	}
	if m.input != "" {
		t.Fatalf("input should clear after submit, got %q", m.input)
	}
	if len(m.messages) != 1 || m.messages[0].Role != roleUser || m.messages[0].Content != "do the thing" {
		t.Fatalf("user message not recorded: %+v", m.messages)
	}
	if cmd == nil {
		t.Fatal("submit should return a command")
	}
}

// Empty input and input while busy are both ignored.
func TestSubmitIgnoresEmptyAndBusy(t *testing.T) {
	m := newTestModel()
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.busy || cmd != nil || len(m.messages) != 0 {
		t.Fatal("empty submit should do nothing")
	}

	m.busy = true
	m.input = "second"
	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if len(m.messages) != 0 {
		t.Fatal("submit while busy should be ignored")
	}
}

// agentFinishedMsg renders the reply into history and clears busy.
func TestAgentFinishedAppendsReply(t *testing.T) {
	m := newTestModel()
	m.busy = true
	m, _ = update(m, agentFinishedMsg{response: "the answer"})

	if m.busy {
		t.Fatal("busy should clear when the agent finishes")
	}
	if len(m.messages) != 1 || m.messages[0].Role != roleAgent || m.messages[0].Content != "the answer" {
		t.Fatalf("reply not appended: %+v", m.messages)
	}
}

// An error result becomes an error-role message; an empty reply adds nothing.
func TestAgentFinishedErrorAndEmpty(t *testing.T) {
	m := newTestModel()
	m.busy = true
	m, _ = update(m, agentFinishedMsg{err: context.DeadlineExceeded})
	if len(m.messages) != 1 || m.messages[0].Role != roleError {
		t.Fatalf("error not surfaced: %+v", m.messages)
	}

	m, _ = update(m, agentFinishedMsg{response: "   "})
	if len(m.messages) != 1 {
		t.Fatalf("blank reply should add no message, got %d", len(m.messages))
	}
}

// logLineMsg becomes an event-role message.
func TestLogLineBecomesEvent(t *testing.T) {
	m, _ := update(newTestModel(), logLineMsg{text: "tool ran"})
	if len(m.messages) != 1 || m.messages[0].Role != roleEvent || m.messages[0].Content != "tool ran" {
		t.Fatalf("log line not stored as event: %+v", m.messages)
	}
}

func TestSessionChangedMsgUpdatesUID(t *testing.T) {
	m, _ := update(newTestModel(), sessionChangedMsg{uid: "20260101T000000Z-abcd1234"})
	if m.sessionUID != "20260101T000000Z-abcd1234" {
		t.Fatalf("session UID not set: %q", m.sessionUID)
	}
}

// ---------------------------------------------------------------- approvals

// An approval request flags the prompt and resets scroll so it is visible.
func TestApprovalRequestedShowsPrompt(t *testing.T) {
	m := newTestModel()
	m.scroll = 5
	m, _ = update(m, approvalRequestedMsg{})
	if !m.pendingApproval || m.scroll != 0 {
		t.Fatalf("approval request: pending=%v scroll=%d", m.pendingApproval, m.scroll)
	}
}

// While an approval is pending, typed text is the answer, not a chat message:
// Enter forwards it to the approval channel.
func TestSubmitAnswersApproval(t *testing.T) {
	ch := make(chan string, 1)
	m := New("Ollama", "m", "/w",
		func(context.Context, string, string, string, *bufio.Reader) (string, error) { return "", nil },
		bufio.NewReader(strings.NewReader("")), ch)
	m.busy = true
	m.pendingApproval = true
	m = typeKey(m, "y")

	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.pendingApproval {
		t.Fatal("answering should clear the pending prompt")
	}
	if len(m.messages) != 0 {
		t.Fatal("an approval answer must not become a chat message")
	}
	if cmd == nil {
		t.Fatal("answering should return a command")
	}

	// Run the command; it should push the answer onto the channel.
	if msg := cmd(); msg != nil {
		t.Fatalf("approval command should return nil, got %v", msg)
	}
	select {
	case got := <-ch:
		if got != "y" {
			t.Fatalf("approval answer = %q, want y", got)
		}
	case <-time.After(time.Second):
		t.Fatal("approval answer was never sent")
	}
}

// Option hit-testing matches only the row and columns of a button.
func TestOptionAtHitTesting(t *testing.T) {
	clicks := &clickTargets{
		visible: true,
		row:     2,
		opts: []approvalOption{
			{start: 0, end: 8, answer: "y"},
			{start: 10, end: 20, answer: "a"},
		},
	}

	if got := optionAt(clicks, 3, 2); got != "y" {
		t.Fatalf("hit y = %q", got)
	}
	if got := optionAt(clicks, 15, 2); got != "a" {
		t.Fatalf("hit a = %q", got)
	}
	if got := optionAt(clicks, 9, 2); got != "" { // gap between buttons
		t.Fatalf("gap should miss, got %q", got)
	}
	if got := optionAt(clicks, 3, 3); got != "" { // wrong row
		t.Fatalf("wrong row should miss, got %q", got)
	}

	clicks.visible = false
	if got := optionAt(clicks, 3, 2); got != "" {
		t.Fatalf("hidden buttons should miss, got %q", got)
	}
}

// Clicking a button answers the approval; clicking elsewhere does nothing.
func TestMouseClickAnswersApproval(t *testing.T) {
	ch := make(chan string, 1)
	m := New("Ollama", "m", "/w",
		func(context.Context, string, string, string, *bufio.Reader) (string, error) { return "", nil },
		bufio.NewReader(strings.NewReader("")), ch)
	m.busy = true
	m.pendingApproval = true
	m.clicks.opts = []approvalOption{{start: 0, end: 8, answer: "a"}}
	m.clicks.row = 1
	m.clicks.visible = true

	_, cmd := update(m, tea.MouseMsg{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
		X:      3, Y: 1,
	})
	if cmd == nil {
		t.Fatal("clicking an approval button should answer")
	}
	cmd()
	if got := <-ch; got != "a" {
		t.Fatalf("clicked answer = %q, want a", got)
	}
}

// ---------------------------------------------------------------- view / wire-format

// View includes the title, provider/model/session meta and the prompt, and is
// safe at width 0 before the first WindowSizeMsg.
func TestViewRendersMetaAndPrompt(t *testing.T) {
	m := newTestModel()
	m.sessionUID = "SESS-1"
	m, _ = update(m, tea.WindowSizeMsg{Width: 80, Height: 24})

	out := m.View()
	for _, want := range []string{"Optine", "Ollama", "test-model", "SESS-1", "Directory: /work", ">"} {
		if !strings.Contains(out, want) {
			t.Fatalf("View missing %q:\n%s", want, out)
		}
	}
}

// A very narrow terminal must not panic (status/title truncation paths).
func TestViewNarrowTerminal(t *testing.T) {
	m := newTestModel()
	m.sessionUID = "SESS-1"
	m, _ = update(m, tea.WindowSizeMsg{Width: 3, Height: 3})
	_ = m.View()
}

// The prompt windows around the cursor so "> " always starts at column 0.
func TestRenderPromptWindowsAroundCursor(t *testing.T) {
	m := newTestModel()
	m.width = 10
	m.input = "0123456789abcdef"
	m.cursor = len([]rune(m.input))

	got := m.renderPrompt()
	if !strings.HasPrefix(got, "> ") {
		t.Fatalf("prompt must start with the marker: %q", got)
	}
	if runeLen(got) > m.width {
		t.Fatalf("prompt exceeds width: %q", got)
	}
}

// The busy spinner renders only while the agent is running.
func TestViewShowsSpinnerWhenBusy(t *testing.T) {
	m := newTestModel()
	m, _ = update(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	if strings.Contains(m.View(), "thinking...") {
		t.Fatal("idle model should not show the spinner")
	}
	m.busy = true
	if !strings.Contains(m.View(), "thinking...") {
		t.Fatal("busy model should show the spinner")
	}
}
