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
	// PromptEvalCount is Ollama's prompt token count for this request.
	PromptEvalCount int `json:"prompt_eval_count,omitempty"`
}

// OpenAI-compatible non-native shapes served by vLLM and OpenRouter, where
// "format":"json" is spelled "response_format".

// NonNativeResponseFormat is the OpenAI spelling of Ollama's Format: "json".
type NonNativeResponseFormat struct {
	Type string `json:"type"`
}

type OpenAINonNativeChatRequest struct {
	Model          string                   `json:"model"`
	Messages       []*NonNativeMessage      `json:"messages"`
	Stream         bool                     `json:"stream"`
	ResponseFormat *NonNativeResponseFormat `json:"response_format,omitempty"`
}

type OpenAINonNativeChatResponse struct {
	Choices []struct {
		Index   int              `json:"index"`
		Message NonNativeMessage `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens int `json:"prompt_tokens"`
	} `json:"usage"`
}
