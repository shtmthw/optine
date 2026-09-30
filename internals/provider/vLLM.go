package provider

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// types and helpers
type VLLMModelsResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

type VLLMErrorResponse struct {
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

var vllmClient = &http.Client{
	Timeout: 200 * time.Second,
}

var ErrNeedsKey = errors.New("server requires an API key")

func bodySnippet(r io.Reader) string {
	b, _ := io.ReadAll(io.LimitReader(r, 512))
	return string(b)
}

// actual functions
func VLLMGetModelData() (string, error) {
	resp, err := http.Get("http://localhost:8000/v1/models")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {

	case http.StatusOK:
		//continue
	case http.StatusUnauthorized, http.StatusForbidden:
		return "", ErrNeedsKey

	default:
		return "", fmt.Errorf("unexpected status %d from /v1/models", resp.StatusCode)
	}

	var data VLLMModelsResponse

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", err
	}

	if len(data.Data) == 0 {
		return "", fmt.Errorf("no model loaded")
	}

	modelName := data.Data[0].ID

	return modelName, nil
}

// rules:
// false and err means do not conitune to the agent loop,
// false and nil means tool calling is not native,
// true and nil means tool calling is native,
// truen and err does not exist.
func VLLMRunSmokeTest(modelName string) (bool, error) {

	// dummy payload
	payload := map[string]any{
		"model": modelName,
		"messages": []map[string]string{
			{
				"role":    "user",
				"content": `Call the get_test_value tool with key "probe".`,
			},
		},
		"tools": []map[string]any{
			{
				"type": "function",
				"function": map[string]any{
					"name":        "get_test_value",
					"description": "Returns a test value for a key.",
					"parameters": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"key": map[string]string{
								"type": "string",
							},
						},
						"required": []string{"key"},
					},
				},
			},
		},
		"tool_choice": "auto",
		"temperature": 0,
		"max_tokens":  256,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return false, err
	}

	resp, err := vllmClient.Post(
		"http://localhost:8000/v1/chat/completions",
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case 400:
		raw, err := io.ReadAll(resp.Body)
		if err != nil {
			return false, fmt.Errorf("could not read vLLM error response: %w", err)
		}

		log.Printf("vLLM 400 body: %s", raw)

		var vllmErr VLLMErrorResponse
		if err := json.Unmarshal(raw, &vllmErr); err != nil {
			return false, fmt.Errorf("could not decode vLLM error response: %w", err)
		}

		if strings.Contains(vllmErr.Error.Message, "requires --enable-auto-tool-choice") &&
			strings.Contains(vllmErr.Error.Message, "--tool-call-parser") {
			// specifically means auto tool calling isn't configured
			return false, nil
		}

		// A different 400 is NOT evidence that tools are disabled.
		return false, fmt.Errorf("vLLM rejected smoke test: %s", vllmErr.Error.Message)

	case 401, 403:
		// key needed: can't tell anything about tools yet
		return false, ErrNeedsKey

	case 422:
		// malformed request
		return false, fmt.Errorf("malformed smoke-test request: %s", bodySnippet(resp.Body))

	case 200:
		// deep check: did a structured tool call actually come back?
		var r struct {
			Choices []struct {
				Message struct {
					ToolCalls []struct {
						Function struct {
							Name string `json:"name"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"message"`
			} `json:"choices"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
			return false, fmt.Errorf("could not decode smoke-test response: %w", err) // 200 but unusable body
		}
		if len(r.Choices) > 0 &&
			len(r.Choices[0].Message.ToolCalls) > 0 &&
			r.Choices[0].Message.ToolCalls[0].Function.Name == "get_test_value" {
			return true, nil // native tools work
		}
		return false, nil // 200 but no tool_calls -> envelope

	default:
		// 404, 429, 5xx, etc.: the test couldn't run, so don't downgrade silently
		return false, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, bodySnippet(resp.Body))
	}

}
