package harnessPermissions

import (
	"log"
	"os"
	"path/filepath"
)

// AuthPath is the resolved location of auth.json, set once in init(). It is
// the single source of the app's config dir: every other file that lives
// there (audit.jsonl, casualMemory.md) is derived from it with
// filepath.Dir, and no other package calls os.UserConfigDir(). Empty means
// the dir never resolved at startup; consumers must fail closed on that.
var AuthPath string

// resolvedOptineConfigDir returns the app's own config dir (the parent of
// audit.jsonl and casualMemory.md), fully resolved so it matches the
// EvalSymlinks-resolved realDir values the policies look up. A literal
// "~/.config/optine" entry can never match: Go performs no tilde expansion
// and lookups use resolved absolute paths.
func resolvedOptineConfigDir() (string, bool) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", false
	}
	dir := filepath.Join(base, "optine")
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		return real, true
	}
	// Not yet created (it is MkdirAll'd just below) or otherwise
	// unresolvable: fall back to the lexical path, which still matches once
	// no symlink sits anywhere above it (the common case).
	return dir, true
}

// establishAuthFile creates the empty auth.json the harness stores its
// provider connections in. An existing file is left untouched.
func establishAuthFile(dir string) error {
	authFile := filepath.Join(dir, "auth.json")
	f, err := os.OpenFile(authFile, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return nil // File already exists; do nothing
		}
		return err // Some other error occurred
	}

	defer f.Close()

	return nil
}

// init pre-approves the app's own config dir for every tool so the agent can
// read/write audit.jsonl-adjacent state and casualMemory.md without a
// per-session prompt. It also creates the dir up front so a fresh device (or
// one where it never existed) works on the very first action; the lazy
// MkdirAlls at each use site stay as the second layer for the mid-run
// deletion edge case. Package-level maps are all initialized before any
// init() runs, so touching the three sets here is safe.
func init() {
	dir, ok := resolvedOptineConfigDir()
	if !ok {
		log.Println("optine config dir unavailable, os.UserConfigDir() failed; audit, memory and auth paths stay unset")
		return
	}
	// Best-effort: approval seeding below proceeds regardless so a
	// read-only home degrades to lazily-created dirs, not a startup panic.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		log.Printf("optine config dir %q unavailable: %v", dir, err)
	}
	approvedReadDirs[dir] = true
	approvedEditDirs[dir] = true
	approvedWriteDirs[dir] = true

	AuthPath = filepath.Join(dir, "auth.json")

	if err := establishAuthFile(dir); err != nil {
		log.Printf("optine, error occured while initializing the auth.json file, error: %v", err)
	}
}
