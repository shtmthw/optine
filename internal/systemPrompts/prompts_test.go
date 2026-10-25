package systemPrompts

import (
	"strings"
	"testing"
	"time"
)

// A fmt verb/argument mismatch renders as "%!d(MISSING)" or similar. Rendering
// both prompts and failing on that marker catches a broken Sprintf call that
// would otherwise ship silently to the model.
func TestPromptsRenderWithoutVerbMismatch(t *testing.T) {
	now := time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)

	native := NativeToolSystemPrompt(now, []string{"tavily", "searxng"})
	nonNative := NonNativeToolSystemPrompt(now, 55, []string{"tavily"})

	for name, prompt := range map[string]string{"native": native, "nonNative": nonNative} {
		if strings.Contains(prompt, "%!") {
			t.Fatalf("%s prompt has an unrendered fmt verb:\n%s", name, prompt)
		}
		if !strings.Contains(prompt, "read_file paging") {
			t.Fatalf("%s prompt is missing the read_file paging section", name)
		}
		if !strings.Contains(prompt, "start_line") {
			t.Fatalf("%s prompt is missing the start_line teaching", name)
		}
	}
}

// readFileSection must name the same cap the tool enforces, and the paging
// example must advance start_line by that cap.
func TestReadFileSectionNamesCap(t *testing.T) {
	s := readFileSection()
	if !strings.Contains(s, "2000") {
		t.Fatal("readFileSection should state the 2000-line cap")
	}
	if !strings.Contains(s, "read_file(path, start_line=2001") {
		t.Fatalf("paging example should show the second window at 2001:\n%s", s)
	}
}
