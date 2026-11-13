package harnessSession

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestParseEffort(t *testing.T) {
	for input, want := range map[string]Effort{
		"":         EffortDefault,
		"default":  EffortDefault,
		" HIGH ":   EffortHigh,
		"off":      EffortOff,
		"on":       EffortOn,
		"minimal":  EffortMinimal,
		"xhigh":    EffortXHigh,
		"max":      EffortMax,
		"medium":   EffortMedium,
		"low":      EffortLow,
		"Default ": EffortDefault,
	} {
		got, ok := ParseEffort(input)
		if !ok || got != want {
			t.Fatalf("ParseEffort(%q) = %q, %v; want %q", input, got, ok, want)
		}
	}

	for _, input := range []string{"none", "ultra", "hi"} {
		if _, ok := ParseEffort(input); ok {
			t.Fatalf("ParseEffort(%q) accepted an unknown level", input)
		}
	}

	if EffortDefault.String() != "default" || EffortHigh.String() != "high" {
		t.Fatal("levels print as the user types them")
	}
}

// Levels are kept lowest first with duplicates and unknown ones dropped, and a
// default the model does not list is forgotten.
func TestNewEffortSupportOrdersLevels(t *testing.T) {
	s := NewEffortSupport([]Effort{EffortHigh, "bogus", EffortLow, EffortOff, EffortHigh}, EffortMedium)

	if want := []Effort{EffortOff, EffortLow, EffortHigh}; !slices.Equal(s.Levels, want) {
		t.Fatalf("levels = %v, want %v", s.Levels, want)
	}
	if s.Default != EffortDefault {
		t.Fatalf("default = %q, want none (medium is not offered)", s.Default)
	}
	if !s.Supported() || !s.Accepts(EffortDefault) || !s.Accepts(EffortLow) || s.Accepts(EffortMedium) {
		t.Fatalf("Accepts wrong for %+v", s)
	}

	var none EffortSupport
	if none.Supported() || !none.Accepts(EffortDefault) || none.Accepts(EffortHigh) {
		t.Fatal("a model with no control only accepts default")
	}
}

func TestEffortSurvivesCommitAndLoad(t *testing.T) {
	stubSessionsDir(t)

	sess := New(Config{Provider: "Ollama", Model: "m", Kind: OllamaNative, Effort: EffortHigh})
	if sess.Effort != EffortHigh {
		t.Fatalf("New ignored Config.Effort: %q", sess.Effort)
	}
	sess.EffortSupport = NewEffortSupport([]Effort{EffortHigh}, EffortDefault)
	sess.RunCount = 1

	if err := Commit(sess); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(sess.UID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Effort != EffortHigh {
		t.Fatalf("loaded effort = %q, want high", loaded.Effort)
	}
	if loaded.EffortSupport.Supported() {
		t.Fatal("effort support belongs to the model and must not be saved")
	}
}

// Sessions saved before /effort, or edited to hold a level this build does
// not know, load at default.
func TestOldSessionLoadsAtDefaultEffort(t *testing.T) {
	dir := stubSessionsDir(t)

	for uid, body := range map[string]string{
		"old":     `{"uid":"old","kind":"ollama-native","provider":"Ollama","model":"m","run_count":1,"total_user_messages":0}`,
		"unknown": `{"uid":"unknown","kind":"ollama-native","provider":"Ollama","model":"m","run_count":1,"total_user_messages":0,"effort":"ultra"}`,
	} {
		if err := os.WriteFile(filepath.Join(dir, uid+".json"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}

		loaded, err := Load(uid)
		if err != nil {
			t.Fatal(err)
		}
		if loaded.Effort != EffortDefault {
			t.Fatalf("%s session loaded effort %q, want default", uid, loaded.Effort)
		}
	}
}
