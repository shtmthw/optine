package dataTypes

type NonNativeMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type NonNativeChatRequest struct {
	Model    string              `json:"model"`
	Messages []*NonNativeMessage `json:"messages"`
	Stream   bool                `json:"stream"`
	Format   string              `json:"format,omitempty"`
}

type NonNativeChatResponse struct {
	Message NonNativeMessage `json:"message"`
}

// The OpenAI-compatible shape vLLM serves on /v1/chat/completions. The response
// is wrapped in a "choices" array, and "format":"json" is spelled
// "response_format" instead.

// NonNativeResponseFormat is vLLM's equivalent of Ollama's Format: "json".
type NonNativeResponseFormat struct {
	Type string `json:"type"`
}

type VLLMNonNativeChatRequest struct {
	Model          string                   `json:"model"`
	Messages       []*NonNativeMessage      `json:"messages"`
	Stream         bool                     `json:"stream"`
	ResponseFormat *NonNativeResponseFormat `json:"response_format,omitempty"`
}

type VLLMNonNativeChatResponse struct {
	Choices []struct {
		Index   int              `json:"index"`
		Message NonNativeMessage `json:"message"`
	} `json:"choices"`
}
