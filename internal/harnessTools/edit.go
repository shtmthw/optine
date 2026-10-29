package harnessTools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// MaxEditPreview caps each old/new excerpt in the result.
const MaxEditPreview = 2000

// editLockTimeout and editLockPoll bound the wait for another editor's flock.
const (
	editLockTimeout = 5 * time.Second
	editLockPoll    = 50 * time.Millisecond
)

// appendContent appends byte-for-byte, with no newline insertion.
func appendContent(fileData, additionString string) string {
	return fileData + additionString
}

// writeAtomicReplace writes updated through a temp file, Sync, Chmod and Rename.
func writeAtomicReplace(dir, realPath, updated string, mode os.FileMode) error {
	tmp, err := os.CreateTemp(dir, ".edit-*")
	if err != nil {
		return fmt.Errorf("edit_file %q: temp file: %w", realPath, err)
	}
	tmpName := tmp.Name()
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

// EditFile applies one exact oldString replacement, or appends newString when
// doAppend is true. The result is built only from what was actually read and
// written; the model's description is never used as fact.
func EditFile(realPath, oldString, newString string, doAppend bool) (string, error) {
	dir := filepath.Dir(realPath)
	base := filepath.Base(realPath)

	// Lock a sidecar file, not realPath: the atomic rename would leave a lock
	// on the old inode. All read-modify-write work runs under LOCK_EX.
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
		// Display-only start line: append starts on a fresh line after a
		// trailing newline, otherwise mid-line.
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

// FormatEditResult builds the deterministic EDIT_OK reply.
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

// countEditLines counts displayed lines; a trailing newline does not add one.
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
