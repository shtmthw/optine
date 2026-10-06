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

// MaxFileToolBytes caps model-provided texts for edit_file (old+new combined)
// and write_file (content). Full texts enter model history, so bound them;
// previews shown to the user are capped separately in harnessTools.
const MaxFileToolBytes = 256 * 1024

// approvedEditDirs remembers directories the user approved with "always" for
// edit_file this session. Separate from read's approvedDirs so reading a dir
// never implies permission to change it.
var approvedEditDirs = map[string]bool{
	cwd: true,
}

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

// editAsk is AskFunc for edit_file with fixed arguments. redact hides the
// texts (lengths only) so sensitive content never reaches approval logs.
func editAsk(reader *bufio.Reader, path, oldString, newString, prompt string, redact bool) (Answer, error) {
	args := map[string]any{"path": path, "old_string": oldString, "new_string": newString}
	if redact {
		args = map[string]any{"path": path, "old_string_chars": len(oldString), "new_string_chars": len(newString)}
	}
	return AskFunc("edit_file", reader, args, prompt)
}

// EditFilePolicy resolves the model's path and enforces the edit guards:
//   - edit-only: the file must already exist (missing file is an error, not a create)
//   - Lstat on the model-provided path first: a symlink always triggers its own
//     AskFunc, even in an approved dir (a link can point outside the workspace)
//   - sensitive files (.env, .ssh, .gnupg, .password-store) always trigger
//     AskFunc, even in an approved dir
//   - otherwise cwd is pre-approved, any other dir asks once per dir per session
//     via AskFunc, with Remember storing the dir in approvedEditDirs
//
// It returns the resolved real path plus the verified old/new strings.
func EditFilePolicy(arguments map[string]any, reader *bufio.Reader) (realPath, oldString, newString string, err error) {
	modelPath, oldString, newString, err := editArgs(arguments)
	if err != nil {
		return "", "", "", err
	}

	// Lstat the path as the model gave it, before resolving anything. It does
	// not follow the final link, so it sees a symlink itself. Any Lstat error
	// other than "does not exist" falls through, EvalSymlinks below reports it.
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
			// allow this call, continue with the normal checks below
		case Remember:
			linkRemember = true // the resolved dir is stored only if every later check passes
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

	// Hardlinks share their data with another path, which may live outside
	// the workspace (EvalSymlinks never resolves them). Treat like a symlink:
	// ask, since editing mutates the shared inode. Skipped when the model
	// path was itself a symlink: following links was already approved above.
	if !wasSymlink {
		if st, ok := fi.Sys().(*syscall.Stat_t); ok && st.Nlink > 1 {
			answer, err := editAsk(reader, realPath, oldString, newString,
				fmt.Sprintf("Wants to edit %q, which shares its data with %d paths (hardlink). Editing changes them all. Allow it anyway?", realPath, st.Nlink), IsSensitive(realPath))
			if err != nil {
				return "", "", "", err
			}
			switch answer {
			case Once:
				// allow this call, continue with the normal checks below
			case Remember:
				linkRemember = true // the resolved dir is stored only if every later check passes
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
	case !approvedEditDirs[realDir]:
		if linkRemember {
			// Link "always" already approved persistence: store the
			// resolved dir without a second prompt. Sensitive files
			// still went through their own approval above, so a
			// denial there stores nothing.
			approvedEditDirs[realDir] = true
			return realPath, oldString, newString, nil
		}
		log.Println("edit dir path: ", realDir)
		prompt = fmt.Sprintf("Wants to edit a file. Choosing always allows editing any file directly in %s for this session.", realDir)
	default: // the whole dir is already approved this session
		if linkRemember {
			approvedEditDirs[realDir] = true
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
		approvedEditDirs[realDir] = true
		return realPath, oldString, newString, nil
	default:
		return "", "", "", fmt.Errorf("edit_file %q: %w", realPath, ErrEditDenied)
	}
}
