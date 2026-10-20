package harnessPermissions

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/mattthew/optine/internals/dataTypes"

	"strings"
)

func GetAPIKey(provider string) (string, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))

	// AuthPath is the single resolved location of auth.json, set up by
	// harnessPermissions at startup. No resolver of our own here.
	if AuthPath == "" {
		return "", errors.New("optine config dir unavailable, AuthPath was never resolved")
	}

	data, err := os.ReadFile(AuthPath)
	if err != nil {
		return "", fmt.Errorf("reading auth.json: %w", err)
	}

	var cfg dataTypes.AuthConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return "", fmt.Errorf("parsing auth.json: %w", err)
	}

	connection, exists := cfg.Connections[provider]
	if !exists || strings.TrimSpace(connection.APIKey) == "" {
		return "", fmt.Errorf("no API key configured for %q", provider)
	}

	return connection.APIKey, nil
}
