package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Markdown rendering for agent and plain messages, consumed by View in
// model.go.
//
// Two wrappers coexist on purpose: wrapWords is for plain text (user input
// echo, errors, plain events, footers) that must never interpret markdown,
// while wrapRuns/parseInline is for model text. Do not "simplify" one into
// the other.

// minWrapWidth disables message wrapping below this terminal width; the
// prompt already pins itself, and wrapping into <20 columns is unreadable.
const minWrapWidth = 20

// wrapWords word-wraps one paragraph (no newlines) to width runes. Overlong
// words (URLs) are hard-broken. Tabs are expanded first so width math holds.
func wrapWords(s string, width int) []string {
	s = strings.ReplaceAll(s, "\t", "  ")
	if width <= 0 || runeLen(s) <= width {
		return []string{s}
	}
	words := strings.Fields(s)
	if len(words) == 0 {
		return []string{""}
	}
	var lines []string
	cur := ""
	flush := func() {
		if cur != "" {
			lines = append(lines, cur)
			cur = ""
		}
	}
	for _, w := range words {
		for runeLen(w) > width {
			flush()
			r := []rune(w)
			lines = append(lines, string(r[:width]))
			w = string(r[width:])
		}
		if cur == "" {
			cur = w
		} else if runeLen(cur)+1+runeLen(w) <= width {
			cur += " " + w
		} else {
			flush()
			cur = w
		}
	}
	flush()
	return lines
}

// truncateLine caps one line (code fences, which must not reflow).
func truncateLine(s string, width int) string {
	s = strings.ReplaceAll(s, "\t", "  ")
	if width > 0 && runeLen(s) > width {
		return truncateRunes(s, width)
	}
	return s
}

// textRun is a visible-text segment with combined inline styles.
type textRun struct {
	text               string
	bold, italic, code bool
}

// styledWord is one wrappable word keeping its inline styles.
type styledWord struct {
	text string
	textRun
}

// parseInline splits one line into styled runs. Code spans win over emphasis
// and are never re-parsed; bold/italic inners are parsed recursively so
// `code` inside **bold** still renders as code. Unclosed markers stay
// literal. No escape handling.
func parseInline(s string) []textRun {
	var runs []textRun
	emit := func(t string, bold, italic, code bool) {
		if t != "" {
			runs = append(runs, textRun{t, bold, italic, code})
		}
	}
	emitRuns := func(inner []textRun, bold, italic bool) {
		for _, r := range inner {
			runs = append(runs, textRun{r.text, r.bold || bold, r.italic || italic, r.code})
		}
	}
	for len(s) > 0 {
		iTick, tickOK := -1, false
		if i := strings.Index(s, "`"); i >= 0 {
			if strings.Index(s[i+1:], "`") >= 0 {
				iTick, tickOK = i, true
			}
		}
		iBold, boldOK := -1, false
		if i := strings.Index(s, "**"); i >= 0 {
			if strings.Index(s[i+2:], "**") >= 0 {
				iBold, boldOK = i, true
			}
		}
		iStar, starOK := -1, false
		for i := strings.Index(s, "*"); i >= 0; {
			if !strings.HasPrefix(s[i:], "**") {
				if strings.Index(s[i+1:], "*") >= 0 {
					iStar, starOK = i, true
				}
				break
			}
			// a "**" opener is handled as bold, look past it
			next := strings.Index(s[i+2:], "*")
			if next < 0 {
				break
			}
			i += 2 + next
		}
		best, openLen, closeLen := -1, 0, 0
		kind := 0 // 1=code 2=bold 3=italic
		if tickOK && (best < 0 || iTick < best) {
			best, kind, openLen, closeLen = iTick, 1, 1, 1
		}
		if boldOK && (best < 0 || iBold < best) {
			best, kind, openLen, closeLen = iBold, 2, 2, 2
		}
		if starOK && (best < 0 || iStar < best) {
			best, kind, openLen, closeLen = iStar, 3, 1, 1
		}
		if best < 0 {
			emit(s, false, false, false)
			break
		}
		emit(s[:best], false, false, false)
		rest := s[best+openLen:]
		// find the matching closer for this opener kind
		closer := "*"
		if kind == 1 {
			closer = "`"
		} else if kind == 2 {
			closer = "**"
		}
		// for a lone "*", don't close on the "*" of a "**" pair
		closeIdx := -1
		if kind == 3 {
			for i := 0; i < len(rest); {
				j := strings.Index(rest[i:], "*")
				if j < 0 {
					break
				}
				j += i
				if strings.HasPrefix(rest[j:], "**") {
					i = j + 2
					continue
				}
				closeIdx = j
				break
			}
		} else {
			closeIdx = strings.Index(rest, closer)
		}
		if closeIdx < 0 { // closer vanished; treat opener literally
			emit(s[:best+openLen], false, false, false)
			s = rest
			continue
		}
		switch kind {
		case 1:
			emit(rest[:closeIdx], false, false, true)
		case 2:
			emitRuns(parseInline(rest[:closeIdx]), true, false)
		default:
			emitRuns(parseInline(rest[:closeIdx]), false, true)
		}
		s = rest[closeIdx+closeLen:]
	}
	return runs
}

// wrapRuns fills visual lines to width measuring visible runes; words keep
// their styles and overlong words hard-break keeping them.
func wrapRuns(runs []textRun, width int) [][]styledWord {
	var words []styledWord
	for _, rn := range runs {
		for _, f := range strings.Fields(rn.text) {
			words = append(words, styledWord{f, rn})
		}
	}
	if len(words) == 0 {
		return nil
	}
	if width <= 0 {
		return [][]styledWord{words}
	}
	var lines [][]styledWord
	cur := []styledWord{}
	curLen := 0
	flush := func() {
		if len(cur) > 0 {
			lines = append(lines, cur)
			cur = nil
			curLen = 0
		}
	}
	for _, w := range words {
		for runeLen(w.text) > width {
			flush()
			r := []rune(w.text)
			lines = append(lines, []styledWord{{string(r[:width]), w.textRun}})
			w.text = string(r[width:])
		}
		if curLen == 0 {
			cur, curLen = []styledWord{w}, runeLen(w.text)
		} else if curLen+1+runeLen(w.text) <= width {
			cur, curLen = append(cur, w), curLen+1+runeLen(w.text)
		} else {
			flush()
			cur, curLen = []styledWord{w}, runeLen(w.text)
		}
	}
	flush()
	return lines
}

// mergeInline returns the text style for a run: the line base look plus the
// inline flags. Code keeps its own fill on top of emphasis.
func mergeInline(base lipgloss.Style, rn textRun) lipgloss.Style {
	s := base
	if rn.bold {
		s = s.Copy().Bold(true)
	}
	if rn.italic {
		s = s.Copy().Italic(true)
	}
	if rn.code {
		s = s.Copy().Foreground(lipgloss.Color("231")).Background(lipgloss.Color("237"))
	}
	return s
}

// renderRunLine joins one visual line, spaces inheriting the previous word's
// style so code spans don't stripe. Leading punctuation (",", ".", …) attaches
// to the previous word without a gap.
func renderRunLine(words []styledWord, base lipgloss.Style) string {
	var b strings.Builder
	for i, w := range words {
		if i > 0 && !noSpaceBefore(w.text) {
			b.WriteString(mergeInline(base, words[i-1].textRun).Render(" "))
		}
		b.WriteString(mergeInline(base, w.textRun).Render(w.text))
	}
	return b.String()
}

// noSpaceBefore reports punctuation that hugs the previous word.
func noSpaceBefore(w string) bool {
	if w == "" {
		return true
	}
	switch []rune(w)[0] {
	case ',', '.', ';', ':', '!', '?', '%', ')', ']', '}', '’', '”':
		return true
	}
	return false
}

// renderAgentContent lays out model text for the terminal width: prose is
// word-wrapped with **bold**, *italic* and `code` interpreted, "> " quotes
// get a purple rail, "# " headings go bold, fenced code becomes a dark panel
// (never reflowed), GitHub-style tables become real columns, and runs of
// blank lines collapse to one.
func renderAgentContent(content string, width int) string {
	if width < minWrapWidth {
		return agentStyle.Render(content)
	}
	var b strings.Builder
	inFence := false
	fenceLang := ""
	var fenceLines []string
	blankRun := 0
	first := true
	emit := func(s string) {
		if !first {
			b.WriteString("\n")
		}
		first = false
		b.WriteString(s)
	}
	flushFence := func() {
		title := fenceLang
		if title == "" {
			title = "code"
		}
		emit(codeBlockTitleStyle.Width(width).Render(title))
		emit(codeBlockStyle.Width(width).Render(strings.Join(fenceLines, "\n")))
		fenceLines = nil
	}
	for i, lines := 0, strings.Split(content, "\n"); i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimLeft(line, " \t")
		if !inFence {
			if table, next, ok := tryTableBlock(lines, i, width); ok {
				blankRun = 0
				emit(table)
				i = next - 1
				continue
			}
		}
		if strings.HasPrefix(trimmed, "```") {
			if !inFence {
				inFence = true
				fenceLang = strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
				fenceLines = nil
			} else {
				inFence = false
				flushFence()
			}
			blankRun = 0
			continue
		}
		if inFence {
			fenceLines = append(fenceLines, truncateLine(line, width-2))
			continue
		}
		if strings.TrimSpace(line) == "" {
			blankRun++
			if blankRun > 1 {
				continue
			}
			emit("")
			continue
		}
		blankRun = 0
		// renderProse wraps each <br> part continuously (no blank between).
		renderProse := func(text string, base lipgloss.Style) {
			for _, part := range splitBreaks(text) {
				for _, wl := range wrapRuns(parseInline(part), width) {
					emit(renderRunLine(wl, base))
				}
			}
		}
		switch {
		case strings.HasPrefix(trimmed, ">"):
			q := strings.TrimLeft(trimmed[1:], " ")
			for _, part := range splitBreaks(q) {
				for _, wl := range wrapRuns(parseInline(part), width-2) {
					emit(editDividerStyle.Render("│ ") + renderRunLine(wl, quoteTextBase))
				}
			}
		case strings.HasPrefix(trimmed, "#"):
			h := strings.TrimLeft(strings.TrimLeft(trimmed, "#"), " ")
			for _, part := range splitBreaks(h) {
				for _, wl := range wrapRuns(parseInline(part), width) {
					emit(renderRunLine(wl, headingStyle))
				}
			}
		default:
			renderProse(line, agentTextBase)
		}
	}
	if inFence {
		flushFence() // unclosed fence still renders as a panel
	}
	return b.String()
}

// renderPlainWrapped wraps non-agent text (user input echo, errors, plain
// events) to width without any markdown interpretation. Wrapped continuation
// lines use restPrefix so they don't repeat quote-looking markers.
func renderPlainWrapped(content string, width int, style lipgloss.Style, firstPrefix, restPrefix string) string {
	if width < minWrapWidth {
		return style.Render(firstPrefix + content)
	}
	var b strings.Builder
	first := true
	emit := func(s string) {
		if !first {
			b.WriteString("\n")
		}
		first = false
		b.WriteString(s)
	}
	for _, line := range strings.Split(content, "\n") {
		if strings.TrimSpace(line) == "" {
			emit("")
			continue
		}
		prefix := firstPrefix
		for _, wl := range wrapWords(line, width-runeLen(prefix)) {
			emit(style.Render(prefix + wl))
			prefix = restPrefix
		}
	}
	return b.String()
}
