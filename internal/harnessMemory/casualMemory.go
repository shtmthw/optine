package harnessMemory

import (
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/mattthew/optine/internal/harnessPermissions"
)

// CasualMemoryPath returns the memory file path, creating the config dir.
func CasualMemoryPath() (string, error) {
	if harnessPermissions.AuthPath == "" {
		return "", errors.New("optine config dir unavailable, AuthPath was never resolved")
	}

	dir := filepath.Dir(harnessPermissions.AuthPath)

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}

	return filepath.Join(dir, "casualMemory.md"), nil
}

func loadCasualMemoryFile() (*os.File, error) {
	path, err := CasualMemoryPath()
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

// ReadCasualMemoryFile returns the contents of the memory file.
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
