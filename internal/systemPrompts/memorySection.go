package systemPrompts

import (
	"fmt"
	"strings"
)

// MaxCasualMemoryChars caps injected memory so an unbounded memory file
// cannot eat the context window on every run.
const MaxCasualMemoryChars = 4000

// MaxCasualMemoryBytes is the soft budget for the memory file itself. Past
// it the section turns into a compaction notice instead of the full content
// (soft enforcement: writes are never rejected, the model is told to compact).
const MaxCasualMemoryBytes = 750 * 1024

// WithCasualMemory appends the long-term memory section to base. memoryPath
// is printed literally so the model knows exactly which file it may edit.
// Past MaxCasualMemoryBytes the section turns into a compaction notice
// instead of the full content (soft enforcement: writes are never rejected,
// the model is told to compact). An empty memory still renders so the model
// knows the file exists and is writable.
func WithCasualMemory(base, memoryPath, memoryData string) string {
	overBudget := len(memoryData) > MaxCasualMemoryBytes

	shown := memoryData
	truncated := false
	if runes := []rune(shown); len(runes) > MaxCasualMemoryChars {
		shown, truncated = string(runes[:MaxCasualMemoryChars]), true
	}

	var b strings.Builder
	b.WriteString(base)
	if overBudget {
		fmt.Fprintf(&b, "\n\n---\nLong-term memory notice: the memory file at %s is %d bytes, over the %d byte budget. It is NOT fully shown below.\n", memoryPath, len(memoryData), MaxCasualMemoryBytes)
		b.WriteString("Compact it back under budget with several smaller edit_file calls (delete stale entries, tighten wording, remove duplicates — each call must stay under the 256KB text limit). Do this before storing anything new.\n")
		b.WriteString("Preview (first part only):\n")
	} else {
		fmt.Fprintf(&b, "\n\n---\nLong-term memory (stored in %s — use it to personalize; never paste it verbatim unless asked):\n", memoryPath)
	}
	if strings.TrimSpace(shown) == "" {
		b.WriteString("(empty — nothing stored yet)\n")
	} else {
		b.WriteString(shown)
		if !strings.HasSuffix(shown, "\n") {
			b.WriteString("\n")
		}
	}
	if truncated {
		marker := fmt.Sprintf("[memory truncated to %d chars]\n", MaxCasualMemoryChars)
		if overBudget {
			marker = "[preview truncated]"
		}
		b.WriteString(marker)
	}
	if !overBudget {
		b.WriteString(fmt.Sprintf("You may update that file with edit_file (append=true appends to the end, otherwise replace an exact block) when something is worth keeping as long-term memory. Only store what is crucial; keep entries short. The file already exists at %s.", memoryPath))
	}
	return b.String()
}
