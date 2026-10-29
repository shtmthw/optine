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

// approvedWriteDirs is the per-session "always" set for write_file.
var approvedWriteDirs = baseApprovedDirs()

var ErrWriteDenied = errors.New("write request has been denied by the user")

// writeArgs parses write_file arguments. Content may be empty, but the key
// must be present.
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

// WriteFilePolicy resolves and approves a new file. It never overwrites;
// symlinks anywhere on the target path are denied; sensitive files always ask;
// other directories ask once per session.
func WriteFilePolicy(arguments map[string]any, reader *bufio.Reader) (realPath, content string, err error) {
	modelPath, content, err := writeArgs(arguments)
	if err != nil {
		return "", "", err
	}

	// Lstat so a symlink target is detected instead of followed.
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

	// Missing trailing components cannot be links.
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

// denySymlinkParents rejects a symlink anywhere on an existing parent path.
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

// resolveWriteTarget resolves the deepest existing ancestor of absPath and
// appends the missing suffix lexically.
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
