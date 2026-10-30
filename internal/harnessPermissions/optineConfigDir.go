package harnessPermissions

import (
	"log"
	"os"
	"path/filepath"
)

// AuthPath is the resolved auth.json path. Empty means the config dir never
// resolved; consumers must fail closed.
var AuthPath string

// resolvedOptineConfigDir returns the app's config dir with symlinks resolved.
func resolvedOptineConfigDir() (string, bool) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", false
	}
	dir := filepath.Join(base, "optine")
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		return real, true
	}
	// Not created yet, or unresolvable: use the lexical path.
	return dir, true
}

// establishAuthFile creates an empty auth.json, leaving an existing file alone.
func establishAuthFile(dir string) error {
	authFile := filepath.Join(dir, "auth.json")
	f, err := os.OpenFile(authFile, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return nil
		}
		return err
	}

	defer f.Close()

	return nil
}

// init pre-approves the app's config dir and creates it up front, so first-run
// audit and memory writes need no approval prompt.
func init() {
	dir, ok := resolvedOptineConfigDir()
	if !ok {
		log.Println("optine config dir unavailable, os.UserConfigDir() failed; audit, memory and auth paths stay unset")
		return
	}
	// Best effort: a read-only home degrades to lazily-created dirs.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		log.Printf("optine config dir %q unavailable: %v", dir, err)
	}

	approvedDirs[dir] = true
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		approvedDirs[real] = true
	}

	AuthPath = filepath.Join(dir, "auth.json")

	if err := establishAuthFile(dir); err != nil {
		log.Printf("optine, error occured while initializing the auth.json file, error: %v", err)
	}
}
