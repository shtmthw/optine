package harnessTools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// MaxEditPreview caps each old/new excerpt in the deterministic result so a
// huge replacement does not flood the model context or the TUI event feed.
const MaxEditPreview = 2000

// EditFile applies one exact oldString -> newString replacement to the file at
// realPath (already resolved and approved by EditFilePolicy) and returns a
// deterministic info string built only from what was actually read and written.
// The model's own description of the change is never used for facts.
func EditFile(realPath, oldString, newString string) (string, error) {
	data, err := os.ReadFile(realPath)
	if err != nil {
		return "", fmt.Errorf("edit_file %q: reading: %w", realPath, err)
	}
	content := string(data)

	count := strings.Count(content, oldString)
	if count == 0 {
		return "", fmt.Errorf("edit_file %q: old_string not found, re-read the file and copy the exact text", realPath)
	}
	if count > 1 {
		return "", fmt.Errorf("edit_file %q: old_string matches %d times, add more surrounding context so it matches exactly once", realPath, count)
	}

	offset := strings.Index(content, oldString)
	startLine := strings.Count(content[:offset], "\n") + 1
	oldLines := countEditLines(oldString)
	newLines := countEditLines(newString)
	endLine := startLine + oldLines - 1

	updated := content[:offset] + newString + content[offset+len(oldString):]

	info, err := os.Stat(realPath)
	if err != nil {
		return "", fmt.Errorf("edit_file %q: stat: %w", realPath, err)
	}

	dir := filepath.Dir(realPath)
	tmp, err := os.CreateTemp(dir, ".edit-*")
	if err != nil {
		return "", fmt.Errorf("edit_file %q: temp file: %w", realPath, err)
	}
	tmpName := tmp.Name()
	// Best effort cleanup; renamed away on success.
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.WriteString(updated); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("edit_file %q: writing temp: %w", realPath, err)
	}
	if err := tmp.Chmod(info.Mode()); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("edit_file %q: chmod temp: %w", realPath, err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("edit_file %q: closing temp: %w", realPath, err)
	}
	if err := os.Rename(tmpName, realPath); err != nil {
		return "", fmt.Errorf("edit_file %q: replacing file: %w", realPath, err)
	}

	return FormatEditResult(realPath, dir, filepath.Base(realPath), startLine, endLine, newLines, len(data), len(updated), oldString, newString), nil
}

// FormatEditResult builds the deterministic harness reply for a passing edit.
// Stable EDIT_OK prefix keeps future TUI parsing to a HasPrefix check.
func FormatEditResult(realPath, dir, file string, startLine, endLine, newLines, bytesBefore, bytesAfter int, oldString, newString string) string {
	oldPreview, oldTruncated := truncatePreview(oldString)
	newPreview, newTruncated := truncatePreview(newString)

	var b strings.Builder
	fmt.Fprintf(&b, "EDIT_OK path=%s dir=%s file=%s lines=%d-%d (new %d lines) bytes=%d->%d\n", realPath, dir, file, startLine, endLine, newLines, bytesBefore, bytesAfter)
	fmt.Fprintf(&b, "--- old (lines %d-%d):\n%s\n", startLine, endLine, oldPreview)
	if oldTruncated {
		fmt.Fprintf(&b, "[old truncated to %d chars]\n", MaxEditPreview)
	}
	fmt.Fprintf(&b, "+++ new (starts line %d, %d lines):\n%s\n", startLine, newLines, newPreview)
	if newTruncated {
		fmt.Fprintf(&b, "[new truncated to %d chars]\n", MaxEditPreview)
	}
	fmt.Fprintf(&b, "This has been changed from the old text above to the new text above in %s.", realPath)
	return b.String()
}

func truncatePreview(s string) (string, bool) {
	if len(s) > MaxEditPreview {
		return s[:MaxEditPreview], true
	}
	return s, false
}

// countEditLines counts the lines s spans: a trailing newline does not start
// another line ("a\nb\n" is 2 lines, "" is 0). Display-only; the splice itself
// is byte-based.
func countEditLines(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
}
