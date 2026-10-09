package harnessCore

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/mattthew/optine/internals/dataTypes"
	"github.com/mattthew/optine/internals/systemPrompts"
)

func produceNonNativeReqBody(messages []*dataTypes.NonNativeMessage, modelName string) dataTypes.NonNativeChatRequest {

	var requestBody = dataTypes.NonNativeChatRequest{
		Model:    modelName,
		Messages: messages,
		Stream:   false,

		// This asks Ollama to keep the output valid JSON.
		// It is NOT native tool calling.
		Format: "json",
	}

	return requestBody
}

func produceVLLMNonNativeReqBody(messages []*dataTypes.NonNativeMessage, modelName string) dataTypes.VLLMNonNativeChatRequest {

	var responseFormat = dataTypes.NonNativeResponseFormat{
		Type: "json_object",
	}

	var requestBody = dataTypes.VLLMNonNativeChatRequest{
		Model:    modelName,
		Messages: messages,
		Stream:   false,

		// OpenAI-compatible servers ask for the same thing as
		// Format: "json", just spelled differently.
		ResponseFormat: &responseFormat,
	}

	return requestBody
}

func ollamaNonNativeChat(ctx context.Context, messages []*dataTypes.NonNativeMessage, modelName string) (*dataTypes.NonNativeMessage, error) {

	var result dataTypes.NonNativeChatResponse

	if err := postJSON(ctx, ollamaChatURL, produceNonNativeReqBody(messages, modelName), &result); err != nil {
		return nil, err
	}

	return &result.Message, nil
}

func vllmNonNativeChat(ctx context.Context, messages []*dataTypes.NonNativeMessage, modelName string) (*dataTypes.NonNativeMessage, error) {

	var result dataTypes.VLLMNonNativeChatResponse

	if err := postJSON(ctx, vLLMChatURL, produceVLLMNonNativeReqBody(messages, modelName), &result); err != nil {
		return nil, err
	}

	if len(result.Choices) == 0 {
		return nil, ErrNoChoices
	}

	return &result.Choices[0].Message, nil
}

// parseNonNativeResponse pulls the JSON envelope out of a reply. The fence
// stripping is defensive: format:"json" should prevent it, but models still
// wrap JSON in markdown from time to time.
func parseNonNativeResponse(content string) (*dataTypes.NonNativeLLMResponse, error) {
	content = strings.TrimSpace(content)

	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	var response dataTypes.NonNativeLLMResponse

	decoder := json.NewDecoder(strings.NewReader(content))

	if err := decoder.Decode(&response); err != nil {
		return nil, err
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

// maxEnvelopeExcerpt caps the raw reply quoted back in a correction so one
// huge garbage dump does not flood the context it is trying to rescue.
const maxEnvelopeExcerpt = 500

// correctionForBadEnvelope builds the retry instruction for a malformed
// envelope reply. Unlike the native path the offending text is held here, so
// it is quoted back (truncated) for the model to debug. Pure for testability.
func correctionForBadEnvelope(raw string) string {
	if runes := []rune(raw); len(runes) > maxEnvelopeExcerpt {
		raw = string(runes[:maxEnvelopeExcerpt])
	}
	return fmt.Sprintf(
		"Your last reply was not a valid tool-call envelope. Reply with exactly one valid JSON object and nothing else, no markdown fences. What you sent was:\n%s",
		strings.TrimSpace(raw),
	)
}
func nonNativeAgentCall(ctx context.Context, reader *bufio.Reader, provider string, modelName string, userMessage string) (string, error) {

	var chat func(ctx context.Context, messages []*dataTypes.NonNativeMessage, modelName string) (*dataTypes.NonNativeMessage, error)

	switch provider {

	case "Ollama":
		chat = ollamaNonNativeChat

	case "vLLM":
		chat = vllmNonNativeChat

	default:
		return "", fmt.Errorf("unsupported provider %q", provider)
	}

	var messages = []*dataTypes.NonNativeMessage{
		{
			Role:    "system",
			Content: withCasualMemory(systemPrompts.NonNativeToolSystemPrompt(time.Now(), maxTurns)),
		},
		{
			Role:    "user",
			Content: userMessage,
		},
	}

	badBodies := 0
	for turn := range maxTurns {
		reply, err := chat(ctx, messages, modelName)
		if err != nil {
			return "", err
		}

		aiResponse, err := parseNonNativeResponse(reply.Content)
		if err != nil {
			_, abort := handleMalformedBody(&badBodies, fmt.Errorf("invalid JSON response from %s: %w", provider, err))
			if abort != nil {
				return "", abort
			}
			log.Printf("non-native turn %d: malformed envelope (%d/%d), asking model to re-issue", turn, badBodies, maxBadBodies)
			messages = append(messages, &dataTypes.NonNativeMessage{
				Role:    "user",
				Content: correctionForBadEnvelope(reply.Content),
			})
			continue
		}
		badBodies = 0

		switch aiResponse.Type {
		case "final_answer":
			return aiResponse.Content, nil

		case "tool_call":
			result := runTool(ctx, reader, aiResponse.Tool, aiResponse.Arguments)

			// Keep the assistant's JSON tool request in the conversation.
			messages = append(messages, reply)

			// Feed the actual tool result back to the model.
			messages = append(messages, &dataTypes.NonNativeMessage{
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
			})

		default:
			// parseNonNativeResponse already rejects unknown types.
			return "", fmt.Errorf(
				"unknown response type %q",
				aiResponse.Type,
			)
		}
	}

	return "", ErrMaxToolCalls
}
