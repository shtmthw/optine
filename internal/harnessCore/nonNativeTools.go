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
	"github.com/mattthew/optine/internal/harnessSession"
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

func ollamaNonNativeChat(ctx context.Context, messages []*dataTypes.NonNativeMessage, modelName string) (*dataTypes.NonNativeMessage, int, error) {

	var result dataTypes.NonNativeChatResponse

	if err := postJSON(ctx, ollamaChatURL, produceNonNativeReqBody(messages, modelName), &result); err != nil {
		return nil, 0, err
	}

	return &result.Message, result.PromptEvalCount, nil
}

func vllmNonNativeChat(ctx context.Context, messages []*dataTypes.NonNativeMessage, modelName string) (*dataTypes.NonNativeMessage, int, error) {

	var result dataTypes.VLLMNonNativeChatResponse

	if err := postJSON(ctx, vLLMChatURL, produceVLLMNonNativeReqBody(messages, modelName), &result); err != nil {
		return nil, 0, err
	}

	if len(result.Choices) == 0 {
		return nil, 0, ErrNoChoices
	}

	return &result.Choices[0].Message, result.Usage.PromptTokens, nil
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
func nonNativeAgentCall(ctx context.Context, sess *harnessSession.Session, reader *bufio.Reader, provider string, modelName string) (string, error) {

	var chat func(ctx context.Context, messages []*dataTypes.NonNativeMessage, modelName string) (*dataTypes.NonNativeMessage, int, error)

	switch provider {

	case "Ollama":
		chat = ollamaNonNativeChat

	case "vLLM":
		chat = vllmNonNativeChat

	default:
		return "", fmt.Errorf("unsupported provider %q", provider)
	}

	history := sess.Envelope()

	turns := baseTurns
	badBodies := 0
	for turn := 0; turn < turns; turn++ {
		reply, promptTokens, err := chat(ctx, *history, modelName)
		if err != nil {
			return "", err
		}
		sess.RecordPromptTokens(promptTokens)

		aiResponse, err := parseNonNativeResponse(reply.Content)
		if err != nil {
			_, abort := handleMalformedBody(&badBodies, fmt.Errorf("invalid JSON response from %s: %w", provider, err))
			if abort != nil {
				return "", abort
			}
			turns++
			log.Printf("non-native turn %d: malformed envelope (%d/%d), asking model to re-issue", turn, badBodies, maxBadBodies)
			*history = append(*history, &dataTypes.NonNativeMessage{
				Role:    "user",
				Content: correctionForBadEnvelope(reply.Content),
			})
			continue
		}
		badBodies = 0

		switch aiResponse.Type {
		case "final_answer":
			// Persist the assistant's final envelope as sent; a session that
			// only stores user turns is not a transcript.
			*history = append(*history, reply)

			return aiResponse.Content, nil

		case "tool_call":
			result := runTool(ctx, reader, aiResponse.Tool, aiResponse.Arguments)

			// Keep the assistant's JSON tool request and the actual tool
			// result together: one append, so no exit path can separate them.
			*history = append(*history,
				reply,
				&dataTypes.NonNativeMessage{
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
			// parseNonNativeResponse already rejects unknown types.
			return "", fmt.Errorf(
				"unknown response type %q",
				aiResponse.Type,
			)
		}
	}

	return "", ErrMaxInferenceCalls
}
