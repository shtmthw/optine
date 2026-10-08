package harnessMemory

import (
	"errors"
	"io"
	"os"
	"path/filepath"
)

func casualMemoryPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}

	optineDir := filepath.Join(dir, "optine")

	if err := os.MkdirAll(optineDir, 0o700); err != nil {
		return "", err
	}

	return filepath.Join(optineDir, "casualMemory.md"), nil
}

// CasualMemoryPath returns the absolute path of the casual memory file,
// creating the config dir if needed. Exported so prompts can name the exact
// file the model is allowed to edit.
func CasualMemoryPath() (string, error) {
	return casualMemoryPath()
}

func loadCasualMemoryFile() (*os.File, error) {
	path, err := casualMemoryPath()
	if err != nil {
		return nil, err
	}

	file, err := os.OpenFile(
		path,
		os.O_CREATE|os.O_RDWR|os.O_EXCL,
		0o600,
	)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return os.OpenFile(path, os.O_RDWR, 0o600)
		}
		return nil, err
	}

	if _, err := file.WriteString("This is the default memory file.\n"); err != nil {
		_ = file.Close()
		return nil, err
	}

	if _, err := file.Seek(0, io.SeekStart); err != nil {
		_ = file.Close()
		return nil, err
	}

	return file, nil
}

func ReadCasualMemoryFile() (string, error) {
	file, err := loadCasualMemoryFile()

	if err != nil {
		return "", err
	}

	defer file.Close()

	byteData, readErr := io.ReadAll(file)

	if readErr != nil {
		return "", readErr
	}

	return string(byteData), nil
}
