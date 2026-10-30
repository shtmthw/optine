package tui

import (
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattthew/optine/internal/harnessTools"
	"github.com/muesli/termenv"
)

// updateGolden rewrites every fixture from the code under test. Fixtures are
// generated on the pre-refactor tree and must stay byte-identical afterwards.
var updateGolden = flag.Bool("update-golden", false, "rewrite golden fixtures")

// TestMain pins the color profile so ANSI output does not depend on whether
// the test process happens to have a TTY attached.
func TestMain(m *testing.M) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	os.Exit(m.Run())
}

func goldenModel(width, height int) Model {
	m := newTestModel()
	m, _ = update(m, tea.WindowSizeMsg{Width: width, Height: height})
	return m
}

func goldenMessages(m Model, messages ...Message) Model {
	m.messages = append([]Message(nil), messages...)
	return m
}

func assertGolden(t *testing.T, name, got string) {
	t.Helper()

	path := filepath.Join("testdata", "golden", name+".golden")

	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create golden dir: %v", err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden %s: %v", name, err)
		}
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (run with -update-golden)", name, err)
	}
	if string(want) != got {
		t.Fatalf(
			"golden %s changed\n--- want ---\n%s\n--- got ---\n%s",
			name,
			escapeANSI(string(want)),
			escapeANSI(got),
		)
	}
}

func escapeANSI(s string) string {
	return strings.ReplaceAll(s, "\x1b", "\\e")
}

var ansiSGR = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// plainText strips SGR styling so tests can assert on visible text while the
// package runs under a forced TrueColor profile.
func plainText(s string) string {
	return ansiSGR.ReplaceAllString(s, "")
}

func markdownSample() string {
	return strings.Join([]string{
		"# Heading with **bold**",
		"",
		"> a quote line with `code`",
		"> second quote line",
		"",
		"Paragraph with **bold**, *italic*, `inline code`, and a verylongwordthatmustbreakacrosscolumnsbecauseitislong.",
		"",
		"```go",
		"func main() {",
		"\tfmt.Println(\"hi\")",
		"}",
		"```",
		"",
		"| Name | Qty |",
		"| :--- | ---: |",
		"| apple | 3 |",
		"| pear | 12 |",
		"",
		"- list item one",
		"- list item two",
	}, "\n")
}

func tableSample() string {
	return strings.Join([]string{
		"| Name | Qty |",
		"| :--- | ---: |",
		"| apple | 3 |",
		"| pear | 12 |",
	}, "\n")
}

// ---------------------------------------------------------------------------
// Full View goldens
// ---------------------------------------------------------------------------

func TestGoldenViewFresh(t *testing.T) {
	m := goldenModel(80, 24)
	m.sessionUID = "20260101T000000Z-abcd1234"
	assertGolden(t, "view_fresh_80x24", m.View())
}

func TestGoldenViewMarkdown(t *testing.T) {
	m := goldenModel(80, 40)
	m.sessionUID = "20260101T000000Z-abcd1234"
	m = goldenMessages(m,
		Message{Role: roleUser, Content: "explain this code"},
		Message{Role: roleAgent, Content: markdownSample()},
	)
	assertGolden(t, "view_markdown_80x40", m.View())
}

func TestGoldenViewErrorAndEvent(t *testing.T) {
	m := goldenModel(60, 20)
	m = goldenMessages(m,
		Message{Role: roleError, Content: "provider returned a malformed body"},
		Message{Role: roleEvent, Content: "tool ran with a long line that will wrap because it keeps going past the width of the terminal"},
	)
	assertGolden(t, "view_error_event_60x20", m.View())
}

func TestGoldenViewEditWide(t *testing.T) {
	raw := harnessTools.FormatEditResult(
		"/work/a.go", "/work", "a.go",
		3, 4, 2, 10, 22,
		"old line\n", "new line\nsecond new line\n",
	)

	m := goldenModel(120, 40)
	m = goldenMessages(m, Message{Role: roleEvent, Content: raw})
	assertGolden(t, "view_edit_wide_120x40", m.View())
}

func TestGoldenViewEditStacked(t *testing.T) {
	raw := harnessTools.FormatEditResult(
		"/work/a.go", "/work", "a.go",
		3, 4, 2, 10, 22,
		"old line\n", "new line\nsecond new line\n",
	)

	m := goldenModel(60, 40)
	m = goldenMessages(m, Message{Role: roleEvent, Content: raw})
	assertGolden(t, "view_edit_stacked_60x40", m.View())
}

func TestGoldenViewWrite(t *testing.T) {
	raw := harnessTools.FormatWriteResult("/work/new.txt", "/work", "new.txt", "line one\nline two\n")

	m := goldenModel(80, 30)
	m = goldenMessages(m, Message{Role: roleEvent, Content: raw})
	assertGolden(t, "view_write_80x30", m.View())
}

func TestGoldenViewApproval(t *testing.T) {
	m := goldenModel(80, 24)
	m = goldenMessages(m, Message{Role: roleUser, Content: "run the tests"})
	m.busy = true
	m.pendingApproval = true
	assertGolden(t, "view_approval_80x24", m.View())
}

func TestGoldenViewApprovalHover(t *testing.T) {
	m := goldenModel(80, 24)
	m = goldenMessages(m, Message{Role: roleUser, Content: "run the tests"})
	m.busy = true
	m.pendingApproval = true
	m.hovered = "a"
	assertGolden(t, "view_approval_hover_80x24", m.View())
}

func TestGoldenViewBusySpinner(t *testing.T) {
	m := goldenModel(80, 24)
	m = goldenMessages(m, Message{Role: roleUser, Content: "thinking about it"})
	m.busy = true
	m.spinnerFrame = 3
	assertGolden(t, "view_busy_spinner_80x24", m.View())
}

func TestGoldenViewScrolled(t *testing.T) {
	m := goldenModel(60, 12)

	for i := 0; i < 12; i++ {
		m.messages = append(m.messages, Message{Role: roleUser, Content: "message " + string(rune('a'+i))})
	}

	m.scroll = 9
	assertGolden(t, "view_scrolled_60x12", m.View())
}

func TestGoldenViewNarrow(t *testing.T) {
	for _, tc := range []struct {
		name  string
		width int
		texth int
	}{
		{"view_narrow_20x10", 20, 10},
		{"view_narrow_3x3", 3, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := goldenModel(tc.width, tc.texth)
			m = goldenMessages(m,
				Message{Role: roleUser, Content: "**not markdown** in user text"},
				Message{Role: roleAgent, Content: "# Heading\n\nA paragraph that wraps."},
			)
			assertGolden(t, tc.name, m.View())
		})
	}
}

func TestGoldenViewHeightZero(t *testing.T) {
	m := goldenModel(80, 0)
	m = goldenMessages(m, Message{Role: roleUser, Content: "hello"})
	assertGolden(t, "view_height0_80", m.View())
}

func TestGoldenViewLongInput(t *testing.T) {
	m := goldenModel(10, 10)
	m.input = "0123456789abcdef"
	m.cursor = 12
	assertGolden(t, "view_long_input_10x10", m.View())
}

func TestGoldenViewResumeSwap(t *testing.T) {
	m := New(
		t.Context(),
		"Ollama",
		"test-model",
		"/work",
		[]Message{{Role: roleUser, Content: "old"}},
		nil,
		nil,
		nil,
	)
	m.clicks = &clickTargets{}
	m, _ = update(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m, _ = update(m, agentFinishedMsg{swap: []Message{
		{Role: roleUser, Content: "resumed question"},
		{Role: roleAgent, Content: "resumed answer"},
	}})

	assertGolden(t, "view_resume_swap_80x24", m.View())
}

// ---------------------------------------------------------------------------
// Renderer-level goldens
// ---------------------------------------------------------------------------

func TestGoldenRenderers(t *testing.T) {
	t.Run("agent_markdown_60", func(t *testing.T) {
		assertGolden(t, "render_agent_markdown_60", renderAgentContent(markdownSample(), 60))
	})
	t.Run("agent_markdown_100", func(t *testing.T) {
		assertGolden(t, "render_agent_markdown_100", renderAgentContent(markdownSample(), 100))
	})
	t.Run("plain_user_60", func(t *testing.T) {
		assertGolden(t, "render_plain_user_60", renderPlainWrapped("hello **not bold** world", 60, userStyle, "> ", "  "))
	})
	t.Run("plain_error_60", func(t *testing.T) {
		assertGolden(t, "render_plain_error_60", renderPlainWrapped("error: something broke", 60, errorStyle, "", ""))
	})
	t.Run("edit_wide_120", func(t *testing.T) {
		info, ok := parseEditInfo(harnessTools.FormatEditResult(
			"/work/a.go", "/work", "a.go", 3, 4, 2, 10, 22,
			"old line\n", "new line\nsecond new line\n",
		))
		if !ok {
			t.Fatal("edit block did not parse")
		}
		assertGolden(t, "render_edit_wide_120", renderEditInfo(info, 120))
	})
	t.Run("edit_stacked_60", func(t *testing.T) {
		info, ok := parseEditInfo(harnessTools.FormatEditResult(
			"/work/a.go", "/work", "a.go", 3, 4, 2, 10, 22,
			"old line\n", "new line\nsecond new line\n",
		))
		if !ok {
			t.Fatal("edit block did not parse")
		}
		assertGolden(t, "render_edit_stacked_60", renderEditInfo(info, 60))
	})
	t.Run("write_80", func(t *testing.T) {
		info, ok := parseWriteInfo(harnessTools.FormatWriteResult("/work/new.txt", "/work", "new.txt", "line one\nline two\n"))
		if !ok {
			t.Fatal("write block did not parse")
		}
		assertGolden(t, "render_write_80", renderWriteInfo(info, 80))
	})
	t.Run("title_80", func(t *testing.T) {
		assertGolden(t, "render_title_80", centeredTitle(80))
	})
	t.Run("meta_80", func(t *testing.T) {
		assertGolden(t, "render_meta_80", centeredMeta(80, "Ollama", "test-model", "SESS-1", "/work"))
	})
	t.Run("table_60", func(t *testing.T) {
		out, _, ok := tryTableBlock(strings.Split(tableSample(), "\n"), 0, 60)
		if !ok {
			t.Fatal("table block did not render")
		}
		assertGolden(t, "render_table_60", out)
	})
}

// ---------------------------------------------------------------------------
// Behavioral goldens for the approval hitbox
// ---------------------------------------------------------------------------

// approvalButtonRow returns the 0-based screen row of the first rendered
// approval button, or -1 when it is not on screen.
func approvalButtonRow(screen string) int {
	for i, line := range strings.Split(screen, "\n") {
		if strings.Contains(line, "[y] once") {
			return i
		}
	}
	return -1
}

func TestApprovalHitboxMatchesRenderedRow(t *testing.T) {
	m := goldenModel(80, 24)
	m = goldenMessages(m, Message{Role: roleUser, Content: "run the tests"})
	m.busy = true
	m.pendingApproval = true
	m.clicks = &clickTargets{}

	screen := m.View()
	row := approvalButtonRow(screen)
	if row < 0 {
		t.Fatal("approval buttons not rendered")
	}

	if !m.clicks.visible {
		t.Fatal("approval buttons should be visible")
	}
	if m.clicks.row != row {
		t.Fatalf("approval hitbox row=%d, rendered row=%d", m.clicks.row, row)
	}
	if len(m.clicks.opts) == 0 {
		t.Fatal("approval options were not recorded")
	}
	if got := optionAt(m.clicks, m.clicks.opts[0].start, row); got != "y" {
		t.Fatalf("click at the rendered button row = %q, want y", got)
	}
}

func TestGoldenTruncateHelpers(t *testing.T) {
	assertGolden(t, "truncate_cut", cutRunes("héllo world", 7))
	assertGolden(t, "truncate_ellipsis", truncateRunes("héllo world", 7))
}
