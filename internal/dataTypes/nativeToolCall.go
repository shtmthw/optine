package dataTypes

type NativeTypeToolCall struct {
	// Kept because OpenAI-compatible servers require an id on tool replies.
	ID       string `json:"id,omitempty"`
	Function struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	} `json:"function"`
}

type NativeTooltypeMessage struct {
	Role       string               `json:"role"`
	Content    string               `json:"content"`
	ToolCalls  []NativeTypeToolCall `json:"tool_calls,omitempty"`
	ToolCallID string               `json:"tool_call_id,omitempty"`
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
	// Do not combine Tools with Format:"json"; gpt-oss can drop content.
}

type NativeToolChatResponse struct {
	Message NativeTooltypeMessage `json:"message"`
	// PromptEvalCount is Ollama's measured prompt size.
	PromptEvalCount int `json:"prompt_eval_count,omitempty"`
}

// OpenAI-compatible shapes, used by vLLM and OpenRouter. They differ from
// the Ollama types: responses are wrapped in "choices" and tool arguments are
// JSON strings.

// OpenAITypeToolCall is the OpenAI-style counterpart of NativeTypeToolCall.
type OpenAITypeToolCall struct {
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Index    int    `json:"index,omitempty"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type OpenAIChatMessage struct {
	Role       string               `json:"role"`
	Content    string               `json:"content"`
	ToolCalls  []OpenAITypeToolCall `json:"tool_calls,omitempty"`
	ToolCallID string               `json:"tool_call_id,omitempty"`
}

type OpenAIChatRequest struct {
	Model    string               `json:"model"`
	Messages []*OpenAIChatMessage `json:"messages"`
	Stream   bool                 `json:"stream"`
	Tools    []NativeTypeTool     `json:"tools,omitempty"`
	// vLLM defaults tool_choice to "none", so send "auto" explicitly.
	ToolChoice string `json:"tool_choice,omitempty"`
}

type OpenAIChatResponse struct {
	Choices []struct {
		Index        int               `json:"index"`
		Message      OpenAIChatMessage `json:"message"`
		FinishReason string            `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens int `json:"prompt_tokens"`
	} `json:"usage"`
}
