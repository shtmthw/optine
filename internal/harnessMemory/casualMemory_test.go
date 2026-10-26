package harnessMemory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mattthew/optine/internal/harnessPermissions"
)

// withAuthPath points the whole config-dir derivation at a temp dir for one
// test. Every config file is derived from AuthPath, so this is the only seam
// needed to keep the test off the real ~/.config.
func withAuthPath(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	old := harnessPermissions.AuthPath
	harnessPermissions.AuthPath = filepath.Join(dir, "auth.json")
	t.Cleanup(func() { harnessPermissions.AuthPath = old })

	return dir
}

// An empty AuthPath means the config dir never resolved: every consumer must
// fail closed instead of writing next to the working directory.
func TestCasualMemoryPathFailsClosedWithoutAuthPath(t *testing.T) {
	old := harnessPermissions.AuthPath
	harnessPermissions.AuthPath = ""
	t.Cleanup(func() { harnessPermissions.AuthPath = old })

	if _, err := CasualMemoryPath(); err == nil {
		t.Fatal("empty AuthPath should error, not return a path")
	}
}

// CasualMemoryPath sits next to auth.json and creates the config dir on demand.
func TestCasualMemoryPathCreatesAndDerivesFromAuthPath(t *testing.T) {
	dir := withAuthPath(t)

	got, err := CasualMemoryPath()
	if err != nil {
		t.Fatalf("CasualMemoryPath: %v", err)
	}

	want := filepath.Join(dir, "casualMemory.md")
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		t.Fatalf("config dir was not created: %v", err)
	}
}

// The first read seeds the default memory file and returns its contents; the
// second read returns what is actually on disk without overwriting it.
func TestReadCasualMemorySeedsThenReturnsFile(t *testing.T) {
	withAuthPath(t)

	first, err := ReadCasualMemoryFile()
	if err != nil {
		t.Fatalf("seed read: %v", err)
	}
	if !strings.Contains(first, "default memory file") {
		t.Fatalf("first read should seed the default text, got %q", first)
	}

	path, err := CasualMemoryPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("user note\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	second, err := ReadCasualMemoryFile()
	if err != nil {
		t.Fatalf("second read: %v", err)
	}
	if second != "user note\n" {
		t.Fatalf("existing content must not be overwritten, got %q", second)
	}
}
