package harnessPermissions

import (
	"io/fs"
	"log"
	"os"
	"path/filepath"
)

// approvedDirs is the process-wide set of directories whose file tools and safe bash commands do not need approval.
var approvedDirs = baseApprovedDirs()

// baseApprovedDirs pre-approves cwd and its child directories.
func baseApprovedDirs() map[string]bool {
	cwd, err := os.Getwd()
	if err != nil {
		log.Println("working directory unavailable, no directory is pre-approved:", err)
		return map[string]bool{}
	}

	// Policies compare against symlink-resolved paths, so the map keys must be
	// resolved too. Without this, launching from a symlinked directory would
	// prompt for files in the real cwd.
	real, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		log.Printf("cannot resolve working directory %q, no directory is pre-approved: %v", cwd, err)
		return map[string]bool{}
	}

	return collectApprovedDirs(real)
}

// collectApprovedDirs returns root and every existing descendant directory.
func collectApprovedDirs(root string) map[string]bool {
	approved := map[string]bool{root: true}

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			log.Printf("cannot inspect path %q: %v", path, err)
			return nil
		}

		if d.IsDir() {
			approved[path] = true
		}

		return nil
	})
	if err != nil {
		log.Printf("failed to walk working directory: %v", err)
	}

	return approved
}

// approvedPath reports whether path itself or its parent directory is in the
// shared approval map.
func approvedPath(path string) bool {
	return approvedDirs[path] || approvedDirs[filepath.Dir(path)]
}
