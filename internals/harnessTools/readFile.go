package harnessTools

import (
	"fmt"
	"os"
)

func ReadFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading file %q: %w", path, err)
	}

	return string(data), nil
}
