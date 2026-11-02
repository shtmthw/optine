package harnessCore

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/mattthew/optine/internal/dataTypes"
	"github.com/mattthew/optine/internal/harnessPermissions"
	"github.com/mattthew/optine/internal/harnessSession"
)

func nonNativeWireMessage(msg harnessSession.Message) *dataTypes.NonNativeMessage {
	role := msg.Role
	if role == "tool" {
		role = "user"
	}

	return &dataTypes.NonNativeMessage{Role: role, Content: msg.Content}
}

func produceNonNativeReqBody(messages []harnessSession.Message, modelName string, systemPrompt string) dataTypes.NonNativeChatRequest {

	allMessages := make([]*dataTypes.NonNativeMessage, 0, len(messages)+1)
	if systemPrompt != "" {
		allMessages = append(allMessages, &dataTypes.NonNativeMessage{Role: "system", Content: systemPrompt})
	}
	for _, msg := range messages {
		allMessages = append(allMessages, nonNativeWireMessage(msg))
	}

	var requestBody = dataTypes.NonNativeChatRequest{
		Model:    modelName,
		Messages: allMessages,
		Stream:   false,

		// Valid JSON output, not native tool calling.
		Format: "json",
	}

	return requestBody
}

func produceOpenAINonNativeReqBody(messages []harnessSession.Message, modelName string, systemPrompt string) dataTypes.OpenAINonNativeChatRequest {

	var responseFormat = dataTypes.NonNativeResponseFormat{
		Type: "json_object",
	}

	allMessages := make([]*dataTypes.NonNativeMessage, 0, len(messages)+1)
	if systemPrompt != "" {
		allMessages = append(allMessages, &dataTypes.NonNativeMessage{Role: "system", Content: systemPrompt})
	}
	for _, msg := range messages {
		allMessages = append(allMessages, nonNativeWireMessage(msg))
	}

	var requestBody = dataTypes.OpenAINonNativeChatRequest{
		Model:    modelName,
		Messages: allMessages,
		Stream:   false,

		// vLLM and OpenRouter spell Ollama's Format:"json" as response_format.
		ResponseFormat: &responseFormat,
	}

	return requestBody
}

func ollamaNonNativeChat(ctx context.Context, messages []harnessSession.Message, modelName string, systemPrompt string, _, _ string) (*dataTypes.NonNativeMessage, int, error) {

	var result dataTypes.NonNativeChatResponse

	if err := postJSON(ctx, ollamaChatURL, produceNonNativeReqBody(messages, modelName, systemPrompt), &result); err != nil {
		return nil, 0, err
	}

	return &result.Message, result.PromptEvalCount, nil
}

func openAiNonNativeChat(ctx context.Context, messages []harnessSession.Message, modelName string, systemPrompt string, chatURL string, apiKey string) (*dataTypes.NonNativeMessage, int, error) {

	var result dataTypes.OpenAINonNativeChatResponse

	if err := postJSONAuthorized(ctx, chatURL, apiKey, produceOpenAINonNativeReqBody(messages, modelName, systemPrompt), &result); err != nil {
		return nil, 0, err
	}

	if len(result.Choices) == 0 {
		return nil, 0, ErrNoChoices
	}

	return &result.Choices[0].Message, result.Usage.PromptTokens, nil
}

// parseNonNativeResponse pulls the JSON envelope out of a reply.
func parseNonNativeResponse(content string) (*dataTypes.NonNativeLLMResponse, error) {
	content = strings.TrimSpace(content)

	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	var raw struct {
		Type      string          `json:"type"`
		Tool      string          `json:"tool"`
		Arguments json.RawMessage `json:"arguments"`
		Content   string          `json:"content"`
	}

	if err := json.Unmarshal([]byte(content), &raw); err != nil {
		return nil, err
	}

	arguments, err := decodeNonNativeArguments(raw.Arguments)
	if err != nil {
		return nil, err
	}

	response := dataTypes.NonNativeLLMResponse{
		Type:      raw.Type,
		Tool:      raw.Tool,
		Arguments: arguments,
		Content:   raw.Content,
	}

	switch response.Type {
	case "final_answer":
		if strings.TrimSpace(response.Content) == "" {
			return nil, errors.New("final_answer is missing content")
		}

	case "tool_call":
		if strings.TrimSpace(response.Tool) == "" {
			return nil, errors.New("tool_call is missing tool")
		}

	default:
		return nil, fmt.Errorf(
			"unsupported response type %q",
			response.Type,
		)
	}

	return &response, nil
}

// decodeNonNativeArguments accepts an object or an OpenAI-style JSON string.
func decodeNonNativeArguments(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 {
		return nil, nil
	}

	var arguments map[string]any
	if err := json.Unmarshal(raw, &arguments); err == nil {
		return arguments, nil
	}

	var encoded string
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return nil, fmt.Errorf("decoding arguments %s: %w", raw, err)
	}

	return decodeOpenAIArguments(encoded)
}

// maxEnvelopeExcerpt caps the raw reply quoted in a correction.
const maxEnvelopeExcerpt = 500

// correctionForBadEnvelope builds the retry instruction for a malformed reply.
func correctionForBadEnvelope(raw string) string {
	if runes := []rune(raw); len(runes) > maxEnvelopeExcerpt {
		raw = string(runes[:maxEnvelopeExcerpt])
	}
	return fmt.Sprintf(
		"Your last reply was not a valid tool-call envelope. Reply with exactly one valid JSON object and nothing else, no markdown fences. What you sent was:\n%s",
		strings.TrimSpace(raw),
	)
}

type nonNativeChat func(ctx context.Context, messages []harnessSession.Message, modelName, systemPrompt, chatURL, apiKey string) (*dataTypes.NonNativeMessage, int, error)

func nonNativeAgentCall(ctx context.Context, sess *harnessSession.Session, reader *bufio.Reader, provider string, modelName string) (string, error) {

	var chat nonNativeChat
	var chatURL, apiKey string

	switch provider {

	case "Ollama":
		chat = ollamaNonNativeChat

	case "vLLM":
		chat, chatURL = openAiNonNativeChat, vLLMChatURL

	case "Openrouter":
		key, err := harnessPermissions.GetAPIKey("openrouter")
		if err != nil {
			return "", err
		}
		chat, chatURL, apiKey = openAiNonNativeChat, openRouterChatURL, key

	default:
		return "", fmt.Errorf("unsupported provider %q", provider)
	}

	history := &sess.Messages

	turns := baseTurns
	badBodies := 0
	for turn := 0; turn < turns; turn++ {
		reply, promptTokens, err := chat(ctx, *history, modelName, sess.SystemPrompt, chatURL, apiKey)
		if err != nil {
			if errors.Is(err, ErrMalformedJSON) {
				correction, abort := handleMalformedBody(&badBodies, fmt.Errorf("malformed HTTP body from %s: %w", provider, err))
				if abort != nil {
					return "", abort
				}
				turns = extendTurns(turns)
				log.Printf("non-native turn %d: malformed body (%d/%d), asking model to re-issue", turn+1, badBodies, maxBadBodies)
				*history = append(*history, harnessSession.Message{
					Role:    "user",
					Content: correction,
				})
				continue
			}
			return "", err
		}
		sess.RecordPromptTokens(promptTokens)

		aiResponse, err := parseNonNativeResponse(reply.Content)
		if err != nil {
			_, abort := handleMalformedBody(&badBodies, fmt.Errorf("invalid JSON response from %s: %w", provider, err))
			if abort != nil {
				return "", abort
			}
			turns = extendTurns(turns)
			log.Printf("non-native turn %d: malformed envelope (%d/%d), asking model to re-issue", turn, badBodies, maxBadBodies)
			*history = append(*history, harnessSession.Message{
				Role:    "user",
				Content: correctionForBadEnvelope(reply.Content),
			})
			continue
		}
		badBodies = 0

		switch aiResponse.Type {
		case "final_answer":
			// Keep the assistant envelope in the transcript.
			*history = append(*history, harnessSession.Message{Role: reply.Role, Content: reply.Content})

			return aiResponse.Content, nil

		case "tool_call":
			result := runTool(ctx, reader, aiResponse.Tool, aiResponse.Arguments)

			// Keep the request and result together. The envelope protocol has
			// no tool role, so the result stays a user turn.
			*history = append(*history,
				harnessSession.Message{Role: reply.Role, Content: reply.Content},
				harnessSession.Message{
					Role: "user",
					Content: fmt.Sprintf(
						"TOOL_RESULT\n"+
							"tool: %s\n\n"+
							"result:\n%s\n\n"+
							"Use this result to continue solving the user's request. "+
							"If you have enough information, return a final_answer JSON object. "+
							"If you need to run a tool, return another tool_call JSON object.",
						aiResponse.Tool,
						result,
					),
				},
			)

			turns = extendTurns(turns)

		default:
			return "", fmt.Errorf(
				"unknown response type %q",
				aiResponse.Type,
			)
		}
	}

	return "", ErrMaxInferenceCalls
}
