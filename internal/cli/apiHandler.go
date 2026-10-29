package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"path/filepath"
	"strings"

	"github.com/mattthew/optine/internal/dataTypes"
	"github.com/mattthew/optine/internal/harnessPermissions"
)

// saveConnection adds or updates a provider API key in auth.json.
func saveConnection(connection, apiKey string) error {
	connection = strings.ToLower(strings.TrimSpace(connection))
	apiKey = strings.TrimSpace(apiKey)

	if connection == "" || apiKey == "" {
		return fmt.Errorf("provider and API key are required")
	}

	cfg := dataTypes.AuthConfig{
		Connections: make(map[string]dataTypes.Connection),
	}

	authPath := harnessPermissions.AuthPath
	if authPath == "" {
		return errors.New("optine config dir unavailable, AuthPath was never resolved")
	}
	dir := filepath.Dir(authPath)

	data, err := os.ReadFile(authPath)
	if err == nil {
		if strings.TrimSpace(string(data)) != "" {
			if err := json.Unmarshal(data, &cfg); err != nil {
				return fmt.Errorf("invalid auth.json: %w", err)
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	if cfg.Connections == nil {
		cfg.Connections = make(map[string]dataTypes.Connection)
	}

	cfg.Connections[connection] = dataTypes.Connection{
		APIKey: apiKey,
	}

	updated, err := json.MarshalIndent(cfg, "", "    ")
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".auth-*.tmp")
	if err != nil {
		return err
	}

	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}

	if _, err := tmp.Write(updated); err != nil {
		tmp.Close()
		return err
	}

	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}

	if err := tmp.Close(); err != nil {
		return err
	}

	if err := os.Rename(tmpPath, authPath); err != nil {
		return err
	}

	return nil
}
