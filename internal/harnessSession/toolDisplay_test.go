package harnessSession

import (
	"testing"

	"github.com/mattthew/optine/internal/harnessTools"
)

func TestFormatToolCall(t *testing.T) {
	if got := formatToolCall("bash", nil); got != "tool call: bash" {
		t.Fatalf("empty arguments = %q", got)
	}

	got := formatToolCall("read_file", map[string]any{"path": "a.go", "start_line": 2})
	want := `tool call: read_file {"path":"a.go","start_line":2}`
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestFormatToolResultRedactsSensitiveFile(t *testing.T) {
	raw := harnessTools.FormatEditResult(
		"/home/me/.ssh/id_rsa", "/home/me/.ssh", "id_rsa",
		1, 1, 1, 1, 2,
		"old", "new",
	)

	got := formatToolResult("edit_file", raw)
	want := "edit_file /home/me/.ssh/id_rsa: applied (content hidden: sensitive file)"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestFormatToolResultEmpty(t *testing.T) {
	if got := formatToolResult("bash", "\n"); got != "(no output)" {
		t.Fatalf("got %q", got)
	}
}

func TestDisplayListenerGetsLiveMessages(t *testing.T) {
	var events []DisplayEvent
	SetDisplayEventListener(func(event DisplayEvent) {
		events = append(events, event)
	})
	t.Cleanup(func() {
		SetDisplayEventListener(nil)
	})

	NotifyAssistant("thinking")
	NotifyToolCall("bash", map[string]any{"command": "ls"})
	NotifyToolResult("bash", "file.go\n")

	want := []DisplayEvent{
		{Role: TranscriptAssistant, Content: "thinking"},
		{Role: TranscriptToolCall, Content: `tool call: bash {"command":"ls"}`},
		{Role: TranscriptToolResult, Content: "file.go"},
	}

	if len(events) != len(want) {
		t.Fatalf("got %d events, want %d: %+v", len(events), len(want), events)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("event %d = %+v, want %+v", i, events[i], want[i])
		}
	}
}
