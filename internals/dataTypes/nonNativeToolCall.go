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
