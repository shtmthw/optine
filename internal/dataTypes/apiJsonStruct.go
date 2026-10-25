package dataTypes

type AuthConfig struct {
	Connections map[string]Connection `json:"connections"`
}

type Connection struct {
	APIKey string `json:"api_key"`
}
