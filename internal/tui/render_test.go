package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattthew/optine/internal/harnessTools"
)

// ------------------------------------------------ inline + wrapping primitives

func TestParseInlineStyles(t *testing.T) {
	runs := parseInline("plain **bold** *ital* `code`")

	var bold, italic, code bool
	for _, r := range runs {
		if r.text == "bold" && r.bold {
			bold = true
		}
		if r.text == "ital" && r.italic {
			italic = true
		}
		if r.text == "code" && r.code {
			code = true
		}
	}
	if !bold || !italic || !code {
		t.Fatalf("inline markers not decoded: %+v", runs)
	}
}

// Unclosed markers stay literal rather than swallowing the rest of the line.
func TestParseInlineUnclosedStaysLiteral(t *testing.T) {
	runs := parseInline("a **b")
	var joined strings.Builder
	for _, r := range runs {
		if r.bold {
			t.Fatalf("unclosed ** should not be bold: %+v", runs)
		}
		joined.WriteString(r.text)
	}
	if !strings.Contains(joined.String(), "b") {
		t.Fatalf("literal text lost: %q", joined.String())
	}
}

func TestWrapWords(t *testing.T) {
	lines := wrapWords("the quick brown fox jumps", 10)
	for _, l := range lines {
		if runeLen(l) > 10 {
			t.Fatalf("line over width: %q", l)
		}
	}
	if got := wrapWords("short", 80); len(got) != 1 || got[0] != "short" {
		t.Fatalf("short line should not wrap: %v", got)
	}
	// overlong single word is hard-broken
	lines = wrapWords(strings.Repeat("x", 25), 10)
	if len(lines) != 3 {
		t.Fatalf("overlong word split into %d lines, want 3", len(lines))
	}
}

func TestNoSpaceBeforeAndRunLineLen(t *testing.T) {
	if !noSpaceBefore(".") || noSpaceBefore("word") {
		t.Fatal("noSpaceBefore is wrong")
	}
	line := []styledWord{{text: "end"}, {text: "."}}
	if got := runLineLen(line); got != 4 { // "end." no space before punctuation
		t.Fatalf("runLineLen = %d, want 4", got)
	}
	spaced := []styledWord{{text: "a"}, {text: "b"}}
	if got := runLineLen(spaced); got != 3 { // "a b"
		t.Fatalf("runLineLen = %d, want 3", got)
	}
}

// ------------------------------------------------ block rendering (smoke)

// renderAgentContent must interpret the block constructs without panicking and
// keep every line within a sane width budget.
func TestRenderAgentContentBlocks(t *testing.T) {
	m := newTestModel()
	m, _ = update(m, tea.WindowSizeMsg{Width: 60, Height: 40})

	content := strings.Join([]string{
		"# Heading",
		"",
		"> a quote line",
		"",
		"para with **bold** and `code`",
		"",
		"```go",
		"func main() {}",
		"```",
	}, "\n")

	out := renderAgentContent(content, 60)
	for _, want := range []string{"Heading", "a quote line", "para with", "func main()"} {
		if !strings.Contains(out, want) {
			t.Fatalf("render missing %q:\n%s", want, out)
		}
	}
}

// Below the wrap minimum, agent text is passed through unstyled/unwrapped.
func TestRenderAgentContentNarrowPassthrough(t *testing.T) {
	if got := renderAgentContent("**raw**", 5); !strings.Contains(got, "raw") {
		t.Fatalf("narrow render = %q", got)
	}
}

// ---------------------------------------------------------------- markdown tables

func TestIsTableRowAndDelimRow(t *testing.T) {
	if !isTableRow("| a | b |") || isTableRow("no pipes") || isTableRow("| no trailing pipe") {
		t.Fatal("isTableRow is wrong")
	}
	if !isDelimRow("| --- | :--: |") || isDelimRow("| abc |") || isDelimRow("|   |") {
		t.Fatal("isDelimRow is wrong")
	}
}

func TestParseDelimAndSplitTableRow(t *testing.T) {
	align := parseDelimRow("| :--- | :--: | ---: | --- |")
	want := []int{0, 1, 2, 0} // left, center, right, default
	if len(align) != len(want) {
		t.Fatalf("align length = %d, want %d", len(align), len(want))
	}
	for i := range want {
		if align[i] != want[i] {
			t.Fatalf("align[%d] = %d, want %d", i, align[i], want[i])
		}
	}

	cells := splitTableRow("| a \\| b | c |")
	if len(cells) != 2 || cells[0] != "a | b" || cells[1] != "c" {
		t.Fatalf("escaped pipe not honored: %v", cells)
	}
}

// A well-formed table renders as columns and consumes the right number of
// lines; a too-narrow terminal falls back to prose (ok=false).
func TestTryTableBlock(t *testing.T) {
	lines := []string{
		"| Name | Qty |",
		"| --- | ---: |",
		"| apple | 3 |",
		"| pear | 12 |",
		"after",
	}

	out, next, ok := tryTableBlock(lines, 0, 60)
	if !ok {
		t.Fatal("valid table should render")
	}
	if next != 4 {
		t.Fatalf("table should consume 4 lines, got %d", next)
	}
	for _, want := range []string{"Name", "apple", "pear"} {
		if !strings.Contains(out, want) {
			t.Fatalf("table output missing %q:\n%s", want, out)
		}
	}

	if _, _, ok := tryTableBlock(lines, 0, 5); ok {
		t.Fatal("narrow table should fall back")
	}
	if _, _, ok := tryTableBlock([]string{"| a | b |"}, 0, 60); ok {
		t.Fatal("table without a delimiter row should fall back")
	}
}

func TestFitTableWidths(t *testing.T) {
	// Plenty of room: natural widths plus the column floor.
	out := fitTableWidths([]int{4, 4}, 2, 60)
	if len(out) != 2 || out[0] < minTableColW {
		t.Fatalf("fitTableWidths = %v", out)
	}
	// Too narrow to fit even the floors: nil means fall back.
	if got := fitTableWidths([]int{4, 4}, 2, 8); got != nil {
		t.Fatalf("too-narrow table should return nil, got %v", got)
	}
}

// ---------------------------------------------------------------- edit/write parse

// parseEditInfo reads a real harness EDIT_OK block and pulls out the fields the
// view needs, so the TUI parser can never drift from the harness formatter.
func TestParseEditInfoRoundTrip(t *testing.T) {
	raw := harnessTools.FormatEditResult(
		"/work/a.go", "/work", "a.go",
		3, 4, 2, 10, 22,
		"old line\n", "new line\n",
	)

	info, ok := parseEditInfo(raw)
	if !ok {
		t.Fatalf("parseEditInfo rejected a real EDIT_OK block:\n%s", raw)
	}
	if info.path != "/work/a.go" || info.dir != "/work" || info.file != "a.go" {
		t.Fatalf("headline fields wrong: %+v", info)
	}
	if info.startLine != 3 || info.endLine != 4 || info.newCount != 2 {
		t.Fatalf("line fields wrong: %+v", info)
	}
	if info.bytesBefore != 10 || info.bytesAfter != 22 {
		t.Fatalf("byte fields wrong: %+v", info)
	}
	if len(info.oldText) == 0 || len(info.newText) == 0 {
		t.Fatalf("bodies not parsed: old=%v new=%v", info.oldText, info.newText)
	}
	if !strings.Contains(info.footer, "changed") {
		t.Fatalf("footer missing: %q", info.footer)
	}

	if _, ok := parseEditInfo("WRITE_OK whatever"); ok {
		t.Fatal("a non-EDIT_OK block must not parse as an edit")
	}
}

// parseWriteInfo reads a real harness WRITE_OK block.
func TestParseWriteInfoRoundTrip(t *testing.T) {
	raw := harnessTools.FormatWriteResult("/work/new.txt", "/work", "new.txt", "line one\nline two\n")

	info, ok := parseWriteInfo(raw)
	if !ok {
		t.Fatalf("parseWriteInfo rejected a real WRITE_OK block:\n%s", raw)
	}
	if info.file != "new.txt" || info.lines != 2 {
		t.Fatalf("write info wrong: %+v", info)
	}
	if len(info.text) == 0 || !strings.Contains(strings.Join(info.text, "\n"), "line one") {
		t.Fatalf("content not parsed: %v", info.text)
	}

	if _, ok := parseWriteInfo("EDIT_OK nope"); ok {
		t.Fatal("an EDIT_OK block must not parse as a write")
	}
}

// ---------------------------------------------------------------- panel rows

func TestEditPanelRowsAndDeletionPlaceholder(t *testing.T) {
	rows := editPanelRows([]string{"alpha", "beta"}, 5, 2, 40, false)
	if len(rows) != 2 {
		t.Fatalf("want 2 rows, got %v", rows)
	}
	if !strings.Contains(rows[0], "alpha") || !strings.Contains(rows[0], " 5") {
		t.Fatalf("first row should carry line number 5: %q", rows[0])
	}

	// Empty input on the new side is a deletion placeholder.
	rows = editPanelRows(nil, 1, 1, 40, false)
	if len(rows) != 1 || !strings.Contains(rows[0], "deleted") {
		t.Fatalf("deletion placeholder missing: %v", rows)
	}
}

func TestWritePanelRowsNumbersFromOne(t *testing.T) {
	rows := writePanelRows([]string{"a", "b"}, 1, 40)
	if len(rows) != 2 || !strings.HasPrefix(rows[0], "1 ") {
		t.Fatalf("write rows should number from 1: %v", rows)
	}
	if rows := writePanelRows(nil, 1, 40); !strings.Contains(rows[0], "empty file") {
		t.Fatalf("empty file placeholder missing: %v", rows)
	}
}

// renderEditInfo picks side-by-side at width >= 100 and stacked below it, and
// the header shows the file name at both widths.
func TestRenderEditInfoWidths(t *testing.T) {
	info := &editInfo{
		path: "/work/a.go", dir: "/work", file: "a.go",
		linesRange: "3-4", startLine: 3, endLine: 4, newCount: 2,
		bytesBefore: 10, bytesAfter: 22,
		oldText: []string{"old"}, newText: []string{"new"},
	}

	wide := renderEditInfo(info, 120)
	stacked := renderEditInfo(info, 60)
	for name, out := range map[string]string{"wide": wide, "stacked": stacked} {
		if !strings.Contains(out, "a.go") {
			t.Fatalf("%s render missing the file name:\n%s", name, out)
		}
	}
}

// renderWriteInfo shows the created file name and the new-file banner.
func TestRenderWriteInfo(t *testing.T) {
	info := &writeInfo{path: "/work/n.txt", dir: "/work", file: "n.txt", lines: 1, bytes: 4, text: []string{"data"}}
	out := renderWriteInfo(info, 80)
	if !strings.Contains(out, "n.txt") || !strings.Contains(out, "NEW FILE") {
		t.Fatalf("write render wrong:\n%s", out)
	}
}

// renderFileHeader keeps the file name when the meta tail overflows.
func TestRenderFileHeaderTruncatesMetaFirst(t *testing.T) {
	out := renderFileHeader("main.go", strings.Repeat("x", 200), 40)
	if !strings.Contains(out, "main.go") {
		t.Fatalf("file name should survive truncation:\n%s", out)
	}
}

// quick sanity on the helper the header uses, so width math is exercised.
func TestTruncateRunes(t *testing.T) {
	if got := truncateRunes("héllo", 3); runeLen(got) != 3 {
		t.Fatalf("truncateRunes = %q", got)
	}
	if got := truncateRunes("abc", 5); got != "abc" {
		t.Fatalf("truncateRunes passthrough = %q", got)
	}
	// n <= 1 returns the first n runes with no ellipsis.
	if got := truncateRunes("abcdef", 1); got != "a" {
		t.Fatalf("truncateRunes n=1 = %q, want a", got)
	}
}
