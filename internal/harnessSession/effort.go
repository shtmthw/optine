package harnessSession

import (
	"slices"
	"strings"
)

// Effort is the reasoning effort the user picked with /effort. It is one level
// per session, sent with every request the active model accepts it on.
type Effort string

const (
	// EffortDefault sends no effort field, so the model decides. It is the
	// zero value, so sessions saved before /effort load at it.
	EffortDefault Effort = ""
	// EffortOff turns thinking off where the provider allows it.
	EffortOff Effort = "off"
	// EffortOn turns thinking on, for models that can only switch it on or
	// off (Ollama's boolean thinking models).
	EffortOn      Effort = "on"
	EffortMinimal Effort = "minimal"
	EffortLow     Effort = "low"
	EffortMedium  Effort = "medium"
	EffortHigh    Effort = "high"
	EffortXHigh   Effort = "xhigh"
	EffortMax     Effort = "max"
)

// effortOrder is every level, in the order they are listed, lowest first.
var effortOrder = []Effort{EffortOff, EffortOn, EffortMinimal, EffortLow, EffortMedium, EffortHigh, EffortXHigh, EffortMax}

// String is the level as the user types and reads it.
func (e Effort) String() string {
	if e == EffortDefault {
		return "default"
	}

	return string(e)
}

// ParseEffort reads a level the user typed. "default" (or nothing) is
// EffortDefault; anything that is not a known level is refused, as
// EffortDefault and false.
func ParseEffort(s string) (Effort, bool) {
	e := Effort(strings.ToLower(strings.TrimSpace(s)))
	if e == "" || e == "default" {
		return EffortDefault, true
	}
	if !slices.Contains(effortOrder, e) {
		return EffortDefault, false
	}

	return e, true
}

// EffortSupport is what the active model accepts. It is worked out each time
// a model is picked and is never saved, since it belongs to the model rather
// than the conversation.
type EffortSupport struct {
	// Levels are the accepted levels, lowest first, without EffortDefault.
	// Empty means the model has no effort control.
	Levels []Effort
	// Default is the level the model uses when sent nothing, when the
	// provider reports it; EffortDefault when unknown.
	Default Effort
	// Note says what would give the model effort control, when something
	// would (a vLLM server started without a reasoning parser).
	Note string
}

// NewEffortSupport builds a support from the levels a provider reported,
// dropping unknown and repeated ones and sorting the rest lowest first. A
// default the model does not list is dropped.
func NewEffortSupport(levels []Effort, def Effort) EffortSupport {
	kept := slices.DeleteFunc(slices.Clone(effortOrder), func(e Effort) bool {
		return !slices.Contains(levels, e)
	})

	if !slices.Contains(kept, def) {
		def = EffortDefault
	}

	return EffortSupport{Levels: kept, Default: def}
}

// Supported reports whether the model takes an effort setting at all.
func (s EffortSupport) Supported() bool {
	return len(s.Levels) > 0
}

// Accepts reports whether level can be sent to this model. EffortDefault is
// always accepted, since it sends nothing.
func (s EffortSupport) Accepts(level Effort) bool {
	return level == EffortDefault || slices.Contains(s.Levels, level)
}
