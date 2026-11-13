package cli

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/mattthew/optine/internal/harnessSession"
	"github.com/mattthew/optine/internal/tui"
)

// stubEffortUI replaces the picker and the header notice. pick is the level
// the picker answers with ("" cancels); the returned slices record what the
// picker was shown and what the header was told.
func stubEffortUI(t *testing.T, pick string) (shown *[]tui.EffortChoice, header *[]string) {
	t.Helper()

	shown, header = new([]tui.EffortChoice), new([]string)

	oldPick, oldNotify := pickEffort, notifyEffortChanged
	pickEffort = func(choices []tui.EffortChoice) (string, bool) {
		*shown = choices
		return pick, pick != ""
	}
	notifyEffortChanged = func(level string) { *header = append(*header, level) }
	t.Cleanup(func() { pickEffort, notifyEffortChanged = oldPick, oldNotify })

	return shown, header
}

func effortSession(levels []harnessSession.Effort, def harnessSession.Effort) *harnessSession.Session {
	sess := harnessSession.New(harnessSession.Config{Provider: "Ollama", Model: "qwen3:8b", Kind: harnessSession.OllamaNative})
	sess.EffortSupport = harnessSession.NewEffortSupport(levels, def)
	return sess
}

var lowMedHigh = []harnessSession.Effort{harnessSession.EffortLow, harnessSession.EffortMedium, harnessSession.EffortHigh}

// The picker lists the current level first, marked, then default (naming the
// model's own) and the levels lowest first; the pick is kept and shown.
func TestEffortPickerSetsTheLevel(t *testing.T) {
	shown, header := stubEffortUI(t, "high")
	sess := effortSession(lowMedHigh, harnessSession.EffortMedium)
	sess.Effort = harnessSession.EffortLow

	lines := runEffort(sess, "")

	want := []tui.EffortChoice{
		{Level: "low", Label: "low", Current: true},
		{Level: "default", Label: "default (medium)"},
		{Level: "medium", Label: "medium"},
		{Level: "high", Label: "high"},
	}
	if !slices.Equal(*shown, want) {
		t.Fatalf("picker rows = %+v, want %+v", *shown, want)
	}
	if sess.Effort != harnessSession.EffortHigh {
		t.Fatalf("effort = %q, want high", sess.Effort)
	}
	if !slices.Equal(*header, []string{"high"}) {
		t.Fatalf("header told %v, want [high]", *header)
	}
	if len(lines) != 1 || lines[0] != "Effort set to high for qwen3:8b" {
		t.Fatalf("lines = %q", lines)
	}
}

// Off says in the picker and in the output box that it turns thinking off.
func TestEffortOffSaysWhatItDoes(t *testing.T) {
	shown, _ := stubEffortUI(t, "off")
	sess := effortSession([]harnessSession.Effort{harnessSession.EffortOff, harnessSession.EffortOn}, harnessSession.EffortDefault)

	lines := runEffort(sess, "")

	want := []tui.EffortChoice{
		{Level: "default", Label: "default", Current: true},
		{Level: "off", Label: "off (turns thinking off)"},
		{Level: "on", Label: "on"},
	}
	if !slices.Equal(*shown, want) {
		t.Fatalf("picker rows = %+v, want %+v", *shown, want)
	}
	if sess.Effort != harnessSession.EffortOff || len(lines) != 1 || lines[0] != "Effort set to off for qwen3:8b; thinking is turned off" {
		t.Fatalf("effort %q, lines %q", sess.Effort, lines)
	}
}

func TestEffortPickerCancelKeepsTheLevel(t *testing.T) {
	_, header := stubEffortUI(t, "")
	sess := effortSession(lowMedHigh, harnessSession.EffortDefault)
	sess.Effort = harnessSession.EffortMedium

	if lines := runEffort(sess, ""); lines != nil {
		t.Fatalf("a cancelled pick printed %q", lines)
	}
	if sess.Effort != harnessSession.EffortMedium || len(*header) != 0 {
		t.Fatalf("a cancelled pick changed the level to %q (header %v)", sess.Effort, *header)
	}
}

func TestEffortArgumentSetsTheLevelDirectly(t *testing.T) {
	shown, _ := stubEffortUI(t, "low")
	sess := effortSession(lowMedHigh, harnessSession.EffortDefault)

	runEffort(sess, " HIGH")
	if sess.Effort != harnessSession.EffortHigh || *shown != nil {
		t.Fatalf("/effort high: effort %q, picker shown %v", sess.Effort, *shown)
	}

	lines := runEffort(sess, "default")
	if sess.Effort != harnessSession.EffortDefault || !strings.Contains(lines[0], "the model decides") {
		t.Fatalf("/effort default: effort %q, lines %q", sess.Effort, lines)
	}

	for _, arg := range []string{"ultra", "off", "xhigh"} {
		lines := runEffort(sess, arg)
		if sess.Effort != harnessSession.EffortDefault {
			t.Fatalf("/effort %s changed the level to %q", arg, sess.Effort)
		}
		if len(lines) != 1 || !strings.Contains(lines[0], "default, low, medium, high") {
			t.Fatalf("/effort %s: lines %q, want the accepted levels", arg, lines)
		}
	}
}

func TestEffortOnAModelWithoutControl(t *testing.T) {
	shown, _ := stubEffortUI(t, "high")
	sess := effortSession(nil, harnessSession.EffortDefault)
	sess.Effort = harnessSession.EffortHigh
	sess.EffortSupport.Note = "start vLLM with --reasoning-parser to use effort levels"

	lines := runEffort(sess, "")

	if *shown != nil {
		t.Fatal("no picker should open on a model with no effort setting")
	}
	want := []string{
		"qwen3:8b has no effort setting",
		"start vLLM with --reasoning-parser to use effort levels",
		"the saved level high comes back on a model that has one",
	}
	if !slices.Equal(lines, want) {
		t.Fatalf("lines = %q, want %q", lines, want)
	}
	if sess.Effort != harnessSession.EffortHigh {
		t.Fatal("the saved level must be kept")
	}
}

// Switching models keeps the level; a model that does not take it gets the
// notice, and a failed lookup counts as no control.
func TestRefreshEffortSupportKeepsTheLevel(t *testing.T) {
	old := effortSupportFor
	t.Cleanup(func() { effortSupportFor = old })
	_, header := stubEffortUI(t, "")

	sess := effortSession(lowMedHigh, harnessSession.EffortDefault)
	sess.Effort = harnessSession.EffortHigh
	sess.Model = "gpt-4o-mini"

	effortSupportFor = func(context.Context, string, string) (harnessSession.EffortSupport, error) {
		return harnessSession.EffortSupport{}, nil
	}
	if notice := refreshEffortSupport(context.Background(), sess); notice != "high is not available on gpt-4o-mini; using the model default" {
		t.Fatalf("notice = %q", notice)
	}
	if sess.Effort != harnessSession.EffortHigh {
		t.Fatal("switching models must keep the level")
	}
	if last := (*header)[len(*header)-1]; last != "default" {
		t.Fatalf("header shows %q for a level that is not sent, want default", last)
	}

	effortSupportFor = func(context.Context, string, string) (harnessSession.EffortSupport, error) {
		return harnessSession.NewEffortSupport(lowMedHigh, harnessSession.EffortDefault), nil
	}
	if notice := refreshEffortSupport(context.Background(), sess); notice != "" || !sess.EffortSupport.Accepts(harnessSession.EffortHigh) {
		t.Fatalf("a model that takes the level: notice %q, support %+v", notice, sess.EffortSupport)
	}
	if last := (*header)[len(*header)-1]; last != "high" {
		t.Fatalf("header shows %q, want high back on a model that takes it", last)
	}

	effortSupportFor = func(context.Context, string, string) (harnessSession.EffortSupport, error) {
		return harnessSession.NewEffortSupport(lowMedHigh, harnessSession.EffortDefault), errors.New("down")
	}
	refreshEffortSupport(context.Background(), sess)
	if sess.EffortSupport.Supported() {
		t.Fatal("a failed lookup must leave no effort control")
	}

	sess.Effort = harnessSession.EffortDefault
	if notice := refreshEffortSupport(context.Background(), sess); notice != "" {
		t.Fatalf("default never needs a notice, got %q", notice)
	}
}
