package harnessSnapshotter

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// Directories we never snapshot. A real version should read .gitignore too.
var skipDirs = map[string]bool{
	// Version control
	".git": true,

	// Dependencies
	"node_modules":     true,
	"bower_components": true,

	// Build outputs
	"build":    true,
	"dist":     true,
	"target":   true,
	"coverage": true,

	// Framework build caches
	".next":         true,
	".nuxt":         true,
	".svelte-kit":   true,
	".turbo":        true,
	".parcel-cache": true,
	".vite":         true,

	// General caches
	".cache":        true,
	"__pycache__":   true,
	".pytest_cache": true,
	".mypy_cache":   true,
	".ruff_cache":   true,

	// Python virtual environments
	".venv": true,
	"venv":  true,

	// Platform-specific dependencies
	"Pods": true,
}

// Track stores the current state of workdir under id.
func Track(workdir, storeDir, id string) error {
	manifest, err := scan(workdir, storeDir)
	if err != nil {
		return err
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(storeDir, "manifests", id+".json"), data, 0o600)
}

// Revert restores every path that differs between the beforeID and afterID
// snapshots to its state in beforeID: changed or deleted files get their old
// bytes back, files created in between are removed. It returns the paths it
// touched. Paths that are identical in both snapshots (including anything the
// agent never touched) are left alone.
func Revert(workdir, storeDir, beforeID, afterID string) ([]string, error) {
	before, err := loadManifest(storeDir, beforeID)
	if err != nil {
		return nil, err
	}
	after, err := loadManifest(storeDir, afterID)
	if err != nil {
		return nil, err
	}

	var revertedPaths []string

	// Changed or deleted since "before": put the old bytes back.
	for rel, hash := range before {
		if after[rel] == hash {
			continue
		}
		path, err := localPath(workdir, rel)
		if err != nil {
			return revertedPaths, err
		}
		data, err := os.ReadFile(filepath.Join(storeDir, "objects", hash))
		if err != nil {
			return revertedPaths, err
		}

		// writeAtomic would create missing parent dirs as 0700, which is wrong
		// for a project tree, so make them first.
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return revertedPaths, err
		}
		if err := writeAtomic(path, data, 0o644); err != nil {
			return revertedPaths, err
		}
		revertedPaths = append(revertedPaths, rel)
	}

	// Created since "before": delete them.
	for rel := range after {
		if _, existed := before[rel]; existed {
			continue
		}
		path, err := localPath(workdir, rel)
		if err != nil {
			return revertedPaths, err
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return revertedPaths, err
		}
		revertedPaths = append(revertedPaths, rel)
	}

	sort.Strings(revertedPaths)
	return revertedPaths, nil
}

// scan walks workdir, stores each regular file as a blob, and returns the
// manifest (relative path with forward slashes -> hash).
func scan(workdir, storeDir string) (map[string]string, error) {
	root, err := filepath.Abs(workdir)
	if err != nil {
		return nil, err
	}
	absStore, err := filepath.Abs(storeDir)
	if err != nil {
		return nil, err
	}

	manifest := map[string]string{}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err // a failed capture must be loud, never silently partial
		}
		if d.IsDir() {
			if path == absStore || (path != root && skipDirs[d.Name()]) {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil // skip symlinks, sockets, devices
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		hash := hex.EncodeToString(sum[:])

		blob := filepath.Join(storeDir, "objects", hash)
		if _, err := os.Stat(blob); err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			if err := writeAtomic(blob, data, 0o600); err != nil {
				return err
			}
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		manifest[filepath.ToSlash(rel)] = hash
		return nil
	})
	if err != nil {
		return nil, err
	}
	return manifest, nil
}

func loadManifest(storeDir, id string) (map[string]string, error) {
	data, err := os.ReadFile(filepath.Join(storeDir, "manifests", id+".json"))
	if err != nil {
		return nil, err
	}
	manifest := map[string]string{}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, err
	}
	return manifest, nil
}

// localPath joins a manifest path onto workdir, refusing anything that would
// escape it (e.g. a tampered manifest containing "../").
func localPath(workdir, rel string) (string, error) {
	rel = filepath.FromSlash(rel)
	if !filepath.IsLocal(rel) {
		return "", fmt.Errorf("snapshot: path escapes workdir: %s", rel)
	}
	return filepath.Join(workdir, rel), nil
}

// writeAtomic writes to a temp file and renames it into place, so a crash
// can't leave a half-written blob or manifest behind.
//
// If path already exists, the new file keeps its permission bits (so an
// executable stays executable). Otherwise it gets defaultPerm. os.CreateTemp
// always makes 0600 files, so without this step every replaced file would end
// up 0600.
func writeAtomic(path string, data []byte, defaultPerm os.FileMode) error {
	perm := defaultPerm
	info, err := os.Stat(path)
	if err == nil {
		perm = info.Mode().Perm()
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // harmless after a successful rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// Chmod, not a mode passed at creation: Chmod ignores the umask, so the
	// bits come out exactly as they were.
	if err := os.Chmod(tmp.Name(), perm); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
