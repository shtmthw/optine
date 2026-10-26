package harnessDispatch

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mattthew/optine/internal/dataTypes"
	"github.com/mattthew/optine/internal/harnessPermissions"
)

// TestMain points the config dir at a temp location so the audit log and any
// derived state never touch the real ~/.config during the test run.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "optine-dispatch-*")
	if err != nil {
		panic(err)
	}
	harnessPermissions.AuthPath = filepath.Join(dir, "auth.json")
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// answerReader feeds one approval answer to the policy prompts.
func answerReader(answer string) *bufio.Reader {
	return bufio.NewReader(strings.NewReader(answer + "\n"))
}

func call(tool string, args map[string]any) *dataTypes.NativeLLMResponse {
	return &dataTypes.NativeLLMResponse{Type: "tool_call", Tool: tool, Arguments: args}
}

// An unknown tool name is a hard error, never a silent no-op.
func TestDispatchUnknownTool(t *testing.T) {
	_, err := Dispatch(context.Background(), answerReader("y"), call("nope", nil))
	if err == nil {
		t.Fatal("unknown tool should error")
	}
	if !strings.Contains(err.Error(), "nope") {
		t.Fatalf("error should name the tool, got %v", err)
	}
}

// read_file routes through ReadFilePolicy (approved once here) and returns the
// file's contents.
func TestDispatchReadFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := Dispatch(context.Background(), answerReader("y"), call("read_file", map[string]any{"path": path}))
	if err != nil {
		t.Fatalf("Dispatch read_file: %v", err)
	}
	if !strings.Contains(out, "hello") {
		t.Fatalf("want file contents, got %q", out)
	}
}

// A rejected approval surfaces as ErrToolCallRejection for web_search, and the
// harness must not run the search.
func TestDispatchWebSearchRejected(t *testing.T) {
	_, err := Dispatch(context.Background(), answerReader("n"),
		call("web_search", map[string]any{"query": "x", "crawler": "searxng"}))
	if err != ErrToolCallRejection {
		t.Fatalf("want ErrToolCallRejection, got %v", err)
	}
}

// After the approval, an unknown crawler is reported by the search tool itself.
func TestDispatchWebSearchUnknownCrawler(t *testing.T) {
	_, err := Dispatch(context.Background(), answerReader("y"),
		call("web_search", map[string]any{"query": "x", "crawler": "bogus"}))
	if err == nil {
		t.Fatal("unknown crawler should error")
	}
}

func TestDispatchWebSearchMissingArguments(t *testing.T) {
	cases := []map[string]any{
		{"crawler": "searxng"},              // no query
		{"query": "x"},                      // no crawler
		{"query": "", "crawler": "searxng"}, // empty query
		{"query": "x", "crawler": 5},        // crawler not a string
	}
	for i, args := range cases {
		if _, err := Dispatch(context.Background(), answerReader("y"), call("web_search", args)); err == nil {
			t.Fatalf("case %d: expected error for %v", i, args)
		}
	}
}

// edit_file rejects a missing target before writing anything.
func TestDispatchEditFileMissingTarget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.txt")
	_, err := Dispatch(context.Background(), answerReader("y"),
		call("edit_file", map[string]any{"path": path, "old_string": "a", "new_string": "b"}))
	if err == nil {
		t.Fatal("editing a missing file should error")
	}
}

// write_file creates a new file, and a second write to the same path is a
// create-only error rather than an overwrite.
func TestDispatchWriteFileCreateOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new.txt")

	out, err := Dispatch(context.Background(), answerReader("y"),
		call("write_file", map[string]any{"path": path, "content": "hi"}))
	if err != nil {
		t.Fatalf("first write: %v", err)
	}
	if !strings.HasPrefix(out, "WRITE_OK ") {
		t.Fatalf("want WRITE_OK result, got %q", out)
	}

	if _, err := Dispatch(context.Background(), answerReader("y"),
		call("write_file", map[string]any{"path": path, "content": "again"})); err == nil {
		t.Fatal("writing an existing file should error")
	}
}

// bash round-trips through the tool and returns readable text for the model;
// a denial is a successful dispatch carrying explanatory text, not an error.
func TestDispatchBashDeniedIsTextNotError(t *testing.T) {
	// A heredoc cannot be analyzed, so the classifier denies it without asking.
	out, err := Dispatch(context.Background(), answerReader(""),
		call("bash", map[string]any{"command": "cat <<EOF\nx\nEOF"}))
	if err != nil {
		t.Fatalf("a denied bash call is text, not an error: %v", err)
	}
	if !strings.Contains(out, "denied") {
		t.Fatalf("want a denial message, got %q", out)
	}
}
