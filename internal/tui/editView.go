package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Edit diff rendering for successful edit_file tool calls.
//
// The harness emits a deterministic EDIT_OK block (see
// harnessTools.FormatEditResult). The TUI detects it by prefix and renders a
// structured view: header top-left (file, dir, lines, bytes), old/new
// side-by-side with tinted backgrounds, stacked below 100 columns.
// Long diffs show all rows; vertical overflow uses the existing wheel scroll.

var (
	editHeaderStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#D3D3D3"))

	editMetaStyle = lipgloss.NewStyle().
			Italic(true).
			Foreground(lipgloss.Color("99"))

	editOldStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("231")).
			Background(lipgloss.Color("52")).
			Padding(0, 1)

	editNewStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("231")).
			Background(lipgloss.Color("22")).
			Padding(0, 1)

	editOldTitleStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("231")).
				Background(lipgloss.Color("52")).
				Padding(0, 1)

	editNewTitleStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("231")).
				Background(lipgloss.Color("22")).
				Padding(0, 1)

	editDividerStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("99"))

	editColumnDividerStyle = lipgloss.NewStyle().
				Border(lipgloss.NormalBorder(), false, true, false, false).
				BorderForeground(lipgloss.Color("99"))

	editFooterStyle = lipgloss.NewStyle().
			Italic(true).
			Faint(true)
)

type editInfo struct {
	path        string
	dir         string
	file        string
	linesRange  string
	startLine   int
	endLine     int
	newCount    int
	bytesBefore int
	bytesAfter  int
	oldText     []string
	newText     []string
	footer      string
}

// parseEditInfo recognizes a harness EDIT_OK block. Second return is false
// splitOKHeadline strips the "TAG " prefix from a deterministic harness
// result line and pulls path/dir/file off the front, returning the
// unconsumed rest for kind-specific parsing. Shared by both parsers.
func splitOKHeadline(line, tag string) (path, dir, file, rest string, ok bool) {
	if !strings.HasPrefix(line, tag+" ") {
		return "", "", "", "", false
	}
	rest = strings.TrimPrefix(line, tag+" ")

	idx := strings.Index(rest, " dir=")
	if idx < 0 || !strings.HasPrefix(rest, "path=") {
		return "", "", "", "", false
	}
	path = rest[len("path="):idx]
	rest = rest[idx+len(" dir="):]

	idx = strings.Index(rest, " file=")
	if idx < 0 {
		return "", "", "", "", false
	}
	dir = rest[:idx]
	rest = rest[idx+len(" file="):]

	idx = strings.Index(rest, " lines=")
	if idx < 0 {
		return "", "", "", "", false
	}
	file = rest[:idx]
	rest = rest[idx+len(" lines="):]
	return path, dir, file, rest, true
}

// when content is any other event text (falls back to plain event style).
func parseEditInfo(content string) (*editInfo, bool) {
	if !strings.HasPrefix(content, "EDIT_OK ") {
		return nil, false
	}
	lines := strings.Split(content, "\n")
	if len(lines) < 5 {
		return nil, false
	}

	info := &editInfo{}
	var rest string
	var ok bool
	if info.path, info.dir, info.file, rest, ok = splitOKHeadline(lines[0], "EDIT_OK"); !ok {
		return nil, false
	}

	idx := strings.Index(rest, " (new ")
	if idx < 0 {
		return nil, false
	}
	info.linesRange = rest[:idx]
	rest = rest[idx+len(" (new "):]

	idx = strings.Index(rest, " lines) bytes=")
	if idx < 0 {
		return nil, false
	}
	var err error
	if info.newCount, err = strconv.Atoi(rest[:idx]); err != nil {
		return nil, false
	}
	rest = rest[idx+len(" lines) bytes="):]

	parts := strings.SplitN(rest, "->", 2)
	if len(parts) != 2 {
		return nil, false
	}
	if info.bytesBefore, err = strconv.Atoi(parts[0]); err != nil {
		return nil, false
	}
	if info.bytesAfter, err = strconv.Atoi(parts[1]); err != nil {
		return nil, false
	}

	bounds := strings.SplitN(info.linesRange, "-", 2)
	if len(bounds) != 2 {
		return nil, false
	}
	if info.startLine, err = strconv.Atoi(bounds[0]); err != nil {
		return nil, false
	}
	if info.endLine, err = strconv.Atoi(bounds[1]); err != nil {
		return nil, false
	}

	oldHdr := -1
	newHdr := -1
	for i, l := range lines {
		if strings.HasPrefix(l, "--- old") {
			oldHdr = i
		} else if strings.HasPrefix(l, "+++ new (starts line ") {
			newHdr = i
			break
		}
	}
	if oldHdr < 0 || newHdr < 0 || newHdr <= oldHdr {
		return nil, false
	}

	oldBody := lines[oldHdr+1 : newHdr]
	if len(oldBody) > 0 && strings.HasPrefix(oldBody[len(oldBody)-1], "[old truncated") {
		oldBody = oldBody[:len(oldBody)-1]
	}
	// Drop the single separator blank line the harness leaves before +++.
	for len(oldBody) > 0 && oldBody[len(oldBody)-1] == "" {
		oldBody = oldBody[:len(oldBody)-1]
	}

	footerIdx := -1
	for i := len(lines) - 1; i > newHdr; i-- {
		if strings.HasPrefix(lines[i], "This has been changed") {
			footerIdx = i
			break
		}
	}
	newEnd := len(lines)
	if footerIdx >= 0 {
		newEnd = footerIdx
		info.footer = lines[footerIdx]
	}
	newBody := lines[newHdr+1 : newEnd]
	if len(newBody) > 0 && strings.HasPrefix(newBody[len(newBody)-1], "[new truncated") {
		newBody = newBody[:len(newBody)-1]
	}
	for len(newBody) > 0 && newBody[len(newBody)-1] == "" {
		newBody = newBody[:len(newBody)-1]
	}

	info.oldText = oldBody
	info.newText = newBody
	return info, true
}

// renderFileHeader renders "file  meta" top-left. Truncation happens on the
// plain text before styling: truncating styled output would count ANSI
// escapes as runes and could cut mid-sequence. The file name (most important)
// is preserved longest; the meta tail is shortened first.
func renderFileHeader(file, meta string, width int) string {
	fileShown, metaShown := file, meta
	if width > 0 {
		if total := runeLen(fileShown) + 2 + runeLen(metaShown); total > width {
			overflow := total - width
			if spare := runeLen(metaShown) - overflow; spare >= 10 {
				metaShown = truncateRunes(metaShown, spare)
			} else {
				metaShown = truncateRunes(metaShown, 10)
				fileW := width - 2 - 10
				if fileW < 1 {
					fileW = 1
				}
				fileShown = truncateRunes(fileShown, fileW)
			}
		}
	}
	return editHeaderStyle.Render(fileShown) + "  " + editMetaStyle.Render(metaShown)
}

// renderEditInfo builds the structured edit view. Wide (>=100 cols) renders
// old/new side-by-side; narrower stacks old above new.
func renderEditInfo(info *editInfo, width int) string {
	header := renderFileHeader(info.file, info.dir+"  lines "+info.linesRange+"  bytes "+strconv.Itoa(info.bytesBefore)+"->"+strconv.Itoa(info.bytesAfter), width)

	footer := ""
	if info.footer != "" {
		footer = renderFooter(info.footer, width)
	}

	if width >= 100 {
		return header + "\n" + renderEditColumns(info, width) + "\n" + footer
	}
	return header + "\n" + renderEditStacked(info, width) + "\n" + footer
}

// renderFooter wraps the harness closing line to width so long paths never
// push past the window border. Plain text: the footer carries no markdown,
// so this is renderPlainWrapped with empty prefixes.
func renderFooter(footer string, width int) string {
	return renderPlainWrapped(footer, width, editFooterStyle, "", "")
}

func renderEditColumns(info *editInfo, width int) string {
	// One border cell separates the panels; each side keeps equal width.
	colW := (width - 1) / 2
	if colW < 20 {
		return renderEditStacked(info, width)
	}

	maxOld := info.endLine
	maxNew := info.startLine + info.newCount - 1
	if maxNew < info.startLine {
		maxNew = info.startLine
	}
	lineNoW := len(strconv.Itoa(max(maxOld, maxNew)))

	oldRows := editPanelRows(info.oldText, info.startLine, lineNoW, colW-2, true)
	newRows := editPanelRows(info.newText, info.startLine, lineNoW, colW-2, false)

	// Equal height so JoinHorizontal stays aligned.
	for len(oldRows) < len(newRows) {
		oldRows = append(oldRows, "")
	}
	for len(newRows) < len(oldRows) {
		newRows = append(newRows, "")
	}

	oldTitle := editOldTitleStyle.Width(colW).Render(fmt.Sprintf("— OLD  lines %d-%d", info.startLine, info.endLine))
	newTitle := editNewTitleStyle.Width(colW).Render(fmt.Sprintf("+ NEW  from line %d (%d lines)", info.startLine, info.newCount))

	oldPanel := editOldStyle.Width(colW).Render(strings.Join(oldRows, "\n"))
	newPanel := editNewStyle.Width(colW).Render(strings.Join(newRows, "\n"))

	// Real panel borders instead of a manual text divider: one continuous
	// purple rail down the full column height, same color as the quote rail.
	left := editColumnDividerStyle.Render(
		lipgloss.JoinVertical(lipgloss.Left, oldTitle, oldPanel),
	)

	return lipgloss.JoinHorizontal(lipgloss.Top,
		left,
		lipgloss.JoinVertical(lipgloss.Left, newTitle, newPanel),
	)
}

func renderEditStacked(info *editInfo, width int) string {
	colW := width
	if colW < 0 {
		colW = 0
	}
	maxOld := info.endLine
	maxNew := info.startLine + info.newCount - 1
	if maxNew < info.startLine {
		maxNew = info.startLine
	}
	lineNoW := len(strconv.Itoa(max(maxOld, maxNew)))

	contentW := colW - 2
	if colW <= 0 {
		contentW = 0 // no constraint before first WindowSizeMsg
	}
	oldRows := editPanelRows(info.oldText, info.startLine, lineNoW, contentW, true)
	newRows := editPanelRows(info.newText, info.startLine, lineNoW, contentW, false)

	var b strings.Builder
	if colW > 0 {
		b.WriteString(editOldTitleStyle.Width(colW).Render(fmt.Sprintf("— OLD  lines %d-%d", info.startLine, info.endLine)))
		b.WriteString("\n")
		b.WriteString(editOldStyle.Width(colW).Render(strings.Join(oldRows, "\n")))
		b.WriteString("\n")
		b.WriteString(editNewTitleStyle.Width(colW).Render(fmt.Sprintf("+ NEW  from line %d (%d lines)", info.startLine, info.newCount)))
		b.WriteString("\n")
		b.WriteString(editNewStyle.Width(colW).Render(strings.Join(newRows, "\n")))
	} else {
		b.WriteString(fmt.Sprintf("— OLD  lines %d-%d", info.startLine, info.endLine))
		b.WriteString("\n")
		b.WriteString(strings.Join(oldRows, "\n"))
		b.WriteString("\n")
		b.WriteString(fmt.Sprintf("+ NEW  from line %d (%d lines)", info.startLine, info.newCount))
		b.WriteString("\n")
		b.WriteString(strings.Join(newRows, "\n"))
	}
	return b.String()
}

// editPanelRows numbers each text line and truncates to fit the panel.
// isOld=false with empty input means a pure deletion: show a placeholder.
func editPanelRows(text []string, startLine, lineNoW, contentW int, isOld bool) []string {
	if len(text) == 0 || (len(text) == 1 && strings.TrimSpace(text[0]) == "") {
		if !isOld {
			return []string{"(empty — block deleted)"}
		}
		return []string{""}
	}
	rows := make([]string, 0, len(text))
	for i, l := range text {
		l = strings.ReplaceAll(l, "\t", "  ")
		prefix := fmt.Sprintf("%*d │ ", lineNoW, startLine+i)
		if contentW > 0 {
			avail := contentW - runeLen(prefix)
			if avail < 1 {
				avail = 1
			}
			l = truncateRunes(l, avail)
		}
		rows = append(rows, prefix+l)
	}
	return rows
}

// Write result rendering for successful write_file tool calls.
//
// The harness emits a deterministic WRITE_OK block (see
// harnessTools.FormatWriteResult). Single full-width green column with line
// numbers from 1; header top-left mirrors the edit header.

type writeInfo struct {
	path   string
	dir    string
	file   string
	lines  int
	bytes  int
	text   []string
	footer string
}

// parseWriteInfo recognizes a harness WRITE_OK block. Second return is false
// for any other event text.
func parseWriteInfo(content string) (*writeInfo, bool) {
	if !strings.HasPrefix(content, "WRITE_OK ") {
		return nil, false
	}
	lines := strings.Split(content, "\n")
	if len(lines) < 4 {
		return nil, false
	}

	info := &writeInfo{}
	var rest string
	var ok bool
	if info.path, info.dir, info.file, rest, ok = splitOKHeadline(lines[0], "WRITE_OK"); !ok {
		return nil, false
	}

	idx := strings.Index(rest, " bytes=")
	if idx < 0 {
		return nil, false
	}
	var err error
	if info.lines, err = strconv.Atoi(rest[:idx]); err != nil {
		return nil, false
	}
	if info.bytes, err = strconv.Atoi(rest[idx+len(" bytes="):]); err != nil {
		return nil, false
	}

	hdr := -1
	for i, l := range lines {
		if l == "--- content:" {
			hdr = i
			break
		}
	}
	if hdr < 0 {
		return nil, false
	}

	footerIdx := -1
	for i := len(lines) - 1; i > hdr; i-- {
		if strings.HasPrefix(lines[i], "This file was created") {
			footerIdx = i
			break
		}
	}
	end := len(lines)
	if footerIdx >= 0 {
		end = footerIdx
		info.footer = lines[footerIdx]
	}
	body := lines[hdr+1 : end]
	if len(body) > 0 && strings.HasPrefix(body[len(body)-1], "[content truncated") {
		body = body[:len(body)-1]
	}
	for len(body) > 0 && body[len(body)-1] == "" {
		body = body[:len(body)-1]
	}
	info.text = body
	return info, true
}

// renderWriteInfo builds the single-column created-file view.
func renderWriteInfo(info *writeInfo, width int) string {
	header := renderFileHeader(info.file, info.dir+"  lines "+strconv.Itoa(info.lines)+"  bytes "+strconv.Itoa(info.bytes), width)

	footer := ""
	if info.footer != "" {
		footer = renderFooter(info.footer, width)
	}

	colW := width
	if colW < 0 {
		colW = 0
	}
	maxLine := info.lines
	if maxLine < 1 {
		maxLine = 1
	}
	lineNoW := len(strconv.Itoa(maxLine))

	contentW := colW - 2
	if colW <= 0 {
		contentW = 0 // no constraint before first WindowSizeMsg
	}
	rows := writePanelRows(info.text, lineNoW, contentW)

	var b strings.Builder
	b.WriteString(header)
	b.WriteString("\n")
	if colW > 0 {
		b.WriteString(editNewTitleStyle.Width(colW).Render(fmt.Sprintf("+ NEW FILE  %d lines", info.lines)))
		b.WriteString("\n")
		b.WriteString(editNewStyle.Width(colW).Render(strings.Join(rows, "\n")))
	} else {
		b.WriteString(fmt.Sprintf("+ NEW FILE  %d lines", info.lines))
		b.WriteString("\n")
		b.WriteString(strings.Join(rows, "\n"))
	}
	b.WriteString("\n")
	b.WriteString(footer)
	return b.String()
}

// writePanelRows numbers content lines from 1. Empty file shows a placeholder.
func writePanelRows(text []string, lineNoW, contentW int) []string {
	if len(text) == 0 || (len(text) == 1 && strings.TrimSpace(text[0]) == "") {
		return []string{"(empty file)"}
	}
	rows := make([]string, 0, len(text))
	for i, l := range text {
		l = strings.ReplaceAll(l, "\t", "  ")
		prefix := fmt.Sprintf("%*d │ ", lineNoW, i+1)
		if contentW > 0 {
			avail := contentW - runeLen(prefix)
			if avail < 1 {
				avail = 1
			}
			l = truncateRunes(l, avail)
		}
		rows = append(rows, prefix+l)
	}
	return rows
}

func runeLen(s string) int {
	return len([]rune(s))
}

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
