package harnessPermissions

import (
	"bufio"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"strings"
)

var approvedDirs = make(map[string]bool)
var ErrReadDenied = errors.New("read request has been denied by the user")

// pathArgument pulls the required path out of the model's read_file arguments.
func pathArgument(arguments map[string]any) (string, error) {
	rawPath, ok := arguments["path"]
	if !ok {
		return "", errors.New("read_file: missing path argument")
	}

	path, ok := rawPath.(string)
	if !ok {
		return "", fmt.Errorf("read_file: path must be a string, got %T", rawPath)
	}

	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("read_file: path cannot be empty")
	}

	return path, nil
}

func ReadFilePolicy(arguments map[string]any, reader *bufio.Reader) (string, error) {
	path, err := pathArgument(arguments)
	if err != nil {
		return "", err
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolving path %q: %w", path, err)
	}

	realPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		return "", fmt.Errorf("resolving path %q: %w", path, err)
	}
	log.Println("file path: ", realPath)

	realDir := filepath.Dir(realPath)

	// The entire directory has already been approved for this session.
	if approvedDirs[realDir] {
		return realPath, nil
	}

	log.Println("Dir path: ", realDir)

	answer, err := Ask(
		"read_file",
		reader,
		map[string]any{"path": realPath},
		fmt.Sprintf("Wants to read file. Choosing always allows reading any file directly in %s for this session.", realDir),
	)
	if err != nil {
		return "", err
	}

	switch answer {
	case Once:
		// Allow this file once.
		return realPath, nil

	case Remember:
		// Allow read_file access to this directory for the rest of
		// the current session.
		approvedDirs[realDir] = true
		return realPath, nil

	default:
		return "", fmt.Errorf(
			"read_file %q: %w",
			realPath,
			ErrReadDenied,
		)
	}
}
