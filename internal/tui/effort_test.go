package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// testEffortOpen is the /effort picker as cli opens it: the current level
// first, then default and the model's levels.
func testEffortOpen(result chan string) pickerOpenMsg {
	return pickerOpenMsg{
		kind:    pickerEffort,
		choices: []string{"medium", "default", "off", "low", "high"},
		labels:  []string{"medium", "default (high)", "off (turns thinking off)", "low", "high"},
		current: "medium",
		result:  result,
	}
}

func TestEffortPickerEnterSendsTheLevel(t *testing.T) {
	result := make(chan string, 1)
	m := openTestPicker(t, newTestModel(), testEffortOpen(result))
	m.picker.cursor = 4

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.picker.active() {
		t.Fatal("picker should close on enter")
	}
	if m.modelName != "test-model" {
		t.Fatalf("an effort pick changed the model to %q", m.modelName)
	}

	select {
	case got := <-result:
		if got != "high" {
			t.Fatalf("selected = %q, want high", got)
		}
	case <-time.After(time.Second):
		t.Fatal("result not sent on enter")
	}
}

// The search matches the row text, so "current" finds the current level and
// the labels stay aligned with the filtered levels.
func TestEffortPickerSearchesLabels(t *testing.T) {
	m := openTestPicker(t, newTestModel(), testEffortOpen(make(chan string, 1)))

	m = typeKey(m, "current")
	if len(m.picker.choices) != 1 || m.picker.choices[0] != "medium" || m.picker.labels[0] != "medium" {
		t.Fatalf("filtered = %v / %v, want the current level", m.picker.choices, m.picker.labels)
	}

	m.setPickerQuery("high")
	if len(m.picker.choices) != 2 || m.picker.choices[0] != "default" || m.picker.choices[1] != "high" {
		t.Fatalf("search high = %v, want default (high) and high", m.picker.choices)
	}
}

func TestEffortPickerViewRenders(t *testing.T) {
	m := openTestPicker(t, newTestModelAt(80, 24), testEffortOpen(make(chan string, 1)))

	out := plainText(m.View())
	for _, want := range []string{"Select an effort level", "medium <current>", "default (high)", "off (turns thinking off)", "low"} {
		if !strings.Contains(out, want) {
			t.Fatalf("effort picker view missing %q:\n%s", want, out)
		}
	}
	if strings.Count(out, "<current>") != 1 {
		t.Fatalf("exactly one row should be marked <current>:\n%s", out)
	}
	assertPickerCounter(t, pickerCounterLine(t, m.View()), "1/5")
}

// <current> is purple on a row that is not selected, and takes the selected
// row's own colors when the cursor is on it.
func TestEffortPickerCurrentMarkIsPurple(t *testing.T) {
	m := openTestPicker(t, newTestModelAt(80, 24), testEffortOpen(make(chan string, 1)))
	purple := pickerCurrentStyle.Render(pickerCurrentMark)

	if strings.Contains(m.View(), purple) {
		t.Fatal("the selected row's <current> should not be drawn purple on the purple highlight")
	}

	m, _ = update(m, tea.KeyMsg{Type: tea.KeyDown})
	if !strings.Contains(m.View(), purple) {
		t.Fatal("<current> should be purple once the cursor leaves its row")
	}
}

// The header shows a set level beside the model and nothing at default.
func TestHeaderShowsTheEffortLevel(t *testing.T) {
	m := newTestModelAt(120, 24)

	m, _ = update(m, effortChangedMsg{level: "high"})
	if out := plainText(m.View()); !strings.Contains(out, "test-model · effort high") {
		t.Fatalf("header lacks the level:\n%s", out)
	}

	m, _ = update(m, effortChangedMsg{level: "default"})
	if out := plainText(m.View()); strings.Contains(out, "effort") {
		t.Fatalf("header shows an effort at default:\n%s", out)
	}

	if modelWithEffort("", "high") != "" || modelWithEffort("m", "") != "m" {
		t.Fatal("modelWithEffort adds a level only to a named model")
	}
}
