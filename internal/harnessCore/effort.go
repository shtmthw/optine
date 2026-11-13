package harnessCore

import (
	"github.com/mattthew/optine/internal/dataTypes"
	"github.com/mattthew/optine/internal/harnessSession"
)

// effortWire is the effort field one request carries: Think for Ollama,
// Reasoning for OpenRouter, ReasoningEffort for vLLM. The zero value sends
// nothing.
type effortWire struct {
	Think           any
	Reasoning       *dataTypes.ReasoningRequest
	ReasoningEffort string
}

// wireEffort turns the session's level into the provider's field. It sends
// nothing at the default level or when the model does not accept the level,
// so a level kept across /model never reaches a model that would reject it.
// OpenRouter and vLLM spell off "none".
func wireEffort(provider string, support harnessSession.EffortSupport, level harnessSession.Effort) effortWire {
	if level == harnessSession.EffortDefault || !support.Accepts(level) {
		return effortWire{}
	}

	switch provider {
	case "Ollama":
		switch level {
		case harnessSession.EffortOff:
			return effortWire{Think: false}
		case harnessSession.EffortOn:
			return effortWire{Think: true}
		}
		return effortWire{Think: string(level)}

	case "Openrouter":
		switch level {
		case harnessSession.EffortOn:
			return effortWire{}
		case harnessSession.EffortOff:
			return effortWire{Reasoning: &dataTypes.ReasoningRequest{Effort: "none"}}
		}
		return effortWire{Reasoning: &dataTypes.ReasoningRequest{Effort: string(level)}}

	case "vLLM":
		switch level {
		case harnessSession.EffortOff:
			return effortWire{ReasoningEffort: "none"}
		case harnessSession.EffortLow, harnessSession.EffortMedium, harnessSession.EffortHigh:
			return effortWire{ReasoningEffort: string(level)}
		}
	}

	return effortWire{}
}
