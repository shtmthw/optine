package provider

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type OllamaModel []struct {
	Name string `json:"name"`
}

type OllamaTagsResponse struct {
	Models OllamaModel `json:"models"`
}

type OllamaShowRequest struct {
	Model string `json:"model"`
}

type OllamaShowResponse struct {
	Capabilities []string       `json:"capabilities"`
	Template     string         `json:"template"`
	Parser       string         `json:"parser"`
	ModelInfo    map[string]any `json:"model_info"`
}

var ollamaClient = &http.Client{
	Timeout: 30 * time.Second,
}

func OllamaGetModelData() ([]string, error) {
	resp, err := ollamaClient.Get("http://localhost:11434/api/tags")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {

	case http.StatusOK:
		//continue
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, ErrNeedsKey

	default:
		return nil, fmt.Errorf("unexpected status %d from /api/tags", resp.StatusCode)
	}

	var data OllamaTagsResponse

	err = json.NewDecoder(resp.Body).Decode(&data)
	if err != nil {
		return nil, err
	}

	if len(data.Models) == 0 {
		return nil, fmt.Errorf("no models found/installed")
	}

	modelNames := make([]string, 0, len(data.Models))

	for _, model := range data.Models {
		modelNames = append(modelNames, model.Name)
	}

	return modelNames, nil
}

func OllamaGetModelMetaData(modelName string) (OllamaShowResponse, error) {
	requestBody := OllamaShowRequest{
		Model: modelName,
	}

	body, err := json.Marshal(requestBody)
	if err != nil {
		return OllamaShowResponse{}, err
	}

	resp, err := ollamaClient.Post(
		"http://localhost:11434/api/show",
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return OllamaShowResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return OllamaShowResponse{}, fmt.Errorf(
			"ollama /api/show for %q returned %s",
			modelName,
			resp.Status,
		)
	}

	var info OllamaShowResponse

	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return OllamaShowResponse{}, err
	}

	return info, nil
}
