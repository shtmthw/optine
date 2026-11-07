package tui

import (
	"strings"
	"testing"

	"github.com/mattthew/optine/internal/harnessTools"
)

const (
	bashBG   = "48;5;237m"
	webBG    = "48;5;58m"
	greenBG  = "48;5;22m"
	nameGrey = "1;38;5;245m"
)

func TestParseToolCall(t *testing.T) {
	call, ok := parseToolCall(`tool call: read_file {"path":"a.go","start_line":3,"end_line":9}`)
	if !ok || call.name != "read_file" || call.stringArg("path") != "a.go" || !call.colored() {
		t.Fatalf("parse = %+v, %v", call, ok)
	}
	if got := call.title(); got != "a.go  lines 3-9" {
		t.Fatalf("title = %q", got)
	}

	for _, content := range []string{`tool call: edit_file {"path":"a.go"}`, `tool call: write_file {"path":"a.go"}`} {
		if call, ok := parseToolCall(content); !ok || call.colored() {
			t.Fatalf("%q: ok=%v colored=%v", content, ok, call.colored())
		}
	}
	if _, ok := parseToolCall("tool result"); ok {
		t.Fatal("non tool-call text parsed")
	}
}

func TestToolTitles(t *testing.T) {
	cases := map[string]string{
		`tool call: bash {"command":"go test ./..."}`: "$ go test ./...",
		"tool call: bash": "$",
		`tool call: web_search {"query":"go 1.24","crawler":"x"}`: "go 1.24  via x",
		`tool call: web_search {}`:                                "(no query)",
		`tool call: read_file {"path":"a.go","start_line":5}`:     "a.go  from line 5",
		`tool call: read_file {"path":"a.go"}`:                    "a.go",
	}
	for content, want := range cases {
		call, _ := parseToolCall(content)
		if got := call.title(); got != want {
			t.Fatalf("title(%s) = %q, want %q", content, got, want)
		}
	}
}

// Bold grey name, then call and result on one background with no gap.
func TestToolBlocksAreColoredAndJoined(t *testing.T) {
	cases := []struct {
		call, result, bg, name, title, body string
	}{
		{`tool call: bash {"command":"ls"}`, "file.go", bashBG, "bash", "$ ls", "file.go"},
		{`tool call: read_file {"path":"a.go","start_line":7}`, "package a", greenBG, "read_file", "a.go", "7 │ package a"},
	}

	for _, tc := range cases {
		m := modelWithMessages(newTestModelAt(80, 30),
			Message{Role: roleToolCall, Content: tc.call},
			Message{Role: roleToolResult, Content: tc.result},
		)
		view := m.View()
		if strings.Contains(plainText(view), "tool call:") {
			t.Fatalf("%s: raw call line shown:\n%s", tc.call, plainText(view))
		}
		lines := strings.Split(view, "\n")

		nameRow := -1
		for i, l := range lines {
			if strings.TrimSpace(plainText(l)) == tc.name {
				nameRow = i
				break
			}
		}
		if nameRow < 0 || nameRow+2 >= len(lines) {
			t.Fatalf("%s: name %q not found:\n%s", tc.call, tc.name, plainText(view))
		}
		name, title, body := lines[nameRow], lines[nameRow+1], lines[nameRow+2]
		if !strings.Contains(name, nameGrey) {
			t.Fatalf("%s: name not bold grey: %s", tc.call, escapeANSI(name))
		}
		if !strings.Contains(plainText(title), tc.title) {
			t.Fatalf("%s: title row = %q, want %q", tc.call, plainText(title), tc.title)
		}
		if !strings.Contains(title, tc.bg) || !strings.Contains(body, tc.bg) {
			t.Fatalf("%s: rows not on %s:\n%s\n%s", tc.call, tc.bg, escapeANSI(title), escapeANSI(body))
		}
		if !strings.Contains(plainText(body), tc.body) {
			t.Fatalf("%s: body row = %q, want %q", tc.call, plainText(body), tc.body)
		}
	}
}

// A log line between call and result keeps its place; the result keeps the color.
func TestToolResultAfterEventKeepsColor(t *testing.T) {
	m := modelWithMessages(newTestModelAt(80, 30),
		Message{Role: roleToolCall, Content: `tool call: read_file {"path":"secret"}`},
		Message{Role: roleEvent, Content: "read denied"},
		Message{Role: roleToolResult, Content: "Tool execution failed: read denied"},
	)
	view := m.View()

	event := strings.Index(plainText(view), "read denied")
	result := strings.Index(plainText(view), "Tool execution failed")
	if event < 0 || result < event {
		t.Fatalf("order wrong:\n%s", plainText(view))
	}
	for _, l := range strings.Split(view, "\n") {
		if strings.Contains(l, "Tool execution failed") && !strings.Contains(l, greenBG) {
			t.Fatalf("failure row lost the read color: %s", escapeANSI(l))
		}
	}
}

func TestReadRowsNotesAndFailures(t *testing.T) {
	call, _ := parseToolCall(`tool call: read_file {"path":"a.go","start_line":9}`)

	rows := readRows(call, "one\ntwo\n\n[read_file: showing lines 9-10; end of file reached]", 80)
	want := []string{" 9 │ one", "10 │ two", "[read_file: showing lines 9-10; end of file reached]"}
	if strings.Join(rows, "\n") != strings.Join(want, "\n") {
		t.Fatalf("rows = %q", rows)
	}

	for _, result := range []string{"Tool execution failed: nope", "[read_file: a.go has fewer than 9 lines, nothing to read]", "(no output)"} {
		if rows := readRows(call, result, 80); len(rows) != 1 || rows[0] != result {
			t.Fatalf("readRows(%q) = %q", result, rows)
		}
	}
}

// edit_file shows only its name above the existing edit view.
func TestEditCallShowsNameAboveEditView(t *testing.T) {
	edit := harnessTools.FormatEditResult(
		"/work/a.go", "/work", "a.go",
		3, 4, 2, 10, 22,
		"old line\n", "new line\n",
	)
	m := modelWithMessages(newTestModelAt(80, 30),
		Message{Role: roleToolCall, Content: `tool call: edit_file {"path":"a.go"}`},
		Message{Role: roleToolResult, Content: edit},
	)
	lines := strings.Split(plainText(m.View()), "\n")

	for i, l := range lines {
		if strings.TrimSpace(l) == "edit_file" {
			if i+1 >= len(lines) || !strings.HasPrefix(lines[i+1], "a.go") {
				t.Fatalf("edit view not directly under name:\n%s", strings.Join(lines, "\n"))
			}
			return
		}
	}
	t.Fatalf("edit_file name missing:\n%s", strings.Join(lines, "\n"))
}

func TestAgentAndUserColors(t *testing.T) {
	m := modelWithMessages(newTestModelAt(80, 20),
		Message{Role: roleUser, Content: "hi"},
		Message{Role: roleAgent, Content: "hello"},
	)
	view := m.View()
	if !strings.Contains(view, "1;38;5;231m> hi") {
		t.Fatalf("user not bold white:\n%s", escapeANSI(view))
	}
	if !strings.Contains(view, "38;5;253mhello") {
		t.Fatalf("agent not grey:\n%s", escapeANSI(view))
	}
}

// web_search shows only its query; the results stay hidden with no gap left.
func TestWebSearchHidesResult(t *testing.T) {
	m := modelWithMessages(newTestModelAt(80, 30),
		Message{Role: roleToolCall, Content: `tool call: web_search {"query":"lipgloss"}`},
		Message{Role: roleToolResult, Content: "1. Lip Gloss\nURL: https://example.com"},
		Message{Role: roleAgent, Content: "done"},
	)
	view := m.View()
	plain := plainText(view)
	if strings.Contains(plain, "Lip Gloss") || strings.Contains(plain, "URL:") {
		t.Fatalf("search results shown:\n%s", plain)
	}

	lines := strings.Split(view, "\n")
	for i, l := range lines {
		if strings.Contains(plainText(l), "lipgloss") {
			if !strings.Contains(l, webBG) || strings.TrimSpace(plainText(lines[i-1])) != "web_search" {
				t.Fatalf("query box wrong:\n%s", escapeANSI(strings.Join(lines[i-1:i+1], "\n")))
			}
			if plainText(lines[i+1]) != "" || strings.TrimSpace(plainText(lines[i+2])) != "done" {
				t.Fatalf("spacing after query wrong:\n%s", plain)
			}
			return
		}
	}
	t.Fatalf("query missing:\n%s", plain)
}
