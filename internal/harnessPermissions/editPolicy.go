package harnessPermissions

import (
	"bufio"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// MaxFileToolBytes caps model-provided edit and write text before it enters
// history. Previews are capped separately.
const MaxFileToolBytes = 256 * 1024

var ErrEditDenied = errors.New("edit request has been denied by the user")

// editStringArg reads one required string argument.
func editStringArg(arguments map[string]any, name string) (string, error) {
	raw, ok := arguments[name]
	if !ok {
		return "", fmt.Errorf("edit_file: missing %s argument", name)
	}
	s, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("edit_file: %s must be a string, got %T", name, raw)
	}
	return s, nil
}

// editArgs pulls the required edit_file arguments out of the model's request.
func editArgs(arguments map[string]any) (path, oldString, newString string, err error) {
	if path, err = editStringArg(arguments, "path"); err != nil {
		return "", "", "", err
	}
	if strings.TrimSpace(path) == "" {
		return "", "", "", errors.New("edit_file: path cannot be empty")
	}

	if oldString, err = editStringArg(arguments, "old_string"); err != nil {
		return "", "", "", err
	}
	if oldString == "" {
		return "", "", "", errors.New("edit_file: old_string cannot be empty")
	}

	if newString, err = editStringArg(arguments, "new_string"); err != nil {
		return "", "", "", err
	}
	if oldString == newString {
		return "", "", "", errors.New("edit_file: old_string and new_string are identical, nothing to change")
	}
	if len(oldString)+len(newString) > MaxFileToolBytes {
		return "", "", "", fmt.Errorf("edit_file: old_string plus new_string is %d bytes, over the %d byte limit; split into smaller edits", len(oldString)+len(newString), MaxFileToolBytes)
	}

	return path, oldString, newString, nil
}

// additionArgs parses append-mode arguments. There is no old_string to match.
func additionArgs(arguments map[string]any) (path, newString string, err error) {
	if path, err = editStringArg(arguments, "path"); err != nil {
		return "", "", err
	}
	if strings.TrimSpace(path) == "" {
		return "", "", errors.New("edit_file: path cannot be empty")
	}

	if newString, err = editStringArg(arguments, "new_string"); err != nil {
		return "", "", err
	}
	if newString == "" {
		return "", "", errors.New("edit_file: new_string cannot be empty")
	}
	if len(newString) > MaxFileToolBytes {
		return "", "", fmt.Errorf("edit_file: new_string is %d bytes, over the %d byte limit; split into smaller edits", len(newString), MaxFileToolBytes)
	}

	return path, newString, nil
}

// EditAppendFlag reports whether edit_file is in append mode.
func EditAppendFlag(arguments map[string]any) bool {
	appendRaw, ok := arguments["append"]
	if !ok {
		return false
	}
	appendFlag, ok := appendRaw.(bool)
	return ok && appendFlag
}

// editAsk prompts for edit_file. When redact is set, only lengths are logged.
func editAsk(reader *bufio.Reader, path, oldString, newString, prompt string, redact bool) (Answer, error) {
	args := map[string]any{"path": path, "old_string": oldString, "new_string": newString}
	if redact {
		args = map[string]any{"path": path, "old_string_chars": len(oldString), "new_string_chars": len(newString)}
	}
	return AskFunc("edit_file", reader, args, prompt)
}

// EditFilePolicy resolves and approves an edit target. The file must already
// exist; symlinks and hardlinks ask explicitly; sensitive files always ask;
// other directories ask once per process, and approvals are shared with the
// other file tools. Append mode returns an empty oldString and the content to
// append.
func EditFilePolicy(arguments map[string]any, reader *bufio.Reader, isAddition bool) (realPath, oldString, newString string, err error) {
	var modelPath string
	var argumentErr error

	if isAddition {
		modelPath, newString, argumentErr = additionArgs(arguments)
	} else {
		modelPath, oldString, newString, argumentErr = editArgs(arguments)
	}

	if argumentErr != nil {
		return "", "", "", argumentErr
	}

	// Lstat before resolving so a symlink is seen as a symlink.
	linkRemember := false
	wasSymlink := false
	info, lerr := os.Lstat(modelPath)
	switch {
	case os.IsNotExist(lerr):
		return "", "", "", fmt.Errorf("edit_file %q: file does not exist, cannot edit", modelPath)

	case lerr == nil && info.Mode()&os.ModeSymlink != 0:
		wasSymlink = true
		answer, err := editAsk(reader, modelPath, oldString, newString,
			fmt.Sprintf("Wants to edit a symlink file %q. Editing follows the link to its target, which may be outside the workspace. Allow editing it anyway?", modelPath), IsSensitive(modelPath))
		if err != nil {
			return "", "", "", err
		}
		switch answer {
		case Once:
		case Remember:
			linkRemember = true
		default:
			return "", "", "", fmt.Errorf("edit_file %q: %w", modelPath, ErrEditDenied)
		}
	}

	absPath, err := filepath.Abs(modelPath)
	if err != nil {
		return "", "", "", fmt.Errorf("resolving path %q: %w", modelPath, err)
	}
	realPath, err = filepath.EvalSymlinks(absPath)
	if err != nil {
		return "", "", "", fmt.Errorf("resolving path %q: %w", modelPath, err)
	}
	log.Println("edit file path: ", realPath)

	fi, err := os.Stat(realPath)
	if err != nil {
		return "", "", "", fmt.Errorf("edit_file %q: %w", realPath, err)
	}
	if fi.IsDir() {
		return "", "", "", fmt.Errorf("edit_file %q: is a directory, not a file", realPath)
	}

	// Hardlinks can share data outside the workspace, so ask explicitly.
	// Following a symlink was already approved above.
	if !wasSymlink {
		if st, ok := fi.Sys().(*syscall.Stat_t); ok && st.Nlink > 1 {
			answer, err := editAsk(reader, realPath, oldString, newString,
				fmt.Sprintf("Wants to edit %q, which shares its data with %d paths (hardlink). Editing changes them all. Allow it anyway?", realPath, st.Nlink), IsSensitive(realPath))
			if err != nil {
				return "", "", "", err
			}
			switch answer {
			case Once:
			case Remember:
				linkRemember = true
			default:
				return "", "", "", fmt.Errorf("edit_file %q: %w", realPath, ErrEditDenied)
			}
		}
	}

	realDir := filepath.Dir(realPath)

	var prompt string
	redact := IsSensitive(realPath)
	switch {
	case redact: // always asks, even in an approved dir
		prompt = fmt.Sprintf("Wants to edit a sensitive file %q. Allow it?", realPath)
	case !approvedDirs[realDir]:
		if linkRemember {
			// A link "always" also approves its resolved directory.
			approvedDirs[realDir] = true
			return realPath, oldString, newString, nil
		}
		log.Println("edit dir path: ", realDir)
		prompt = fmt.Sprintf("Wants to edit a file. Choosing always permits all file tools and safe bash commands directly in %s for this session.", realDir)
	default: // the whole dir is already approved
		if linkRemember {
			approvedDirs[realDir] = true
		}
		return realPath, oldString, newString, nil
	}
	answer, err := editAsk(reader, realPath, oldString, newString, prompt, redact)
	if err != nil {
		return "", "", "", err
	}
	switch answer {
	case Once:
		return realPath, oldString, newString, nil
	case Remember:
		approvedDirs[realDir] = true
		return realPath, oldString, newString, nil
	default:
		return "", "", "", fmt.Errorf("edit_file %q: %w", realPath, ErrEditDenied)
	}
}
