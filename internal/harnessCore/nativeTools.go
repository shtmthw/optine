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
	ollamaChatURL     = "http://localhost:11434/api/chat"
	vLLMChatURL       = "http://localhost:8000/v1/chat/completions"
	openRouterChatURL = "https://openrouter.ai/api/v1/chat/completions"
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
		return openAiToolLoop(ctx, reader, modelName, sess, vLLMChatURL, "")

	case "Openrouter":
		key, err := harnessPermissions.GetAPIKey("openrouter")
		if err != nil {
			return "", err
		}
		return openAiToolLoop(ctx, reader, modelName, sess, openRouterChatURL, key)

	default:
		return "", fmt.Errorf("unsupported provider %q", provider)
	}
}

func ollamaToolLoop(ctx context.Context, reader *bufio.Reader, modelName string, sess *harnessSession.Session) (string, error) {
	history := &sess.Messages
	effort := wireEffort("Ollama", sess.EffortSupport, sess.Effort)
	turns := baseTurns
	badBodies := 0
	for turn := 0; turn < turns; turn++ {
		var response dataTypes.NativeToolChatResponse

		err := postJSON(ctx, ollamaChatURL, produceOllamaReqBody(*history, modelName, sess.SystemPrompt, effort), &response)
		if err != nil {
			if errors.Is(err, ErrMalformedJSON) {
				correction, abort := handleMalformedBody(&badBodies, err)
				if abort != nil {
					return "", abort
				}
				turns = extendTurns(turns)
				log.Printf("ollama turn %d: malformed body (%d/%d), asking model to re-issue", turn+1, badBodies, maxBadBodies)
				*history = append(*history, harnessSession.Message{Role: "user", Content: correction})
				continue
			}
			return "", err
		}
		badBodies = 0
		sess.RecordPromptTokens(response.PromptEvalCount)

		reply := messageFromOllama(response.Message)

		if len(reply.ToolCalls) == 0 {
			if strings.TrimSpace(reply.Content) == "" {
				return "", ErrEmptyFinalAnswer
			}

			// Persist the final answer too.
			*history = append(*history, reply)
			return reply.Content, nil
		}

		harnessSession.NotifyAssistant(reply.Content)

		// Append the tool request and all results as one block.
		results := make([]string, len(reply.ToolCalls))

		for i, toolCall := range reply.ToolCalls {
			results[i] = runTool(ctx, reader, toolCall.Name, toolCall.Arguments)
		}

		block := make([]harnessSession.Message, 0, 1+len(reply.ToolCalls))
		block = append(block, reply)

		for i, toolCall := range reply.ToolCalls {
			block = append(block, harnessSession.Message{
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

	history := &sess.Messages
	// vLLM and OpenRouter share this loop; the session says which one runs.
	effort := wireEffort(sess.Provider, sess.EffortSupport, sess.Effort)

	turns := baseTurns
	badBodies := 0
	for turn := 0; turn < turns; turn++ {
		var response dataTypes.OpenAIChatResponse
		err := postJSONAuthorized(ctx, chatURL, apiKey, produceOpenAIReqBody(*history, modelName, sess.SystemPrompt, effort), &response)
		if err != nil {
			if errors.Is(err, ErrMalformedJSON) {
				correction, abort := handleMalformedBody(&badBodies, err)
				if abort != nil {
					return "", abort
				}
				turns = extendTurns(turns)
				log.Printf("openai turn %d: malformed body (%d/%d), asking model to re-issue", turn, badBodies, maxBadBodies)
				*history = append(*history, harnessSession.Message{Role: "user", Content: correction})
				continue
			}
			return "", err
		}
		badBodies = 0
		sess.RecordPromptTokens(response.Usage.PromptTokens)

		if len(response.Choices) == 0 {
			return "", ErrNoChoices
		}

		reply, arguments, err := messageFromOpenAI(response.Choices[0].Message)
		if err != nil {
			return "", err
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

		harnessSession.NotifyAssistant(reply.Content)

		// Append the tool request and all results as one block.
		results := make([]string, len(reply.ToolCalls))

		for i, toolCall := range reply.ToolCalls {
			results[i] = runTool(ctx, reader, toolCall.Name, arguments[i])
		}

		block := make([]harnessSession.Message, 0, 1+len(reply.ToolCalls))
		block = append(block, reply)

		for i, toolCall := range reply.ToolCalls {
			block = append(block, harnessSession.Message{
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

// interruptedToolResult is what the model sees for a call the user's
// interrupt stopped before it ran.
const interruptedToolResult = "Tool not run: the user interrupted the request."

// runTool dispatches a tool call and returns failures as text for the model.
func runTool(ctx context.Context, reader *bufio.Reader, name string, arguments map[string]any) string {
	harnessSession.NotifyToolCall(name, arguments)

	// After an interrupt the remaining calls of the turn are not run, but each
	// still gets a result so the saved history keeps every call paired.
	if ctx.Err() != nil {
		harnessSession.NotifyToolResult(name, interruptedToolResult)
		return interruptedToolResult
	}

	if !isNativeTool(name) {
		result := fmt.Sprintf("unknown tool %q requested", name)
		harnessSession.NotifyToolResult(name, result)
		return result
	}

	result, err := harnessDispatch.Dispatch(ctx, reader, &dataTypes.NativeLLMResponse{
		Type:      "tool_call",
		Tool:      name,
		Arguments: arguments,
	})
	if err != nil {
		result = fmt.Sprintf("Tool execution failed: %v", err)
	}

	harnessSession.NotifyToolResult(name, result)

	return result
}

// decodeOpenAIArguments decodes openAI based JSON-string tool arguments.
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

func produceOllamaReqBody(messages []harnessSession.Message, modelName string, systemPrompt string, effort effortWire) dataTypes.NativeToolChatRequest {
	allMessages := make([]*dataTypes.NativeTooltypeMessage, 0, len(messages)+1)
	if systemPrompt != "" {
		allMessages = append(allMessages, &dataTypes.NativeTooltypeMessage{Role: "system", Content: systemPrompt})
	}

	for _, msg := range messages {
		allMessages = append(allMessages, ollamaWireMessage(msg))
	}

	return dataTypes.NativeToolChatRequest{
		Model:    modelName,
		Messages: allMessages,
		Stream:   false,
		Tools:    nativeTools(),
		Think:    effort.Think,
	}
}

func ollamaWireMessage(msg harnessSession.Message) *dataTypes.NativeTooltypeMessage {
	calls := make([]dataTypes.NativeTypeToolCall, len(msg.ToolCalls))
	for i, call := range msg.ToolCalls {
		wire := dataTypes.NativeTypeToolCall{ID: call.ID}
		wire.Function.Name = call.Name
		wire.Function.Arguments = call.Arguments
		calls[i] = wire
	}

	return &dataTypes.NativeTooltypeMessage{
		Role:       msg.Role,
		Content:    msg.Content,
		ToolCalls:  calls,
		ToolCallID: msg.ToolCallID,
	}
}

func produceOpenAIReqBody(messages []harnessSession.Message, modelName string, systemPrompt string, effort effortWire) dataTypes.OpenAIChatRequest {
	allMessages := make([]*dataTypes.OpenAIChatMessage, 0, len(messages)+1)
	if systemPrompt != "" {
		allMessages = append(allMessages, &dataTypes.OpenAIChatMessage{Role: "system", Content: systemPrompt})
	}

	// Ollama call ids are optional; OpenAI requires one per call and a matching
	// tool_call_id on every tool reply. Synthesize them at the wire boundary so
	// a session resumed on a different provider still produces a valid request.
	var pending []string
	for _, msg := range messages {
		wire := &dataTypes.OpenAIChatMessage{
			Role:       msg.Role,
			Content:    msg.Content,
			ToolCallID: msg.ToolCallID,
		}

		if msg.Role != "tool" {
			pending = nil
		}

		for i, call := range msg.ToolCalls {
			id := call.ID
			if id == "" {
				id = fmt.Sprintf("call_%d_%d", len(allMessages), i)
			}
			pending = append(pending, id)

			wireCall := dataTypes.OpenAITypeToolCall{ID: id, Type: "function"}
			wireCall.Function.Name = call.Name
			wireCall.Function.Arguments = encodeOpenAIArguments(call.Arguments)
			wire.ToolCalls = append(wire.ToolCalls, wireCall)
		}

		if msg.Role == "tool" && len(pending) > 0 {
			if wire.ToolCallID == "" {
				wire.ToolCallID = pending[0]
			}
			pending = pending[1:]
		}

		allMessages = append(allMessages, wire)
	}

	return dataTypes.OpenAIChatRequest{
		Model:      modelName,
		Messages:   allMessages,
		Stream:     false,
		Tools:      nativeTools(),
		ToolChoice: "auto",

		Reasoning:       effort.Reasoning,
		ReasoningEffort: effort.ReasoningEffort,
	}
}

func encodeOpenAIArguments(arguments map[string]any) string {
	if arguments == nil {
		return "{}"
	}

	encoded, err := json.Marshal(arguments)
	if err != nil {
		return "{}"
	}

	return string(encoded)
}

func messageFromOllama(msg dataTypes.NativeTooltypeMessage) harnessSession.Message {
	calls := make([]harnessSession.ToolCall, len(msg.ToolCalls))
	for i, call := range msg.ToolCalls {
		calls[i] = harnessSession.ToolCall{
			ID:        call.ID,
			Name:      call.Function.Name,
			Arguments: call.Function.Arguments,
		}
	}

	return harnessSession.Message{
		Role:       msg.Role,
		Content:    msg.Content,
		ToolCalls:  calls,
		ToolCallID: msg.ToolCallID,
	}
}

func messageFromOpenAI(msg dataTypes.OpenAIChatMessage) (harnessSession.Message, []map[string]any, error) {
	calls := make([]harnessSession.ToolCall, len(msg.ToolCalls))
	arguments := make([]map[string]any, len(msg.ToolCalls))

	for i, call := range msg.ToolCalls {
		decoded, err := decodeOpenAIArguments(call.Function.Arguments)
		if err != nil {
			return harnessSession.Message{}, nil, fmt.Errorf("tool call %q: %w", call.Function.Name, err)
		}

		arguments[i] = decoded
		calls[i] = harnessSession.ToolCall{
			ID:        call.ID,
			Name:      call.Function.Name,
			Arguments: decoded,
		}
	}

	return harnessSession.Message{
		Role:       msg.Role,
		Content:    msg.Content,
		ToolCalls:  calls,
		ToolCallID: msg.ToolCallID,
	}, arguments, nil
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
