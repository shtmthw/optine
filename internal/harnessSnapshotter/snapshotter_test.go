package harnessSnapshotter

import (
	"os"
	"path/filepath"
	"testing"
)

// writeFile is a small helper that fails the test on any write error.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Track+Revert must restore changed files, recreate deleted ones and remove
// files that were created in between — the whole point of the snapshotter.
func TestTrackRevertRestoresWorkdir(t *testing.T) {
	work := t.TempDir()
	store := t.TempDir()

	writeFile(t, filepath.Join(work, "keep.txt"), "original\n")
	writeFile(t, filepath.Join(work, "gone.txt"), "delete me\n")

	if err := Track(work, store, "before"); err != nil {
		t.Fatalf("Track before: %v", err)
	}

	writeFile(t, filepath.Join(work, "keep.txt"), "changed\n")
	if err := os.Remove(filepath.Join(work, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(work, "created.txt"), "new\n")

	if err := Track(work, store, "after"); err != nil {
		t.Fatalf("Track after: %v", err)
	}

	touched, err := Revert(work, store, "before", "after")
	if err != nil {
		t.Fatalf("Revert: %v", err)
	}
	if len(touched) != 3 {
		t.Fatalf("want 3 reverted paths, got %v", touched)
	}

	if got := readFile(t, filepath.Join(work, "keep.txt")); got != "original\n" {
		t.Fatalf("keep.txt not restored: %q", got)
	}
	if got := readFile(t, filepath.Join(work, "gone.txt")); got != "delete me\n" {
		t.Fatalf("gone.txt not recreated: %q", got)
	}
	if _, err := os.Stat(filepath.Join(work, "created.txt")); !os.IsNotExist(err) {
		t.Fatalf("created.txt should have been removed, err=%v", err)
	}
}

// Files identical in both snapshots are left untouched.
func TestRevertLeavesUntouchedFilesAlone(t *testing.T) {
	work := t.TempDir()
	store := t.TempDir()

	writeFile(t, filepath.Join(work, "stable.txt"), "same\n")

	if err := Track(work, store, "before"); err != nil {
		t.Fatal(err)
	}
	if err := Track(work, store, "after"); err != nil {
		t.Fatal(err)
	}

	touched, err := Revert(work, store, "before", "after")
	if err != nil {
		t.Fatalf("Revert: %v", err)
	}
	if len(touched) != 0 {
		t.Fatalf("nothing changed, want 0 touched, got %v", touched)
	}
}

// An unchanged executable keeps its mode across Track/Revert, which the
// content-addressed store has to re-apply after writing a fresh temp file.
func TestRevertPreservesExecutableMode(t *testing.T) {
	work := t.TempDir()
	store := t.TempDir()

	script := filepath.Join(work, "run.sh")
	writeFile(t, script, "#!/bin/sh\necho one\n")
	if err := os.Chmod(script, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := Track(work, store, "before"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, script, "#!/bin/sh\necho two\n")
	if err := Track(work, store, "after"); err != nil {
		t.Fatal(err)
	}

	if _, err := Revert(work, store, "before", "after"); err != nil {
		t.Fatalf("Revert: %v", err)
	}

	fi, err := os.Stat(script)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %o, want 755", fi.Mode().Perm())
	}
}

// A tampered manifest path must never escape the workdir.
func TestLocalPathRejectsEscape(t *testing.T) {
	if _, err := localPath("/tmp/work", "../evil"); err == nil {
		t.Fatal("path escaping workdir should be rejected")
	}
	if got, err := localPath("/tmp/work", "sub/file.txt"); err != nil || got != "/tmp/work/sub/file.txt" {
		t.Fatalf("localPath = %q, err=%v", got, err)
	}
}

// readFile is a read helper that fails the test on error.
func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
