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

// SaveConnection returns true if an existing connection was updated,
// or false if a new connection was added.
func saveConnection(connection, apiKey string) error {
	connection = strings.ToLower(strings.TrimSpace(connection))
	apiKey = strings.TrimSpace(apiKey)

	if connection == "" || apiKey == "" {
		return fmt.Errorf("provider and API key are required")
	}

	cfg := dataTypes.AuthConfig{
		Connections: make(map[string]dataTypes.Connection),
	}

	// AuthPath is the single resolved location of auth.json, set up by
	// harnessPermissions at startup. No resolver of our own here.
	authPath := harnessPermissions.AuthPath
	if authPath == "" {
		return errors.New("optine config dir unavailable, AuthPath was never resolved")
	}
	dir := filepath.Dir(authPath)

	// Read the existing file, if one exists.
	data, err := os.ReadFile(authPath)
	if err == nil {
		// An empty or whitespace-only file means no connections yet.
		if strings.TrimSpace(string(data)) != "" {
			if err := json.Unmarshal(data, &cfg); err != nil {
				return fmt.Errorf("invalid auth.json: %w", err)
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	// Initialize the map if the JSON had no connections object.
	if cfg.Connections == nil {
		cfg.Connections = make(map[string]dataTypes.Connection)
	}

	// Map assignment updates an existing key or inserts a new one.
	cfg.Connections[connection] = dataTypes.Connection{
		APIKey: apiKey,
	}

	// Convert the updated struct back to JSON.
	updated, err := json.MarshalIndent(cfg, "", "    ")
	if err != nil {
		return err
	}

	//Atomic conversion, Write to a temporary file first, with restricted permissions.
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

	// Replace auth.json with the completed file.

	if err := os.Rename(tmpPath, authPath); err != nil {
		return err
	}

	return nil
}
