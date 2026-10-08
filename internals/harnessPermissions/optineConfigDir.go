package harnessPermissions

import (
	"log"
	"os"
	"path/filepath"
)

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
	// Not yet created (it is MkdirAll'd lazily on first audit/memory use)
	// or otherwise unresolvable: fall back to the lexical path, which still
	// matches once no symlink sits anywhere above it (the common case).
	return dir, true
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
}
