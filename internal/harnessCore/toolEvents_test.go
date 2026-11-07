package harnessCore

import (
	"bufio"
	"context"
	"strings"
	"testing"

	"github.com/mattthew/optine/internal/harnessSession"
)

func TestRunToolEmitsCallAndResult(t *testing.T) {
	var events []harnessSession.DisplayEvent
	harnessSession.SetDisplayEventListener(func(event harnessSession.DisplayEvent) {
		events = append(events, event)
	})
	t.Cleanup(func() {
		harnessSession.SetDisplayEventListener(nil)
	})

	result := runTool(context.Background(), bufio.NewReader(strings.NewReader("")), "read_file", map[string]any{})

	if !strings.Contains(result, "missing path argument") {
		t.Fatalf("result = %q", result)
	}
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2: %+v", len(events), events)
	}
	if events[0].Role != harnessSession.TranscriptToolCall || events[0].Content != "tool call: read_file" {
		t.Fatalf("call event wrong: %+v", events[0])
	}
	if events[1].Role != harnessSession.TranscriptToolResult || events[1].Content != result {
		t.Fatalf("result event wrong: %+v", events[1])
	}
}
