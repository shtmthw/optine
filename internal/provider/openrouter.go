package provider

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/mattthew/optine/internal/dataTypes"
)

var AllOpenrouterModels []dataTypes.OpenRouterModel
var globalRemoteClient = &http.Client{
	Timeout: 25 * time.Second,
}

func ValidateOpenRouterKey(apiKey string) (dataTypes.OpenRouterModel, error) {
	req, err := http.NewRequest(
		http.MethodGet,
		"https://openrouter.ai/api/v1/key",
		nil,
	)
	if err != nil {
		return dataTypes.OpenRouterModel{}, err
	}

	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := globalRemoteClient.Do(req)
	if err != nil {
		return dataTypes.OpenRouterModel{}, fmt.Errorf("openrouter request failed: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		allModels, err := getOpenRouterConfigData(apiKey)
		if err != nil {
			return dataTypes.OpenRouterModel{}, err
		}
		for _, model := range allModels {
			fmt.Println(model.ID)
		}
		AllOpenrouterModels = allModels
		return allModels[0], nil

	case http.StatusUnauthorized:
		return dataTypes.OpenRouterModel{}, fmt.Errorf("invalid OpenRouter API key")

	case http.StatusForbidden:
		return dataTypes.OpenRouterModel{}, fmt.Errorf("OpenRouter denied access")

	default:
		return dataTypes.OpenRouterModel{}, fmt.Errorf("OpenRouter returned unexpected status: %s", resp.Status)
	}
}

func getOpenRouterConfigData(apiKey string) ([]dataTypes.OpenRouterModel, error) {
	req, err := http.NewRequest(
		http.MethodGet,
		"https://openrouter.ai/api/v1/models/user",
		nil,
	)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := globalRemoteClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("OpenRouter request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("OpenRouter returned %s: %s",
			resp.Status, strings.TrimSpace(string(body)))
	}

	var result dataTypes.OpenRouterModelsResponse

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding OpenRouter catalog: %w", err)
	}

	if len(result.Data) == 0 {
		return nil, fmt.Errorf("OpenRouter returned an empty model catalog")
	}

	return result.Data, nil
}
