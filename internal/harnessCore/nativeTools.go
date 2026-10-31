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

	"github.com/mattthew/optine/internal/dataTypes"

	"github.com/mattthew/optine/internal/harnessDispatch"
	"github.com/mattthew/optine/internal/harnessPermissions"
	"github.com/mattthew/optine/internal/harnessSession"
)

var ErrMalformedJSON = fmt.Errorf("the model sent a malformed JSON, skip tool calls and tell the model to retry its previous task")

var (
	ollamaChatURL = "http://localhost:11434/api/chat"
	vLLMChatURL   = "http://localhost:8000/v1/chat/completions"
)

// MaxTurns caps the number of inference calls in one run.
const MaxTurns = 55

// baseTurns is the starting turn budget.
const baseTurns = 10

const (
	maxErrorBodyBytes = 4 << 10  // cap on provider error text read and surfaced
	maxDrainBytes     = 64 << 10 // cap on leftover bytes drained to allow connection reuse
)

// ErrRateLimited is matched (via errors.Is) by any HTTP 429 from the provider.
var ErrRateLimited = errors.New("provider rate limit exceeded")

// extendTurns grants one extra turn without exceeding MaxTurns.
func extendTurns(turns int) int {
	if turns < MaxTurns {
		return turns + 1
	}

	return turns
}

// maxBadBodies bounds retries after malformed provider responses.
const maxBadBodies = 5

const malformedRetryNotice = "Your last reply wasn't usable JSON — re-issue your previous turn, exactly as before."

// handleMalformedBody records one malformed provider body against budget.
func handleMalformedBody(badBodies *int, err error) (correction string, abort error) {
	*badBodies++
	if *badBodies > maxBadBodies {
		return "", fmt.Errorf("provider sent %d malformed bodies in a row, aborting: %w", *badBodies, err)
	}
	return malformedRetryNotice, nil
}

var (
	ErrMaxInferenceCalls = errors.New("maximum inference calls exceeded without an answer")
	ErrEmptyFinalAnswer  = errors.New("provider returned an empty final answer")
	ErrNoChoices         = errors.New("provider returned no choices")
)

var providerHTTPClient = &http.Client{
	Timeout: 999 * time.Second,
}

// nativeToolAgentCall dispatches to the loop for the selected provider.
func nativeToolAgentCall(ctx context.Context, sess *harnessSession.Session, reader *bufio.Reader, provider string, modelName string) (string, error) {
	switch provider {
	case "Ollama":
		return ollamaToolLoop(ctx, reader, modelName, sess)

	case "vLLM":
		return openAiToolLoop(ctx, reader, modelName, sess, "http://localhost:8000/v1/chat/completions", "")

	case "Openrouter":
		key, err := harnessPermissions.GetAPIKey("openrouter")
		if err != nil {
			return "", err
		}
		return openAiToolLoop(ctx, reader, modelName, sess, "https://openrouter.ai/api/v1/chat/completions", key)

	default:
		return "", fmt.Errorf("unsupported provider %q", provider)
	}
}

func ollamaToolLoop(ctx context.Context, reader *bufio.Reader, modelName string, sess *harnessSession.Session) (string, error) {
	history := sess.Ollama()
	turns := baseTurns
	badBodies := 0
	for turn := 0; turn < turns; turn++ {
		var response dataTypes.NativeToolChatResponse

		err := postJSON(ctx, ollamaChatURL, produceOllamaReqBody(*history, modelName), &response)
		if err != nil {
			if errors.Is(err, ErrMalformedJSON) {
				correction, abort := handleMalformedBody(&badBodies, err)
				if abort != nil {
					return "", abort
				}
				turns = extendTurns(turns)
				log.Printf("ollama turn %d: malformed body (%d/%d), asking model to re-issue", turn, badBodies, maxBadBodies)
				*history = append(*history, &dataTypes.NativeTooltypeMessage{Role: "user", Content: correction})
				continue
			}
			return "", err
		}
		badBodies = 0
		sess.RecordPromptTokens(response.PromptEvalCount)

		reply := &response.Message

		if len(reply.ToolCalls) == 0 {
			if strings.TrimSpace(reply.Content) == "" {
				return "", ErrEmptyFinalAnswer
			}

			// Persist the final answer too.
			*history = append(*history, reply)
			return reply.Content, nil
		}

		// Append the tool request and all results as one block.
		results := make([]string, len(reply.ToolCalls))

		for i, toolCall := range reply.ToolCalls {
			results[i] = runTool(ctx, reader, toolCall.Function.Name, toolCall.Function.Arguments)
		}

		block := make([]*dataTypes.NativeTooltypeMessage, 0, 1+len(reply.ToolCalls))
		block = append(block, reply)

		for i, toolCall := range reply.ToolCalls {
			block = append(block, &dataTypes.NativeTooltypeMessage{
				Role:       "tool",
				ToolCallID: toolCall.ID,
				Content:    results[i],
			})
		}
		turns = extendTurns(turns)
		*history = append(*history, block...)
	}

	return "", ErrMaxInferenceCalls
}

func openAiToolLoop(ctx context.Context, reader *bufio.Reader, modelName string, sess *harnessSession.Session, chatURL string, apiKey string) (string, error) {
	history := sess.OpenAI()

	turns := baseTurns
	badBodies := 0
	for turn := 0; turn < turns; turn++ {
		var response dataTypes.OpenAIChatResponse
		err := postJSONAuthorized(ctx, chatURL, apiKey, produceOpenAIReqBody(*history, modelName), &response)
		if err != nil {
			if errors.Is(err, ErrMalformedJSON) {
				correction, abort := handleMalformedBody(&badBodies, err)
				if abort != nil {
					return "", abort
				}
				turns = extendTurns(turns)
				log.Printf("openai turn %d: malformed body (%d/%d), asking model to re-issue", turn, badBodies, maxBadBodies)
				*history = append(*history, &dataTypes.OpenAIChatMessage{Role: "user", Content: correction})
				continue
			}
			return "", err
		}
		badBodies = 0
		sess.RecordPromptTokens(response.Usage.PromptTokens)

		if len(response.Choices) == 0 {
			return "", ErrNoChoices
		}

		reply := &response.Choices[0].Message

		// vLLM sends tool-call arguments as a JSON string.
		arguments := make([]map[string]any, len(reply.ToolCalls))

		for i, toolCall := range reply.ToolCalls {
			arguments[i], err = decodeOpenAIArguments(toolCall.Function.Arguments)
			if err != nil {
				return "", fmt.Errorf("tool call %q: %w", toolCall.Function.Name, err)
			}
		}

		// A tool-calling turn carries empty content, so tool calls have to be
		// checked before treating empty content as an error.
		if len(reply.ToolCalls) == 0 {
			if strings.TrimSpace(reply.Content) == "" {
				return "", ErrEmptyFinalAnswer
			}

			// Persist the final answer too.
			*history = append(*history, reply)

			return reply.Content, nil
		}

		// Append the tool request and all results as one block.
		results := make([]string, len(reply.ToolCalls))

		for i, toolCall := range reply.ToolCalls {
			results[i] = runTool(ctx, reader, toolCall.Function.Name, arguments[i])
		}

		block := make([]*dataTypes.OpenAIChatMessage, 0, 1+len(reply.ToolCalls))
		block = append(block, reply)

		for i, toolCall := range reply.ToolCalls {
			block = append(block, &dataTypes.OpenAIChatMessage{
				Role:       "tool",
				ToolCallID: toolCall.ID,
				Content:    results[i],
			})
		}

		turns = extendTurns(turns)
		*history = append(*history, block...)
	}

	return "", ErrMaxInferenceCalls
}

// nativeTools is the single list of advertised tools.
func nativeTools() []dataTypes.NativeTypeTool {
	return []dataTypes.NativeTypeTool{dataTypes.WebSearch, dataTypes.ReadFile, dataTypes.Bash, dataTypes.EditFile, dataTypes.WriteFile}
}

func isNativeTool(name string) bool {
	for _, tool := range nativeTools() {
		if tool.Function.Name == name {
			return true
		}
	}

	return false
}

// runTool dispatches a tool call and returns failures as text for the model.
func runTool(ctx context.Context, reader *bufio.Reader, name string, arguments map[string]any) string {
	if !isNativeTool(name) {
		return fmt.Sprintf("unknown tool %q requested", name)
	}

	result, err := harnessDispatch.Dispatch(ctx, reader, &dataTypes.NativeLLMResponse{
		Type:      "tool_call",
		Tool:      name,
		Arguments: arguments,
	})
	if err != nil {
		return fmt.Sprintf("Tool execution failed: %v", err)
	}

	return result
}

// decodeVLLMArguments decodes vLLM's JSON-string tool arguments.
func decodeOpenAIArguments(raw string) (map[string]any, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}

	var arguments map[string]any

	if err := json.Unmarshal([]byte(raw), &arguments); err != nil {
		return nil, fmt.Errorf("decoding arguments %q: %w", raw, err)
	}

	return arguments, nil
}

func produceOllamaReqBody(messages []*dataTypes.NativeTooltypeMessage, modelName string) dataTypes.NativeToolChatRequest {
	return dataTypes.NativeToolChatRequest{
		Model:    modelName,
		Messages: messages,
		Stream:   false,
		Tools:    nativeTools(),
	}
}

func produceOpenAIReqBody(messages []*dataTypes.OpenAIChatMessage, modelName string) dataTypes.OpenAIChatRequest {
	return dataTypes.OpenAIChatRequest{
		Model:    modelName,
		Messages: messages,
		Stream:   false,
		Tools:    nativeTools(),

		// vLLM defaults tool_choice to "none", so send "auto" explicitly.
		ToolChoice: "auto",
	}
}

// postJSON posts payload as JSON and decodes the response into out.
func postJSON(ctx context.Context, providerURL string, payload any, out any) error {
	return postJSONAuthorized(ctx, providerURL, "", payload, out)
}

// postJSONAuthorized POSTs payload as JSON, adding bearer auth when apiKey is non-empty, and decodes a 200 response into out.
// A 200 body that can't be decoded into out matches ErrMalformedJSON.
func postJSONAuthorized(ctx context.Context, providerURL, apiKey string, payload, out any) error {
	reqBody, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshalling provider request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, providerURL, bytes.NewReader(reqBody))
	if err != nil {
		return fmt.Errorf("building provider request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
		req.Header.Set("X-Title", "optine")
	}

	resp, err := providerHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("calling provider: %w", err)
	}
	defer func() {
		// Drain a bounded amount so the connection can be reused.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxDrainBytes))
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return providerError(resp)
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading provider response: %w", err)
	}

	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("%w: %w", ErrMalformedJSON, err)
	}

	return nil
}

// providerError builds an error from a non-200 response.
func providerError(resp *http.Response) error {
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
	if err != nil {
		return fmt.Errorf("reading provider error response (HTTP %d): %w", resp.StatusCode, err)
	}

	detail := errorDetail(body)
	if detail == "" {
		detail = strings.TrimSpace(string(body))
	}

	if resp.StatusCode == http.StatusServiceUnavailable {
		return fmt.Errorf("Providers server is unavailable, status: %d", resp.StatusCode)
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		if detail == "" {
			return ErrRateLimited
		}
		return fmt.Errorf("%w: %s", ErrRateLimited, detail)
	}

	if detail == "" {
		return fmt.Errorf("provider returned HTTP %d", resp.StatusCode)
	}
	return fmt.Errorf("provider returned HTTP %d: %s", resp.StatusCode, detail)
}

// errorDetail extracts a human-readable message from an OpenRouter-style error
// envelope, or returns "" if the body isn't one.
func errorDetail(body []byte) string {
	var env dataTypes.OpenRouterErrorResponse
	if err := json.Unmarshal(body, &env); err != nil {
		return ""
	}
	if msg := strings.TrimSpace(env.Error.Message); msg != "" {
		return msg
	}
	return strings.TrimSpace(env.Error.Metadata.RemedyHint)
}
