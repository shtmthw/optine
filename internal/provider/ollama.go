package provider

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
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

type RunningModel struct {
	Name          string `json:"name"`
	ContextLength int    `json:"context_length"`
}

type PSResponse struct {
	Models []RunningModel `json:"models"`
}

var ollamaModelLoader = &http.Client{
	Timeout: 500 * time.Second,
}

// OllamaLoadModel loads the selected model into memory with an empty chat
// request. Ollama returns only after the model is ready, so this can block for
// minutes; the CLI wraps the call in a spinner.
func OllamaLoadModel(selectedModel string) error {
	payload := map[string]any{
		"model":      selectedModel,
		"messages":   []any{},
		"stream":     false,
		"keep_alive": "15m",
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode ollama load request: %w", err)
	}

	resp, err := ollamaModelLoader.Post(
		"http://localhost:11434/api/chat",
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return fmt.Errorf("ollama load request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read ollama load response: %w", err)
	}

	var result struct {
		Error      string `json:"error"`
		DoneReason string `json:"done_reason"`
	}
	decodeErr := json.Unmarshal(raw, &result)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if result.Error != "" {
			return fmt.Errorf("ollama returned %s: %s", resp.Status, result.Error)
		}

		return fmt.Errorf("ollama returned %s: %s", resp.Status, ollamaResponseSnippet(raw))
	}

	if decodeErr != nil {
		return fmt.Errorf("decode ollama load response: %w", decodeErr)
	}

	if result.Error != "" {
		return fmt.Errorf("ollama: %s", result.Error)
	}

	return nil
}

// ollamaResponseSnippet keeps a truncated, readable copy of an unexpected
// response body for the CLI error line.
func ollamaResponseSnippet(raw []byte) string {
	const limit = 512

	snippet := strings.TrimSpace(string(raw))
	if snippet == "" {
		return "empty response body"
	}

	if len(snippet) > limit {
		return snippet[:limit] + "..."
	}

	return snippet
}

// get the currently loaded in memory models of Ollama
func ollamaGetRunningModels() ([]RunningModel, error) {
	resp, err := ollamaClient.Get("http://localhost:11434/api/ps")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama returned status: %s", resp.Status)
	}

	var result PSResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return result.Models, nil
}

// ContextLength reads the running model's context window from /api/ps. Zero
// means Ollama did not report it, so the caller falls back to the default.
func (OllamaShowResponse) ContextLength(selectedModel string) int {
	runningModels, err := ollamaGetRunningModels()
	if err != nil {
		return 0
	}

	for _, modelData := range runningModels {
		if modelData.Name == selectedModel {
			return modelData.ContextLength
		}
	}

	return 0
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
