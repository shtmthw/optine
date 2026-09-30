package dataTypes

type NativeTypeToolCall struct {
	Function struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	} `json:"function"`
}

type NativeTooltypeMessage struct {
	Role      string               `json:"role"`
	Content   string               `json:"content"`
	ToolCalls []NativeTypeToolCall `json:"tool_calls,omitempty"`
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
