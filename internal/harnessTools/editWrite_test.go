package harnessTools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEditFileReplacesExactlyOnce(t *testing.T) {
	path := writeTemp(t, "a.txt", "hello world\n")

	info, err := EditFile(path, "world", "there", false)
	if err != nil {
		t.Fatalf("EditFile: %v", err)
	}
	if !strings.HasPrefix(info, "EDIT_OK") {
		t.Fatalf("result should start with EDIT_OK, got %q", info)
	}

	data, _ := os.ReadFile(path)
	if string(data) != "hello there\n" {
		t.Fatalf("file = %q", string(data))
	}
}

func TestEditFileRejectsZeroAndMultipleMatches(t *testing.T) {
	path := writeTemp(t, "a.txt", "one one\n")

	if _, err := EditFile(path, "missing", "x", false); err == nil {
		t.Fatal("a zero-match edit should error")
	}
	if _, err := EditFile(path, "one", "x", false); err == nil {
		t.Fatal("a multiple-match edit should error")
	}

	data, _ := os.ReadFile(path)
	if string(data) != "one one\n" {
		t.Fatalf("a failed edit must not change the file, got %q", string(data))
	}
}

func TestEditFileAppendMode(t *testing.T) {
	path := writeTemp(t, "a.txt", "start")

	if _, err := EditFile(path, "", "more", true); err != nil {
		t.Fatalf("append: %v", err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "startmore" {
		t.Fatalf("append is byte-for-byte, got %q", string(data))
	}
}

func TestWriteFileCreatesAndFormats(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new.txt")

	info, err := WriteFile(path, "one\ntwo\n")
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if !strings.HasPrefix(info, "WRITE_OK") {
		t.Fatalf("result should start with WRITE_OK, got %q", info)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "one\ntwo\n" {
		t.Fatalf("file = %q", string(data))
	}
}

func TestWriteFileIsCreateOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new.txt")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteFile(path, "y"); err == nil {
		t.Fatal("writing over an existing file must error")
	}
}

func TestFormatWriteResultTruncatesPreview(t *testing.T) {
	content := strings.Repeat("a", MaxWritePreview+10)
	got := FormatWriteResult("/p/f", "/p", "f", content)
	if !strings.Contains(got, "content truncated") {
		t.Fatalf("a long content should note truncation: %q", got)
	}
}

func TestFormatWriteResultCountsLines(t *testing.T) {
	got := FormatWriteResult("/p/f", "/p", "f", "a\nb\nc")
	if !strings.Contains(got, "lines=3") {
		t.Fatalf("want lines=3 for a no-trailing-newline file, got %q", got)
	}
	got = FormatWriteResult("/p/f", "/p", "f", "a\nb\nc\n")
	if !strings.Contains(got, "lines=3") {
		t.Fatalf("want lines=3 for a trailing-newline file, got %q", got)
	}
}
