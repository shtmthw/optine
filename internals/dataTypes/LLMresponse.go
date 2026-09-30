package dataTypes

type AIResponse struct {
	Type      string         `json:"type"`
	Tool      string         `json:"tool,omitempty"`
	Arguments map[string]any `json:"arguments,omitempty"`
	Content   string         `json:"content,omitempty"`
}
