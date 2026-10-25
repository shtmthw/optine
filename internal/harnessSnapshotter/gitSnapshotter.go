package harnessSnapshotter

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// GitAvailable reports whether a working git is installed. It runs git rather
// than only looking it up on the PATH, because on some systems a "git" stub
// exists that fails until developer tools are installed.
func GitAvailable() bool {
	return exec.Command("git", "--version").Run() == nil
}

// GitTrack stores the current state of workdir under id.
func GitTrack(workdir, storeDir, id string) error {
	workdir, err := filepath.Abs(workdir)
	if err != nil {
		return err
	}
	storeDir, err = filepath.Abs(storeDir)
	if err != nil {
		return err
	}
	if err := gitInit(workdir, storeDir); err != nil {
		return err
	}

	// A failed add must fail the capture; never write a tree for a half-staged
	// directory.
	if _, err := git(workdir, storeDir, "add", "-A"); err != nil {
		return err
	}
	tree, err := git(workdir, storeDir, "write-tree")
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(storeDir, "trees", id), []byte(strings.TrimSpace(tree)), 0o600)
}

// GitRevert restores every path that differs between the beforeID and afterID
// snapshots to its state in beforeID: changed or deleted files are checked out
// of the "before" tree, files created in between are removed. It returns the
// paths it touched. Redo is the same call with a "post-undo" snapshot, as in
// Revert.
func GitRevert(workdir, storeDir, beforeID, afterID string) ([]string, error) {
	workdir, err := filepath.Abs(workdir)
	if err != nil {
		return nil, err
	}
	storeDir, err = filepath.Abs(storeDir)
	if err != nil {
		return nil, err
	}
	before, err := readTree(storeDir, beforeID)
	if err != nil {
		return nil, err
	}
	after, err := readTree(storeDir, afterID)
	if err != nil {
		return nil, err
	}

	// Output with -z is: status NUL path NUL status NUL path NUL ...
	// A = only in "after" (created), D = only in "before" (deleted),
	// M / T = content or type changed.
	out, err := git(workdir, storeDir, "diff-tree", "-r", "--no-renames", "--name-status", "-z", before, after)
	if err != nil {
		return nil, err
	}
	fields := strings.Split(out, "\x00")

	var created, changed []string
	for i := 0; i+1 < len(fields); i += 2 {
		status, rel := fields[i], fields[i+1]
		if _, err := localPath(workdir, rel); err != nil {
			return nil, err // refuse anything that would escape workdir
		}
		if status == "A" {
			created = append(created, rel)
		} else {
			changed = append(changed, rel)
		}
	}

	var revertedPaths []string

	// Created since "before": delete them.
	for _, rel := range created {
		path, err := localPath(workdir, rel)
		if err != nil {
			return revertedPaths, err
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return revertedPaths, err
		}
		revertedPaths = append(revertedPaths, rel)
	}

	// Changed or deleted since "before": check the old versions out.
	if len(changed) > 0 {
		// git checkout recreates files with 0666/0777 minus the umask, which
		// would loosen a 0600 file to 0644. Remember the modes of files that
		// still exist and put them back afterwards, like writeAtomic does.
		modes := map[string]os.FileMode{}
		for _, rel := range changed {
			path, err := localPath(workdir, rel)
			if err != nil {
				return revertedPaths, err
			}
			if info, err := os.Stat(path); err == nil {
				modes[path] = info.Mode().Perm()
			}
		}

		args := append([]string{"checkout", before, "--"}, changed...)
		if _, err := git(workdir, storeDir, args...); err != nil {
			return revertedPaths, err
		}

		for path, mode := range modes {
			if err := os.Chmod(path, mode); err != nil {
				return revertedPaths, err
			}
		}
		revertedPaths = append(revertedPaths, changed...)
	}

	sort.Strings(revertedPaths)
	return revertedPaths, nil
}

// git runs one git command against the private repo and returns its stdout.
// The flags make the result independent of the user's git setup:
//   - --literal-pathspecs: paths are taken literally. Without it a file named
//     ":x.txt" is parsed as pathspec magic, so git would silently restore
//     "x.txt" instead and still exit 0
//   - core.autocrlf=false and no global/system config or global ignore file,
//     so the user's own settings can't change or hide any file
func git(workdir, storeDir string, args ...string) (string, error) {
	base := []string{
		"--git-dir", filepath.Join(storeDir, "git"),
		"--work-tree", workdir,
		"--literal-pathspecs",
		"-c", "core.autocrlf=false",
		"-c", "core.excludesFile=" + os.DevNull,
	}
	cmd := exec.Command("git", append(base, args...)...)
	cmd.Dir = workdir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// gitInit creates the private repo on first use; later calls do nothing.
func gitInit(workdir, storeDir string) error {
	gitDir := filepath.Join(storeDir, "git")
	if _, err := os.Stat(filepath.Join(gitDir, "HEAD")); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(gitDir, 0o700); err != nil {
		return err
	}
	if _, err := git(workdir, storeDir, "init", "--quiet"); err != nil {
		return err
	}
	// Snapshot trees are not referenced by any branch, so keep git from ever
	// garbage-collecting them on its own.
	if _, err := git(workdir, storeDir, "config", "gc.auto", "0"); err != nil {
		return err
	}

	// Same skip list as the content-addressed backend, plus the store itself if
	// it lives inside workdir.
	var lines []string
	for name := range skipDirs {
		lines = append(lines, name+"/")
	}
	sort.Strings(lines)
	if rel, err := filepath.Rel(workdir, storeDir); err == nil && filepath.IsLocal(rel) {
		lines = append(lines, "/"+filepath.ToSlash(rel)+"/")
	}
	exclude := []byte(strings.Join(lines, "\n") + "\n")
	if err := writeAtomic(filepath.Join(gitDir, "info", "exclude"), exclude, 0o600); err != nil {
		return err
	}

	// info/attributes beats any .gitattributes in the project. Turning off
	// text/eol conversion, filters (e.g. git-lfs) and ident means the bytes
	// stored and restored are exactly the bytes on disk.
	attributes := []byte("* -text -eol -filter -ident -working-tree-encoding\n")
	return writeAtomic(filepath.Join(gitDir, "info", "attributes"), attributes, 0o600)
}

func readTree(storeDir, id string) (string, error) {
	data, err := os.ReadFile(filepath.Join(storeDir, "trees", id))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}
