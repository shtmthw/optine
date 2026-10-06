package harnessTools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mattthew/optine/internals/harnessPermissions"
)

// MaxWritePreview caps the content excerpt in the deterministic result so a
// huge file does not flood the model context or the TUI event feed.
const MaxWritePreview = 2000

// WriteFile creates the file at realPath (already resolved and approved by
// WriteFilePolicy) with content and returns a deterministic info string built
// only from what was actually written. Create-only: an existing target is an
// error (use edit_file). Missing parents are created with 0755.
func WriteFile(realPath, content string) (string, error) {
	if fi, err := os.Lstat(realPath); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("write_file %q: target is a symlink, writing through symlinks is not allowed", realPath)
		}
		return "", fmt.Errorf("write_file %q: file already exists, use edit_file to change it", realPath)
	}

	dir := filepath.Dir(realPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("write_file %q: creating parent dirs: %w", realPath, err)
	}

	// Re-check after MkdirAll: the file may have appeared between policy and exec.
	if fi, err := os.Lstat(realPath); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("write_file %q: target is a symlink, writing through symlinks is not allowed", realPath)
		}
		return "", fmt.Errorf("write_file %q: file already exists, use edit_file to change it", realPath)
	}

	mode := os.FileMode(0o644)
	if harnessPermissions.IsSensitive(realPath) {
		mode = 0o600
	}

	tmp, err := os.CreateTemp(dir, ".write-*")
	if err != nil {
		return "", fmt.Errorf("write_file %q: temp file: %w", realPath, err)
	}
	tmpName := tmp.Name()
	// Best effort cleanup; renamed away on success.
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("write_file %q: writing temp: %w", realPath, err)
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("write_file %q: chmod temp: %w", realPath, err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("write_file %q: closing temp: %w", realPath, err)
	}
	if err := os.Rename(tmpName, realPath); err != nil {
		return "", fmt.Errorf("write_file %q: creating file: %w", realPath, err)
	}

	return FormatWriteResult(realPath, dir, filepath.Base(realPath), content), nil
}

// FormatWriteResult builds the deterministic harness reply for a passing
// write. Stable WRITE_OK prefix keeps TUI parsing to a HasPrefix check,
// mirroring FormatEditResult's EDIT_OK.
func FormatWriteResult(realPath, dir, file, content string) string {
	lines := 0
	if content != "" {
		lines = strings.Count(content, "\n")
		if !strings.HasSuffix(content, "\n") {
			lines++
		}
	}
	preview, truncated := content, false
	if len(preview) > MaxWritePreview {
		preview = preview[:MaxWritePreview]
		truncated = true
	}

	var b strings.Builder
	fmt.Fprintf(&b, "WRITE_OK path=%s dir=%s file=%s lines=%d bytes=%d\n", realPath, dir, file, lines, len(content))
	fmt.Fprintf(&b, "--- content:\n%s\n", preview)
	if truncated {
		fmt.Fprintf(&b, "[content truncated to %d chars]\n", MaxWritePreview)
	}
	fmt.Fprintf(&b, "This file was created with the content above in %s.", realPath)
	return b.String()
}
