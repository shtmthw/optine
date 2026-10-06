package harnessPermissions

import (
	"bufio"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// approvedWriteDirs remembers directories the user approved with "always" for
// write_file this session. Separate from read's approvedDirs and edit's
// approvedEditDirs so one permission never implies another.
var approvedWriteDirs = map[string]bool{
	cwd: true,
}

var ErrWriteDenied = errors.New("write request has been denied by the user")

// writeArgs pulls the required write_file arguments out of the model's request.
// content may be empty (creates an empty file) but the key must be present.
func writeArgs(arguments map[string]any) (path, content string, err error) {
	rawPath, ok := arguments["path"]
	if !ok {
		return "", "", errors.New("write_file: missing path argument")
	}
	path, ok = rawPath.(string)
	if !ok {
		return "", "", fmt.Errorf("write_file: path must be a string, got %T", rawPath)
	}
	if strings.TrimSpace(path) == "" {
		return "", "", errors.New("write_file: path cannot be empty")
	}

	rawContent, ok := arguments["content"]
	if !ok {
		return "", "", errors.New("write_file: missing content argument")
	}
	content, ok = rawContent.(string)
	if !ok {
		return "", "", fmt.Errorf("write_file: content must be a string, got %T", rawContent)
	}
	if len(content) > MaxFileToolBytes {
		return "", "", fmt.Errorf("write_file: content is %d bytes, over the %d byte limit; split into smaller writes", len(content), MaxFileToolBytes)
	}

	return path, content, nil
}

// WriteFilePolicy resolves the model's path and enforces the write guards:
//   - create-only: an existing path (file, dir, or symlink) is an error, never
//     an overwrite (use edit_file). Symlinks are a hard deny, never an ask.
//   - any symlink in an existing parent component is a hard deny (writing under
//     it would land outside the approved-looking dir).
//   - missing parents are created later by execution, after approval; approval
//     is decided on the resolved target dir (existing prefix resolved, missing
//     suffix lexical).
//   - sensitive files (.env, .ssh, .gnupg, .password-store) always trigger
//     AskFunc, even in an approved dir.
//   - otherwise cwd is pre-approved, any other dir asks once per dir per session
//     via AskFunc, with Remember storing the dir in approvedWriteDirs.
//
// It returns the resolved absolute target path plus the model content.
func WriteFilePolicy(arguments map[string]any, reader *bufio.Reader) (realPath, content string, err error) {
	modelPath, content, err := writeArgs(arguments)
	if err != nil {
		return "", "", err
	}

	// Target check on the path as the model gave it. Lstat sees a symlink
	// itself instead of following it.
	if info, lerr := os.Lstat(modelPath); lerr == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return "", "", fmt.Errorf("write_file %q: target is a symlink, writing through symlinks is not allowed", modelPath)
		}
		if info.IsDir() {
			return "", "", fmt.Errorf("write_file %q: target is a directory, cannot write a file here", modelPath)
		}
		return "", "", fmt.Errorf("write_file %q: file already exists, use edit_file to change it", modelPath)
	} else if !os.IsNotExist(lerr) {
		return "", "", fmt.Errorf("write_file %q: %w", modelPath, lerr)
	}

	absPath, err := filepath.Abs(modelPath)
	if err != nil {
		return "", "", fmt.Errorf("resolving path %q: %w", modelPath, err)
	}

	// Walk the existing parent chain for symlinks. Missing trailing
	// components cannot be links, so only existing prefixes are checked.
	dir := filepath.Dir(absPath)
	if err := denySymlinkParents(dir); err != nil {
		return "", "", err
	}

	realPath, err = resolveWriteTarget(absPath)
	if err != nil {
		return "", "", err
	}
	log.Println("write file path: ", realPath)

	if fi, lerr := os.Lstat(realPath); lerr == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			return "", "", fmt.Errorf("write_file %q: target is a symlink, writing through symlinks is not allowed", realPath)
		}
		return "", "", fmt.Errorf("write_file %q: file already exists, use edit_file to change it", realPath)
	}

	realDir := filepath.Dir(realPath)

	if IsSensitive(realPath) {
		// Sensitive files always need approval, even in an approved dir.
		answer, aerr := AskFunc(
			"write_file",
			reader,
			map[string]any{"path": realPath},
			fmt.Sprintf("Wants to create a sensitive file %q. Allow it?", realPath),
		)
		if aerr != nil {
			return "", "", aerr
		}
		switch answer {
		case Once:
			return realPath, content, nil
		case Remember:
			approvedWriteDirs[realDir] = true
			return realPath, content, nil
		default:
			return "", "", fmt.Errorf("write_file %q: %w", realPath, ErrWriteDenied)
		}
	}

	// The entire directory has already been approved for writes this session.
	if approvedWriteDirs[realDir] {
		return realPath, content, nil
	}

	log.Println("write dir path: ", realDir)

	answer, err := AskFunc(
		"write_file",
		reader,
		map[string]any{"path": realPath},
		fmt.Sprintf("Wants to create a file. Choosing always allows creating any file directly in %s for this session.", realDir),
	)
	if err != nil {
		return "", "", err
	}

	switch answer {
	case Once:
		return realPath, content, nil
	case Remember:
		approvedWriteDirs[realDir] = true
		return realPath, content, nil
	default:
		return "", "", fmt.Errorf(
			"write_file %q: %w",
			realPath,
			ErrWriteDenied,
		)
	}
}

// denySymlinkParents returns an error if any existing level of dir (or dir
// itself) is a symlink. Missing levels are skipped: they cannot be links.
func denySymlinkParents(dir string) error {
	cur := dir
	for {
		if info, err := os.Lstat(cur); err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("write_file: parent %q is a symlink, writing through symlinks is not allowed", cur)
			}
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("write_file: checking parent %q: %w", cur, err)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return nil
		}
		cur = parent
	}
}

// resolveWriteTarget resolves the deepest existing ancestor of absPath with
// EvalSymlinks and appends the missing suffix lexically, so approval and
// creation operate on the real location.
func resolveWriteTarget(absPath string) (string, error) {
	cur, rest := absPath, ""
	for {
		if real, err := filepath.EvalSymlinks(cur); err == nil {
			if rest == "" {
				return real, nil
			}
			return filepath.Join(real, rest), nil
		}
		if _, err := os.Lstat(cur); err == nil {
			return "", fmt.Errorf("cannot resolve %q (dangling or looping symlink?)", cur)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", fmt.Errorf("cannot resolve %q", absPath)
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}
