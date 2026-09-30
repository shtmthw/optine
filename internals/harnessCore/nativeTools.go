package harnessCore

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/mattthew/optine/internals/dataTypes"
	"github.com/mattthew/optine/internals/harnessDispatch"
	"github.com/mattthew/optine/internals/systemPrompts"
)

func produceReqBody(messages []*dataTypes.NativeTooltypeMessage, modelName string) dataTypes.NativeToolChatRequest {

	tools := []dataTypes.NativeTypeTool{dataTypes.WebSearch}

	var requestBody = dataTypes.NativeToolChatRequest{
		Model:    modelName,
		Messages: messages,
		Stream:   false,
		Tools:    tools,
	}

	return requestBody
}

var providerHTTPClient = &http.Client{
	Timeout: 320 * time.Second,
}

func inferenceCall(ctx context.Context, reqBody dataTypes.NativeToolChatRequest, providerURL string) (*dataTypes.NativeTooltypeMessage, error) {

	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshalling provider request: %w", err)
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		providerURL,
		bytes.NewReader(data),
	)
	if err != nil {
		return nil, fmt.Errorf("building provider request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := providerHTTPClient.Do(req) // reused from gemma.go
	if err != nil {
		return nil, fmt.Errorf("calling provider: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf(
			"provider returned HTTP %d: %s",
			resp.StatusCode,
			strings.TrimSpace(string(errBody)),
		)
	}

	var result dataTypes.NativeToolChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding provider response: %w", err)
	}

	return &result.Message, nil
}

func nativeToolAgentCall(ctx context.Context, reader *bufio.Reader, provider string, modelName string, userMessage string) (string, error) {
	ollamaURL := "http://localhost:11434/api/chat"
	switch provider {

	case "Ollama":
		messages := []*dataTypes.NativeTooltypeMessage{
			{
				Role:    "system",
				Content: systemPrompts.NativeToolSystemPrompt(),
			},
			{
				Role:    "user",
				Content: userMessage,
			},
		}

		for range 12 {
			reqBody := produceReqBody(messages, modelName)

			reply, err := inferenceCall(ctx, reqBody, ollamaURL)
			if err != nil {
				return "", err
			}

			log.Println("inference reply:", reply)

			if len(reply.ToolCalls) == 0 {
				if strings.TrimSpace(reply.Content) == "" {
					return "", errors.New("provider returned an empty final answer")
				}
				return reply.Content, nil
			}

			// Keep the assistant's tool-call message in history.
			messages = append(messages, reply)

			for _, tc := range reply.ToolCalls {
				if tc.Function.Name != "web_search" {
					messages = append(messages, &dataTypes.NativeTooltypeMessage{
						Role:    "tool",
						Content: fmt.Sprintf("unknown tool %q requested", tc.Function.Name),
					})
					continue
				}

				result, err := harnessDispatch.Dispatch(ctx, reader, &dataTypes.AIResponse{
					Type:      "tool_call",
					Tool:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				})
				if err != nil {
					result = fmt.Sprintf("Tool execution failed: %v", err)
				}

				messages = append(messages, &dataTypes.NativeTooltypeMessage{
					Role:    "tool",
					Content: result,
				})
			}
		}

	case "vLLM":
		log.Println("under contruction")
		return "", nil
	default:
		return "", fmt.Errorf("unsupported provider %q", provider)
	}

	return "", errors.New("maximum native tool calls exceeded")
}
