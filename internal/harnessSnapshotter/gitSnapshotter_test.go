package harnessSnapshotter

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// skipWithoutGit skips tests that need a working git binary.
func skipWithoutGit(t *testing.T) {
	t.Helper()
	if !GitAvailable() {
		t.Skip("git is not available")
	}
}

// The git backend must restore changed files, recreate deleted ones and remove
// files created in between, just like the content-addressed backend.
func TestGitTrackRevertRestoresWorkdir(t *testing.T) {
	skipWithoutGit(t)

	work := t.TempDir()
	store := t.TempDir()

	writeFile(t, filepath.Join(work, "keep.txt"), "original\n")
	writeFile(t, filepath.Join(work, "gone.txt"), "delete me\n")

	if err := GitTrack(work, store, "before"); err != nil {
		t.Fatalf("GitTrack before: %v", err)
	}

	writeFile(t, filepath.Join(work, "keep.txt"), "changed\n")
	if err := os.Remove(filepath.Join(work, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(work, "created.txt"), "new\n")

	if err := GitTrack(work, store, "after"); err != nil {
		t.Fatalf("GitTrack after: %v", err)
	}

	touched, err := GitRevert(work, store, "before", "after")
	if err != nil {
		t.Fatalf("GitRevert: %v", err)
	}
	if want := []string{"created.txt", "gone.txt", "keep.txt"}; !slices.Equal(touched, want) {
		t.Fatalf("reverted paths = %v, want %v", touched, want)
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

// Inherited GIT_* overrides must not reach the private snapshot repo.
func TestGitEnvScrubsInheritedOverrides(t *testing.T) {
	t.Setenv("GIT_INDEX_FILE", "/tmp/hijack-index")
	t.Setenv("GIT_OBJECT_DIRECTORY", "/tmp/hijack-objects")

	env := gitEnv()

	for _, kv := range env {
		if strings.HasPrefix(kv, "GIT_INDEX_FILE=") || strings.HasPrefix(kv, "GIT_OBJECT_DIRECTORY=") {
			t.Fatalf("inherited override survived: %s", kv)
		}
	}
	for _, want := range []string{"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull} {
		if !slices.Contains(env, want) {
			t.Fatalf("missing %s", want)
		}
	}
}
