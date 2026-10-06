package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Markdown table rendering for model text.
//
// A block of consecutive lines starting with "|" whose second line is a
// delimiter row (|---|---|) renders as real columns: purple rails shared
// with the diff/quote language, bold header, per-column alignment from the
// delimiter colons, <br> as in-cell line breaks, inline **bold**/*italic*/`
// code` inside cells. Columns share the terminal width proportionally and
// wrap; anything malformed or too wide falls back to prose rendering
// (caller checks the second return).

var (
	tableSepStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("99"))
)

// minTableColW floors proportional shrinking; narrower tables fall back.
const minTableColW = 6

// tryTableBlock recognizes a markdown table starting at lines[i]. It returns
// the rendered block and the index of the first line past the table.
// ok=false means "not a table or doesn't fit": caller renders as prose.
func tryTableBlock(lines []string, i, width int) (out string, next int, ok bool) {
	if width < minWrapWidth || i+1 >= len(lines) {
		return "", 0, false
	}
	if !isTableRow(lines[i]) || !isDelimRow(lines[i+1]) {
		return "", 0, false
	}
	j := i + 2
	for j < len(lines) && isTableRow(lines[j]) {
		j++
	}
	rows := make([][]string, 0, j-i)
	align := parseDelimRow(lines[i+1])
	for _, ln := range lines[i:j] {
		if isDelimRow(ln) {
			continue
		}
		rows = append(rows, splitTableRow(ln))
	}
	if len(rows) == 0 {
		return "", 0, false
	}
	n := len(align)
	for _, r := range rows {
		if len(r) > n {
			n = len(r)
		}
	}
	// normalize ragged rows; extra alignment defaults to left
	for len(align) < n {
		align = append(align, 0)
	}
	for k, r := range rows {
		for len(r) < n {
			r = append(r, "")
		}
		rows[k] = r
	}

	// natural widths from visible text (<br> split, markers stripped)
	natural := make([]int, n)
	for ci := 0; ci < n; ci++ {
		best := 0
		for _, r := range rows {
			for _, part := range splitBreaks(r[ci]) {
				for _, wl := range wrapRuns(parseInline(part), 0) {
					if w := runLineLen(wl); w > best {
						best = w
					}
				}
			}
		}
		natural[ci] = best
	}

	widths := fitTableWidths(natural, n, width)
	if widths == nil {
		return "", 0, false
	}

	var b strings.Builder
	rail := tableSepStyle.Render("│")
	sep := tableSepStyle.Render("┼")
	dash := tableSepStyle.Render("─")

	// header is rows[0]; wrap its cells first
	header := make([][][]styledWord, n)
	for ci, cell := range rows[0] {
		header[ci] = wrapCellLines(cell, widths[ci])
	}
	emitRowLines(&b, header, widths, align, headingStyle, rail, sep, dash, true)
	for _, r := range rows[1:] {
		body := make([][][]styledWord, n)
		for ci, cell := range r {
			body[ci] = wrapCellLines(cell, widths[ci])
		}
		emitRowLines(&b, body, widths, align, agentTextBase, rail, sep, dash, false)
	}
	return strings.TrimRight(b.String(), "\n"), j, true
}

// wrapCellLines wraps one cell's text (<br> aware) into visual lines,
// keeping styles. Empty cells yield one empty line.
func wrapCellLines(cell string, colW int) [][]styledWord {
	var visual [][]styledWord
	for _, part := range splitBreaks(cell) {
		lines := wrapRuns(parseInline(part), colW)
		if len(lines) == 0 {
			visual = append(visual, nil)
			continue
		}
		visual = append(visual, lines...)
	}
	if len(visual) == 0 {
		visual = [][]styledWord{nil}
	}
	return visual
}

// emitRowLines renders one table row (possibly multi-line) with alignment.
func emitRowLines(b *strings.Builder, cells [][][]styledWord, widths []int, align []int, base lipgloss.Style, rail, sep, dash string, header bool) {
	n := len(cells)
	maxH := 0
	for _, v := range cells {
		if len(v) > maxH {
			maxH = len(v)
		}
	}
	// separator below the header; body rows have none
	for r := 0; r < maxH; r++ {
		b.WriteString(rail + " ")
		for ci := 0; ci < n; ci++ {
			var wl []styledWord
			if r < len(cells[ci]) {
				wl = cells[ci][r]
			}
			vis := runLineLen(wl)
			text := renderRunLine(wl, base)
			b.WriteString(padTableCell(text, vis, widths[ci], align[ci]))
			if ci < n-1 {
				b.WriteString(" " + rail + " ")
			}
		}
		b.WriteString(" " + rail + "\n")
	}
	if header {
		b.WriteString(rail)
		for ci := 0; ci < n; ci++ {
			b.WriteString(strings.Repeat(dash, widths[ci]+2))
			if ci < n-1 {
				b.WriteString(sep)
			}
		}
		b.WriteString(rail + "\n")
	}
}

// padTableCell pads styled text (visible length vis) to colW per alignment.
func padTableCell(text string, vis, colW, align int) string {
	if vis >= colW {
		return text
	}
	switch align {
	case 2: // right
		return strings.Repeat(" ", colW-vis) + text
	case 1: // center
		left := (colW - vis) / 2
		return strings.Repeat(" ", left) + text + strings.Repeat(" ", colW-vis-left)
	default: // left
		return text + strings.Repeat(" ", colW-vis)
	}
}

// fitTableWidths shares avail width proportionally; nil means fall back.
func fitTableWidths(natural []int, n, width int) []int {
	rails := 3*n + 1 // "│ " + (" │ ")*(n-1) + " │"
	avail := width - rails
	if avail < n*minTableColW {
		return nil
	}
	total := 0
	for _, w := range natural {
		total += w
	}
	if total <= avail {
		out := make([]int, n)
		copy(out, natural)
		for i, w := range out {
			if w < minTableColW {
				out[i] = minTableColW
			}
		}
		// re-check after flooring
		sum := 0
		for _, w := range out {
			sum += w
		}
		if sum > avail {
			return fitTableWidthsShrink(natural, n, avail)
		}
		return out
	}
	return fitTableWidthsShrink(natural, n, avail)
}

func fitTableWidthsShrink(natural []int, n, avail int) []int {
	total := 0
	for _, w := range natural {
		total += w
	}
	out := make([]int, n)
	assigned := 0
	for i, w := range natural {
		share := w * avail / total
		if share < minTableColW {
			share = minTableColW
		}
		out[i] = share
		assigned += share
	}
	// fix rounding drift on the last column
	out[n-1] -= assigned - avail
	if out[n-1] < minTableColW {
		return nil
	}
	return out
}

// runLineLen is the visible width of one visual line of words, mirroring
// renderRunLine's punctuation attachment.
func runLineLen(wl []styledWord) int {
	n := 0
	for i, w := range wl {
		if i > 0 && !noSpaceBefore(w.text) {
			n++
		}
		n += runeLen(w.text)
	}
	return n
}

// splitBreaks splits <br> variants into in-cell lines.
func splitBreaks(s string) []string {
	r := strings.NewReplacer("<br>", "\n", "<br/>", "\n", "<br />", "\n", "<BR>", "\n", "<BR/>", "\n", "<BR />", "\n")
	return strings.Split(r.Replace(s), "\n")
}

// isTableRow reports a line shaped like a table row.
func isTableRow(line string) bool {
	t := strings.TrimSpace(line)
	return strings.HasPrefix(t, "|") && strings.HasSuffix(t, "|") && strings.Count(t, "|") >= 2
}

// isDelimRow reports a |---|---| delimiter row.
func isDelimRow(line string) bool {
	t := strings.TrimSpace(line)
	if !strings.HasPrefix(t, "|") || !strings.HasSuffix(t, "|") {
		return false
	}
	inner := strings.Trim(t, "|")
	if strings.TrimSpace(inner) == "" {
		return false
	}
	for _, cell := range strings.Split(inner, "|") {
		c := strings.TrimSpace(cell)
		i := 0
		if strings.HasPrefix(c, ":") {
			i++
		}
		dashes := 0
		for i < len(c) && c[i] == '-' {
			dashes++
			i++
		}
		trail := c[i:]
		if dashes == 0 || (trail != "" && trail != ":") {
			return false
		}
	}
	return true
}

// parseDelimRow returns per-column alignment: 0 left, 1 center, 2 right.
func parseDelimRow(line string) []int {
	inner := strings.Trim(strings.TrimSpace(line), "|")
	var align []int
	for _, cell := range strings.Split(inner, "|") {
		c := strings.TrimSpace(cell)
		left, right := strings.HasPrefix(c, ":"), strings.HasSuffix(c, ":")
		switch {
		case left && right:
			align = append(align, 1)
		case right:
			align = append(align, 2)
		default:
			align = append(align, 0)
		}
	}
	return align
}

// splitTableRow splits a row into cells, honoring \| escapes.
func splitTableRow(line string) []string {
	t := strings.TrimSpace(line)
	t = strings.TrimPrefix(t, "|")
	t = strings.TrimSuffix(t, "|")
	const esc = "\x00PIPE\x00"
	t = strings.ReplaceAll(t, `\|`, esc)
	parts := strings.Split(t, "|")
	for i, p := range parts {
		parts[i] = strings.TrimSpace(strings.ReplaceAll(p, esc, "|"))
	}
	return parts
}
