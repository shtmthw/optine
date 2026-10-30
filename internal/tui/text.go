package tui

import "strings"

// Small text helpers shared by every renderer. Keeping them in one file stops
// width and truncation rules from drifting between the chat view, markdown,
// tables and the diff panels.

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}

	return v
}

// cutRunes truncates s to at most n runes without an ellipsis.
func cutRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}

	return s
}

// truncateRunes truncates s to n runes, replacing the last one with an
// ellipsis when there is room for it.
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}

func runeLen(s string) int {
	return len([]rune(s))
}

// promptSafe makes typed or pasted text fit the single-row prompt by turning
// line breaks and tabs into spaces.
func promptSafe(runes []rune) []rune {
	out := make([]rune, len(runes))

	for i, r := range runes {
		if r == '\n' || r == '\r' || r == '\t' {
			r = ' '
		}
		out[i] = r
	}

	return out
}

func deleteLastWord(s string) string {
	s = strings.TrimRight(s, " ")

	if i := strings.LastIndex(s, " "); i >= 0 {
		return s[:i+1]
	}

	return ""
}
