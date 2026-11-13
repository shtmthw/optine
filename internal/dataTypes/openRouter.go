package dataTypes

import "encoding/json"

type OpenRouterModelsResponse struct {
	Data []OpenRouterModel `json:"data"`
}

type OpenRouterModel struct {
	ID                  string   `json:"id"`
	Name                string   `json:"name"`
	ContextLength       int      `json:"context_length"`
	SupportedParameters []string `json:"supported_parameters"`
	// Reasoning is nil when the catalog says nothing about reasoning.
	Reasoning *OpenRouterModelReasoning `json:"reasoning,omitempty"`

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

// OpenRouterModelReasoning is what a catalog entry says about effort control.
type OpenRouterModelReasoning struct {
	// SupportedEfforts stays raw because missing and null differ: missing
	// means no effort control, null means every level.
	SupportedEfforts json.RawMessage `json:"supported_efforts,omitempty"`
	DefaultEffort    string          `json:"default_effort,omitempty"`
	// Mandatory models cannot turn reasoning off.
	Mandatory bool `json:"mandatory,omitempty"`
}

// ReasoningRequest is OpenRouter's request-side reasoning setting.
type ReasoningRequest struct {
	Effort string `json:"effort"`
}
