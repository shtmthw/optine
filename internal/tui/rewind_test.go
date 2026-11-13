package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattthew/optine/internal/harnessSession"
)

// testTurns are three user messages, newest first, as /rewind lists them.
func testTurns() []harnessSession.UserTurn {
	return []harnessSession.UserTurn{
		{Number: 3, Index: 8, Content: "now write the docs"},
		{Number: 2, Index: 4, Content: "fix the failing\ntests   please"},
		{Number: 1, Index: 0, Content: "read the repo"},
	}
}

// Enter sends back the picked message's number, the key PickRewind maps to
// its turn.
func TestRewindPickerEnterSendsMessageNumber(t *testing.T) {
	result := make(chan string, 1)
	m := openTestPicker(t, newTestModel(), pickerOpenMsg{kind: pickerRewind, turns: testTurns(), result: result})
	m.picker.cursor = 1

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.picker.active() {
		t.Fatal("picker should close on enter")
	}
	if m.modelName != "test-model" {
		t.Fatalf("a rewind pick changed the model to %q", m.modelName)
	}

	select {
	case got := <-result:
		if got != "2" {
			t.Fatalf("selected = %q, want 2", got)
		}
	case <-time.After(time.Second):
		t.Fatal("result not sent on enter")
	}
}

// The search matches the message text (and its #number), not just the key,
// and keeps the turns aligned with the filtered rows.
func TestRewindPickerSearchesMessageText(t *testing.T) {
	m := openTestPicker(t, newTestModel(), pickerOpenMsg{kind: pickerRewind, turns: testTurns(), result: make(chan string, 1)})

	m = typeKey(m, "TESTS")
	if len(m.picker.choices) != 1 || m.picker.choices[0] != "2" {
		t.Fatalf("filtered choices = %v, want [2]", m.picker.choices)
	}
	if len(m.picker.turns) != 1 || m.picker.turns[0].Number != 2 {
		t.Fatalf("filtered turns = %+v", m.picker.turns)
	}

	m.setPickerQuery("#1")
	if len(m.picker.choices) != 1 || m.picker.choices[0] != "1" {
		t.Fatalf("search by number = %v, want [1]", m.picker.choices)
	}
}

func TestRewindPickerViewRenders(t *testing.T) {
	turns := append(testTurns(), harnessSession.UserTurn{Number: 4, Content: strings.Repeat("long ", 40)})
	m := openTestPicker(t, newTestModelAt(100, 24), pickerOpenMsg{kind: pickerRewind, turns: turns, result: make(chan string, 1)})

	out := plainText(m.View())
	for _, want := range []string{
		"Select a message to rewind to",
		"#3  now write the docs",
		"#2  fix the failing tests please", // one line, spaces collapsed
		"#1  read the repo",
		"…",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("rewind picker view missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, strings.Repeat("long ", 20)) {
		t.Fatalf("a long message was not cut:\n%s", out)
	}
	assertPickerCounter(t, pickerCounterLine(t, m.View()), "1/4")
}

// A swap that hands text back fills the input line, on one row, unless the
// user typed there in the meantime.
func TestAgentFinishedFillsEmptyInput(t *testing.T) {
	m := newTestModel()
	m.busy = true

	m, _ = update(m, agentFinishedMsg{swap: []Message{}, input: "fix the\ntests"})
	if m.input != "fix the tests" || m.cursor != runeLen("fix the tests") {
		t.Fatalf("input = %q cursor %d, want the message with the cursor at its end", m.input, m.cursor)
	}

	m.busy = true
	m.input, m.cursor = "typed", 5
	m, _ = update(m, agentFinishedMsg{input: "other"})
	if m.input != "typed" {
		t.Fatalf("input = %q, want what the user typed kept", m.input)
	}
}
