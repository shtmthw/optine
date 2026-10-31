package dataTypes

type OpenRouterModelsResponse struct {
	Data []OpenRouterModel `json:"data"`
}

type OpenRouterModel struct {
	ID                  string   `json:"id"`
	Name                string   `json:"name"`
	ContextLength       int      `json:"context_length"`
	SupportedParameters []string `json:"supported_parameters"`

	Pricing struct {
		Prompt     string `json:"prompt"`
		Completion string `json:"completion"`
	} `json:"pricing"`
}

type OpenRouterErrorResponse struct {
	Error struct {
		Message  string `json:"message"`
		Metadata struct {
			RemedyHint string `json:"remedy_hint"`
		} `json:"metadata"`
	} `json:"error"`
}
