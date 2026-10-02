package dataTypes

type NativeTypeToolCall struct {
	// Ollama sends an id here and ignores it on the way back in. It is kept
	// because OpenAI-compatible servers reject a "tool" reply that has no
	// id to match against.
	ID       string `json:"id,omitempty"`
	Function struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	} `json:"function"`
}

type NativeTooltypeMessage struct {
	Role      string               `json:"role"`
	Content   string               `json:"content"`
	ToolCalls []NativeTypeToolCall `json:"tool_calls,omitempty"`
	// Unused by Ollama, mandatory on OpenAI-compatible servers.
	ToolCallID string `json:"tool_call_id,omitempty"`
}

type NativeTypeTool struct {
	Type     string                 `json:"type"`
	Function NativeTypeToolFunction `json:"function"`
}

type NativeTypeToolFunction struct {
	Name        string               `json:"name"`
	Description string               `json:"description"`
	Parameters  NativeTypeParameters `json:"parameters"`
}

type NativeTypeProperties struct {
	Type        string `json:"type"`
	Description string `json:"description"`
}

type NativeTypeParameters struct {
	Type       string                          `json:"type"`
	Properties map[string]NativeTypeProperties `json:"properties"`
	Required   []string                        `json:"required"`
}

type NativeToolChatRequest struct {
	Model    string                   `json:"model"`
	Messages []*NativeTooltypeMessage `json:"messages"`
	Stream   bool                     `json:"stream"`
	Tools    []NativeTypeTool         `json:"tools,omitempty"`
	// No Format field here on purpose — do not combine Tools with
	// Format:"json", gpt-oss can drop content entirely if you do.
}

type NativeToolChatResponse struct {
	Message NativeTooltypeMessage `json:"message"`
}

// Everything below is the OpenAI-compatible shape that vLLM serves on
// /v1/chat/completions. It cannot reuse the types above because the response is
// wrapped in a "choices" array and tool call arguments are a JSON-encoded
// string rather than an object. Mixing the two shapes up fails to decode.

// VLLMTypeToolCall is the OpenAI-style counterpart of NativeTypeToolCall.
type VLLMTypeToolCall struct {
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Index    int    `json:"index,omitempty"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type VLLMTooltypeMessage struct {
	Role       string             `json:"role"`
	Content    string             `json:"content"`
	ToolCalls  []VLLMTypeToolCall `json:"tool_calls,omitempty"`
	ToolCallID string             `json:"tool_call_id,omitempty"`
}

type VLLMNChatRequest struct {
	Model    string                 `json:"model"`
	Messages []*VLLMTooltypeMessage `json:"messages"`
	Stream   bool                   `json:"stream"`
	Tools    []NativeTypeTool       `json:"tools,omitempty"`
	// vLLM defaults tool_choice to "none", which silently disables tool
	// calling, so this has to be sent as "auto" on every request.
	ToolChoice string `json:"tool_choice,omitempty"`
}

type VLLMNChatResponse struct {
	Choices []struct {
		Index        int                 `json:"index"`
		Message      VLLMTooltypeMessage `json:"message"`
		FinishReason string              `json:"finish_reason"`
	} `json:"choices"`
}
