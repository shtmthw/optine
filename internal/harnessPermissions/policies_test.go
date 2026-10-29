package harnessPermissions

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func answerReader(answer string) *bufio.Reader {
	return bufio.NewReader(strings.NewReader(answer + "\n"))
}

func TestReadFilePolicyApprovedOnceWhenDenied(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := ReadFilePolicy(map[string]any{"path": path}, answerReader("y"))
	if err != nil {
		t.Fatalf("Once should allow the read: %v", err)
	}
	if got != path {
		t.Fatalf("got %q, want %q", got, path)
	}

	if _, err := ReadFilePolicy(map[string]any{"path": path}, answerReader("n")); !errors.Is(err, ErrReadDenied) {
		t.Fatalf("No should deny with ErrReadDenied, got %v", err)
	}
}

func TestReadFilePolicyRemembersDirOnAlways(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	// "always" approves the directory for the session, so the next read of
	// another file in the same dir must not read from stdin at all.
	if _, err := ReadFilePolicy(map[string]any{"path": path}, answerReader("a")); err != nil {
		t.Fatalf("Remember should allow: %v", err)
	}
	empty := bufio.NewReader(strings.NewReader("")) // EOF if it tries to prompt
	if _, err := ReadFilePolicy(map[string]any{"path": path}, empty); err != nil {
		t.Fatalf("approved dir should not prompt again: %v", err)
	}
}

func TestReadFilePolicyArgumentValidation(t *testing.T) {
	_, err := ReadFilePolicy(map[string]any{}, answerReader("y"))
	if err == nil {
		t.Fatal("missing path should error")
	}
	_, err = ReadFilePolicy(map[string]any{"path": 42}, answerReader("y"))
	if err == nil {
		t.Fatal("non-string path should error")
	}
	_, err = ReadFilePolicy(map[string]any{"path": "  "}, answerReader("y"))
	if err == nil {
		t.Fatal("blank path should error")
	}
}

func TestReadFilePolicyMissingFileErrors(t *testing.T) {
	dir := t.TempDir()
	_, err := ReadFilePolicy(map[string]any{"path": filepath.Join(dir, "nope.txt")}, answerReader("y"))
	if err == nil {
		t.Fatal("a missing file should not be readable")
	}
}

func TestWriteFilePolicyCreatesAndRejectsExisting(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "new.txt")

	got, content, err := WriteFilePolicy(map[string]any{"path": target, "content": "hello"}, answerReader("y"))
	if err != nil {
		t.Fatalf("a fresh path should be approvable: %v", err)
	}
	if got != target || content != "hello" {
		t.Fatalf("got (%q, %q)", got, content)
	}

	// Create-only: once it exists the policy refuses, never overwrites.
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := WriteFilePolicy(map[string]any{"path": target, "content": "y"}, answerReader("y")); err == nil {
		t.Fatal("an existing file must be rejected")
	}
}

func TestWriteFilePolicyRejectsSymlinkTarget(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "link.txt")
	if err := os.Symlink(filepath.Join(dir, "real.txt"), link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	_, _, err := WriteFilePolicy(map[string]any{"path": link, "content": "x"}, answerReader("y"))
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("a symlink target must be a hard error, got %v", err)
	}
}

func TestWriteFilePolicyDeniedByUser(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "new.txt")
	if _, _, err := WriteFilePolicy(map[string]any{"path": target, "content": "x"}, answerReader("n")); !errors.Is(err, ErrWriteDenied) {
		t.Fatalf("want ErrWriteDenied, got %v", err)
	}
}

func TestWriteFilePolicyContentRequiredAndBounded(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "new.txt")

	if _, _, err := WriteFilePolicy(map[string]any{"path": target}, answerReader("y")); err == nil {
		t.Fatal("missing content should error")
	}

	big := strings.Repeat("x", MaxFileToolBytes+1)
	if _, _, err := WriteFilePolicy(map[string]any{"path": target, "content": big}, answerReader("y")); err == nil {
		t.Fatal("over-limit content should error")
	}
}

func TestEditFilePolicyRequiresExistingFile(t *testing.T) {
	dir := t.TempDir()
	_, _, _, err := EditFilePolicy(map[string]any{
		"path":       filepath.Join(dir, "nope.txt"),
		"old_string": "a",
		"new_string": "b",
	}, answerReader("y"), false)
	if err == nil {
		t.Fatal("editing a missing file should error")
	}
}

func TestEditFilePolicyReturnsStringsOnApproval(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}

	real, old, newS, err := EditFilePolicy(map[string]any{
		"path":       path,
		"old_string": "world",
		"new_string": "there",
	}, answerReader("y"), false)
	if err != nil {
		t.Fatalf("approval should succeed: %v", err)
	}
	if real != path || old != "world" || newS != "there" {
		t.Fatalf("got (%q, %q, %q)", real, old, newS)
	}
}

func TestEditFilePolicyRejectsBadArgs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// identical old/new has nothing to change
	if _, _, _, err := EditFilePolicy(map[string]any{"path": path, "old_string": "x", "new_string": "x"}, answerReader("y"), false); err == nil {
		t.Fatal("identical old/new should error")
	}
	// empty old_string
	if _, _, _, err := EditFilePolicy(map[string]any{"path": path, "old_string": "", "new_string": "y"}, answerReader("y"), false); err == nil {
		t.Fatal("empty old_string should error")
	}
	// append mode needs only new_string
	if _, _, _, err := EditFilePolicy(map[string]any{"path": path, "new_string": ""}, answerReader("y"), true); err == nil {
		t.Fatal("empty append new_string should error")
	}
}

func TestEditAppendFlag(t *testing.T) {
	if EditAppendFlag(map[string]any{}) {
		t.Fatal("missing append should be false")
	}
	if EditAppendFlag(map[string]any{"append": "yes"}) {
		t.Fatal("non-bool append should be false")
	}
	if !EditAppendFlag(map[string]any{"append": true}) {
		t.Fatal("append=true should be true")
	}
}

func TestWriteAuditAppendsJSONL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")

	if err := WriteAudit(path, AuditRecord{Command: "ls", Dir: "/d", Decision: "allow"}); err != nil {
		t.Fatalf("WriteAudit: %v", err)
	}
	if err := WriteAudit(path, AuditRecord{Command: "rm", Dir: "/d", Decision: "deny"}); err != nil {
		t.Fatalf("WriteAudit: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 JSONL records, got %d: %q", len(lines), string(data))
	}
	if !strings.Contains(lines[0], `"command":"ls"`) || !strings.Contains(lines[0], `"time":"`) {
		t.Fatalf("record missing fields or timestamp: %s", lines[0])
	}
}
