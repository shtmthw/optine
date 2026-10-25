package harnessTools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// MaxEditPreview caps each old/new excerpt in the deterministic result so a
// huge replacement does not flood the model context or the TUI event feed.
const MaxEditPreview = 2000

// editLockTimeout bounds how long EditFile waits for another editor's flock
// before giving up so the agent gets a retryable error instead of hanging.
// editLockPoll is the retry interval for non-blocking LOCK_EX attempts.
const (
	editLockTimeout = 5 * time.Second
	editLockPoll    = 50 * time.Millisecond
)

// appendContent returns fileData with additionString appended byte-for-byte.
// Pure string concat by design: no newline insertion or separator logic,
// mirroring the byte-based splice the edit path uses.
func appendContent(fileData, additionString string) string {
	return fileData + additionString
}

// writeAtomicReplace persists updated to realPath atomically: temp file in
// the same dir, Sync for durability, Chmod to mode, then Rename. Shared by
// the edit and append paths so both get identical durability semantics.
func writeAtomicReplace(dir, realPath, updated string, mode os.FileMode) error {
	tmp, err := os.CreateTemp(dir, ".edit-*")
	if err != nil {
		return fmt.Errorf("edit_file %q: temp file: %w", realPath, err)
	}
	tmpName := tmp.Name()
	// Best effort cleanup; renamed away on success.
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.WriteString(updated); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("edit_file %q: writing temp: %w", realPath, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("edit_file %q: syncing temp: %w", realPath, err)
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("edit_file %q: chmod temp: %w", realPath, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("edit_file %q: closing temp: %w", realPath, err)
	}
	if err := os.Rename(tmpName, realPath); err != nil {
		return fmt.Errorf("edit_file %q: replacing file: %w", realPath, err)
	}
	return nil
}

// EditFile applies one exact oldString -> newString replacement to the file at
// realPath (already resolved and approved by EditFilePolicy) and returns a
// deterministic info string built only from what was actually read and written.
// When doAppend is true oldString is ignored and newString is appended
// byte-for-byte to the end of the file instead.
// The model's own description of the change is never used for facts.
func EditFile(realPath, oldString, newString string, doAppend bool) (string, error) {
	dir := filepath.Dir(realPath)
	base := filepath.Base(realPath)

	// Cross-process mutual exclusion via a persistent sidecar lockfile. A
	// lock on realPath itself would stay on the old inode after the atomic
	// Rename below, so concurrent editors could each hold a lock on a
	// different inode. The sidecar survives renames because it is never
	// renamed. All read-modify-write work happens while LOCK_EX is held.
	lockPath := filepath.Join(dir, "."+base+".lock")
	lockedfile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return "", fmt.Errorf("edit_file %q: lock file: %w", realPath, err)
	}
	acquired := false
	deadline := time.Now().Add(editLockTimeout)
	var flockErr error
	for {
		flockErr = syscall.Flock(int(lockedfile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if flockErr == nil {
			acquired = true
			break
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(editLockPoll)
	}
	if !acquired {
		_ = lockedfile.Close()
		if flockErr != nil {
			return "", fmt.Errorf("edit_file %q: locked by another edit, re-read and retry: %v", realPath, flockErr)
		}
		return "", fmt.Errorf("edit_file %q: locked by another edit, re-read and retry", realPath)
	}
	defer func() {
		_ = syscall.Flock(int(lockedfile.Fd()), syscall.LOCK_UN)
		_ = lockedfile.Close()
	}()

	data, err := os.ReadFile(realPath)
	if err != nil {
		return "", fmt.Errorf("edit_file %q: reading: %w", realPath, err)
	}

	content := string(data)

	info, err := os.Stat(realPath)
	if err != nil {
		return "", fmt.Errorf("edit_file %q: stat: %w", realPath, err)
	}

	if doAppend {
		updated := appendContent(content, newString)
		newLines := countEditLines(newString)
		// Display-only start line for the appended range: a trailing
		// newline (or empty file) means appending starts on a fresh line,
		// otherwise it continues mid-last-line.
		startLine := countEditLines(content) + 1
		if content != "" && !strings.HasSuffix(content, "\n") {
			startLine = countEditLines(content)
		}
		if err := writeAtomicReplace(dir, realPath, updated, info.Mode()); err != nil {
			return "", err
		}
		return FormatEditResult(realPath, dir, base, startLine, startLine, newLines, len(data), len(updated), "", newString), nil
	}

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

	if err := writeAtomicReplace(dir, realPath, updated, info.Mode()); err != nil {
		return "", err
	}

	return FormatEditResult(realPath, dir, base, startLine, endLine, newLines, len(data), len(updated), oldString, newString), nil
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
