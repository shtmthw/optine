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
	"github.com/mattthew/optine/internals/harnessMemory"
	"github.com/mattthew/optine/internals/systemPrompts"
)

var ErrMalformedJSON = fmt.Errorf("The model sent a malformed JSON, skip tool calls and tell the model to retry its previous task.")

var (
	ollamaChatURL = "http://localhost:11434/api/chat"
	vLLMChatURL   = "http://localhost:8000/v1/chat/completions"
)

// A model that keeps asking for tools instead of answering gets cut off here
// rather than looping forever.
const maxTurns = 55

// maxBadBodies bounds same-history retries when a provider answers with a
// body that is not usable JSON. One bad body is usually a transient truncated
// stream, so a couple of retries are worth it; past the budget the run aborts
// instead of burning turns on confusion.
const maxBadBodies = 5

// malformedRetryNotice is the whole correction: short on purpose, so the
// garbage body never enters history and costs zero future context.
const malformedRetryNotice = "Your last reply wasn't usable JSON — re-issue your previous turn, exactly as before."

// handleMalformedBody records one malformed provider body against budget.
// A non-empty correction means the loop should append it as a user message
// and continue; a non-nil abort means the budget is spent and the loop
// should return it.
func handleMalformedBody(badBodies *int, err error) (correction string, abort error) {
	*badBodies++
	if *badBodies > maxBadBodies {
		return "", fmt.Errorf("provider sent %d malformed bodies in a row, aborting: %w", *badBodies, err)
	}
	return malformedRetryNotice, nil
}

var (
	ErrMaxToolCalls     = errors.New("maximum tool calls exceeded without an answer")
	ErrEmptyFinalAnswer = errors.New("provider returned an empty final answer")
	ErrNoChoices        = errors.New("vLLM returned no choices")
)

var providerHTTPClient = &http.Client{
	Timeout: 320 * time.Second,
}

// ---------------------------------------------------------------------------
// Entry point
// ---------------------------------------------------------------------------

// nativeToolAgentCall runs the agent loop on the chosen provider. Picking a
// provider is also picking a model and an inference server, so a whole run
// stays on one of the two loops below.
func nativeToolAgentCall(ctx context.Context, reader *bufio.Reader, provider string, modelName string, userMessage string) (string, error) {
	switch provider {
	case "Ollama":
		return ollamaToolLoop(ctx, reader, modelName, userMessage)

	case "vLLM":
		return vllmToolLoop(ctx, reader, modelName, userMessage)

	default:
		return "", fmt.Errorf("unsupported provider %q", provider)
	}
}

// withCasualMemory appends the user's long-term memory to base, once per
// agent-loop trigger at history construction. Best-effort: any failure logs
// and returns base unchanged so a memory hiccup never aborts the run.
func withCasualMemory(base string) string {
	memoryPath, err := harnessMemory.CasualMemoryPath()
	if err != nil {
		log.Printf("casual memory path unavailable: %v", err)
		return base
	}
	memoryData, err := harnessMemory.ReadCasualMemoryFile()
	if err != nil {
		log.Printf("casual memory unavailable: %v", err)
		return base
	}
	return systemPrompts.WithCasualMemory(base, memoryPath, memoryData)
}

// ---------------------------------------------------------------------------
// The two agent loops
//
// Each keeps its history in its own server's message type, so history is
// replayed exactly as the server sent it and never translated between formats.
// ---------------------------------------------------------------------------

func ollamaToolLoop(ctx context.Context, reader *bufio.Reader, modelName string, userMessage string) (string, error) {
	history := []*dataTypes.NativeTooltypeMessage{
		{Role: "system", Content: withCasualMemory(systemPrompts.NativeToolSystemPrompt(time.Now()))},
		{Role: "user", Content: userMessage},
	}

	badBodies := 0
	for turn := range maxTurns {
		var response dataTypes.NativeToolChatResponse

		//the inference call
		err := postJSON(ctx, ollamaChatURL, produceOllamaReqBody(history, modelName), &response)
		if err != nil {
			if errors.Is(err, ErrMalformedJSON) {
				correction, abort := handleMalformedBody(&badBodies, err)
				if abort != nil {
					return "", abort
				}
				log.Printf("ollama turn %d: malformed body (%d/%d), asking model to re-issue", turn, badBodies, maxBadBodies)
				history = append(history, &dataTypes.NativeTooltypeMessage{Role: "user", Content: correction})
				continue
			}
			return "", err
		}
		badBodies = 0

		reply := &response.Message

		log.Println("inference reply on turn", turn, ":", reply)

		// A tool-calling turn carries empty content, so tool calls have to be
		// checked before treating empty content as an error.
		if len(reply.ToolCalls) == 0 {
			if strings.TrimSpace(reply.Content) == "" {
				return "", ErrEmptyFinalAnswer
			}

			return reply.Content, nil
		}

		// Keep the assistant's tool-call message in history, as sent.
		history = append(history, reply)

		for _, toolCall := range reply.ToolCalls {
			result := runTool(ctx, reader, toolCall.Function.Name, toolCall.Function.Arguments)

			history = append(history, &dataTypes.NativeTooltypeMessage{
				Role:       "tool",
				ToolCallID: toolCall.ID,
				Content:    result,
			})
		}
	}

	return "", ErrMaxToolCalls
}

func vllmToolLoop(ctx context.Context, reader *bufio.Reader, modelName string, userMessage string) (string, error) {
	history := []*dataTypes.VLLMTooltypeMessage{
		{Role: "system", Content: withCasualMemory(systemPrompts.NativeToolSystemPrompt(time.Now()))},
		{Role: "user", Content: userMessage},
	}

	badBodies := 0
	for turn := range maxTurns {
		var response dataTypes.VLLMNChatResponse
		err := postJSON(ctx, vLLMChatURL, produceVLLMReqBody(history, modelName), &response)
		if err != nil {
			if errors.Is(err, ErrMalformedJSON) {
				correction, abort := handleMalformedBody(&badBodies, err)
				if abort != nil {
					return "", abort
				}
				log.Printf("vllm turn %d: malformed body (%d/%d), asking model to re-issue", turn, badBodies, maxBadBodies)
				history = append(history, &dataTypes.VLLMTooltypeMessage{Role: "user", Content: correction})
				continue
			}
			return "", err
		}
		badBodies = 0

		if len(response.Choices) == 0 {
			return "", ErrNoChoices
		}

		reply := &response.Choices[0].Message

		// vLLM sends tool-call arguments as a JSON string. Decode them all
		// before running any tool, so one malformed call stops the run before
		// anything executes. History keeps the message as sent.
		arguments := make([]map[string]any, len(reply.ToolCalls))

		for i, toolCall := range reply.ToolCalls {
			arguments[i], err = decodeVLLMArguments(toolCall.Function.Arguments)
			if err != nil {
				return "", fmt.Errorf("tool call %q: %w", toolCall.Function.Name, err)
			}
		}

		log.Println("inference reply on turn", turn, ":", reply)

		// A tool-calling turn carries empty content, so tool calls have to be
		// checked before treating empty content as an error.
		if len(reply.ToolCalls) == 0 {
			if strings.TrimSpace(reply.Content) == "" {
				return "", ErrEmptyFinalAnswer
			}

			return reply.Content, nil
		}

		// Keep the assistant's tool-call message in history, as sent.
		history = append(history, reply)

		for i, toolCall := range reply.ToolCalls {
			result := runTool(ctx, reader, toolCall.Function.Name, arguments[i])

			history = append(history, &dataTypes.VLLMTooltypeMessage{
				Role:       "tool",
				ToolCallID: toolCall.ID,
				Content:    result,
			})
		}
	}

	return "", ErrMaxToolCalls
}

// nativeTools is the one list of tools the agent advertises, so the request
// body and the unknown-tool guard can never drift apart.
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

// runTool dispatches one tool call and turns any failure into text the model can
// read, so a single bad call does not end the turn.
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

// decodeVLLMArguments unwraps the JSON string vLLM sends for tool call
// arguments back into the map the dispatcher expects.
func decodeVLLMArguments(raw string) (map[string]any, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}

	var arguments map[string]any

	if err := json.Unmarshal([]byte(raw), &arguments); err != nil {
		return nil, fmt.Errorf("decoding arguments %q: %w", raw, err)
	}

	return arguments, nil
}

// ---------------------------------------------------------------------------
// Requests
// ---------------------------------------------------------------------------

func produceOllamaReqBody(messages []*dataTypes.NativeTooltypeMessage, modelName string) dataTypes.NativeToolChatRequest {
	return dataTypes.NativeToolChatRequest{
		Model:    modelName,
		Messages: messages,
		Stream:   false,
		Tools:    nativeTools(),
	}
}

func produceVLLMReqBody(messages []*dataTypes.VLLMTooltypeMessage, modelName string) dataTypes.VLLMNChatRequest {
	return dataTypes.VLLMNChatRequest{
		Model:    modelName,
		Messages: messages,
		Stream:   false,
		Tools:    nativeTools(),

		// vLLM defaults tool_choice to "none", which silently disables tool
		// calling, so this has to be sent as "auto" on every request.
		ToolChoice: "auto",
	}
}

// postJSON is the HTTP plumbing shared by every provider call in this package:
// send the payload and decode the body into out.
func postJSON(ctx context.Context, providerURL string, payload any, out any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshalling provider request: %w", err)
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		providerURL,
		bytes.NewReader(data),
	)
	if err != nil {
		return fmt.Errorf("building provider request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := providerHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("calling provider: %w", err)
	}

	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf(
			"provider returned HTTP %d: %s",
			resp.StatusCode,
			strings.TrimSpace(string(errBody)),
		)
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return ErrMalformedJSON

	}

	return nil
}
