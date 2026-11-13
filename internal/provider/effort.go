package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/mattthew/optine/internal/dataTypes"
	"github.com/mattthew/optine/internal/harnessSession"
)

// OllamaThinking is /api/show's thinking object: the values "think" accepts
// (level strings or booleans) and the model's own default.
type OllamaThinking struct {
	Values  []any `json:"values"`
	Default any   `json:"default"`
}

// EffortSupportFor works out which effort levels the model accepts. An error
// means it could not be told; the caller treats that as no effort control.
func EffortSupportFor(ctx context.Context, providerName, model string) (harnessSession.EffortSupport, error) {
	switch providerName {
	case "Ollama":
		meta, err := OllamaGetModelMetaData(ctx, model)
		if err != nil {
			return harnessSession.EffortSupport{}, err
		}
		return OllamaEffortSupport(meta), nil

	case "Openrouter":
		for _, m := range AllOpenrouterModels {
			if m.ID == model {
				return OpenRouterEffortSupport(m), nil
			}
		}
		return harnessSession.EffortSupport{}, fmt.Errorf("%s is not in the OpenRouter catalog", model)

	case "vLLM":
		ok, err := VLLMProbeEffort(ctx, model)
		if err != nil {
			return harnessSession.EffortSupport{}, err
		}
		if !ok {
			return harnessSession.EffortSupport{Note: "start vLLM with --reasoning-parser to use effort levels"}, nil
		}
		return vLLMEffortSupport, nil
	}

	return harnessSession.EffortSupport{}, fmt.Errorf("unknown provider %q", providerName)
}

// OllamaEffortSupport reads the levels from /api/show. A model with no
// thinking object but the "thinking" capability (an older Ollama) can only
// switch thinking on or off.
func OllamaEffortSupport(meta OllamaShowResponse) harnessSession.EffortSupport {
	if meta.Thinking == nil {
		if slices.Contains(meta.Capabilities, "thinking") {
			return harnessSession.NewEffortSupport([]harnessSession.Effort{harnessSession.EffortOn, harnessSession.EffortOff}, harnessSession.EffortDefault)
		}
		return harnessSession.EffortSupport{}
	}

	levels := make([]harnessSession.Effort, 0, len(meta.Thinking.Values))
	for _, v := range meta.Thinking.Values {
		if e, ok := ollamaThinkValue(v); ok {
			levels = append(levels, e)
		}
	}

	// Only false (or nothing) means thinking cannot be turned on.
	if len(levels) == 0 || slices.Equal(levels, []harnessSession.Effort{harnessSession.EffortOff}) {
		return harnessSession.EffortSupport{}
	}

	def, _ := ollamaThinkValue(meta.Thinking.Default)

	return harnessSession.NewEffortSupport(levels, def)
}

// ollamaThinkValue maps one "think" value onto a level.
func ollamaThinkValue(v any) (harnessSession.Effort, bool) {
	switch v := v.(type) {
	case bool:
		if v {
			return harnessSession.EffortOn, true
		}
		return harnessSession.EffortOff, true

	case string:
		e, ok := harnessSession.ParseEffort(v)
		return e, ok && e != harnessSession.EffortDefault
	}

	return harnessSession.EffortDefault, false
}

// openRouterAllEfforts is every level OpenRouter takes, apart from none (off).
var openRouterAllEfforts = []harnessSession.Effort{
	harnessSession.EffortMinimal, harnessSession.EffortLow, harnessSession.EffortMedium,
	harnessSession.EffortHigh, harnessSession.EffortXHigh, harnessSession.EffortMax,
}

// OpenRouterEffortSupport reads a catalog entry's reasoning object. A missing
// supported_efforts means no effort control and null means every level; a
// mandatory model is never offered off. An entry with no reasoning object
// but "reasoning" in its parameters gets the three common levels and no off,
// since whether it can be turned off is unknown.
func OpenRouterEffortSupport(model dataTypes.OpenRouterModel) harnessSession.EffortSupport {
	r := model.Reasoning
	if r == nil {
		if slices.Contains(model.SupportedParameters, "reasoning") {
			return harnessSession.NewEffortSupport([]harnessSession.Effort{harnessSession.EffortLow, harnessSession.EffortMedium, harnessSession.EffortHigh}, harnessSession.EffortDefault)
		}
		return harnessSession.EffortSupport{}
	}

	raw := bytes.TrimSpace(r.SupportedEfforts)

	var levels []harnessSession.Effort
	switch {
	case len(raw) == 0:
		return harnessSession.EffortSupport{}

	case bytes.Equal(raw, []byte("null")):
		levels = append(slices.Clone(openRouterAllEfforts), harnessSession.EffortOff)

	default:
		var names []string
		if err := json.Unmarshal(raw, &names); err != nil {
			return harnessSession.EffortSupport{}
		}
		for _, name := range names {
			if e, ok := openRouterEffort(name); ok {
				levels = append(levels, e)
			}
		}
	}

	if r.Mandatory {
		levels = slices.DeleteFunc(levels, func(e harnessSession.Effort) bool { return e == harnessSession.EffortOff })
	}

	def, _ := openRouterEffort(r.DefaultEffort)

	return harnessSession.NewEffortSupport(levels, def)
}

// openRouterEffort maps an OpenRouter level name onto a level; none is off.
func openRouterEffort(name string) (harnessSession.Effort, bool) {
	if strings.EqualFold(strings.TrimSpace(name), "none") {
		return harnessSession.EffortOff, true
	}

	e, _ := harnessSession.ParseEffort(name)
	return e, slices.Contains(openRouterAllEfforts, e)
}

// vLLMEffortSupport is what reasoning_effort takes: low, medium, high and
// none (off). What each level does is up to the model's chat template.
var vLLMEffortSupport = harnessSession.NewEffortSupport([]harnessSession.Effort{
	harnessSession.EffortOff, harnessSession.EffortLow, harnessSession.EffortMedium, harnessSession.EffortHigh,
}, harnessSession.EffortDefault)

// vLLMChatCompletionsURL is the vLLM endpoint the effort probe posts to.
const vLLMChatCompletionsURL = "http://localhost:8000/v1/chat/completions"

// VLLMProbeEffort sends one short request with reasoning_effort set and
// reports whether the reply carries reasoning. vLLM has no API that says so,
// and a server started without --reasoning-parser returns none. A server
// that rejects the field (400) is reported as unsupported, not as an error.
func VLLMProbeEffort(ctx context.Context, modelName string) (bool, error) {
	payload := map[string]any{
		"model": modelName,
		"messages": []map[string]string{
			{"role": "user", "content": "Reply with OK."},
		},
		"reasoning_effort": "low",
		"max_tokens":       64,
		"temperature":      0,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return false, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, vLLMChatCompletionsURL, bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := vllmClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusBadRequest:
		return false, nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return false, ErrNeedsKey
	default:
		return false, fmt.Errorf("vLLM effort probe returned %d: %s", resp.StatusCode, bodySnippet(resp.Body))
	}

	var r struct {
		Choices []struct {
			Message struct {
				Reasoning        string `json:"reasoning"`
				ReasoningContent string `json:"reasoning_content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return false, fmt.Errorf("decoding the vLLM effort probe reply: %w", err)
	}

	if len(r.Choices) == 0 {
		return false, nil
	}
	msg := r.Choices[0].Message

	return strings.TrimSpace(msg.Reasoning+msg.ReasoningContent) != "", nil
}
