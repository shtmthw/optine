package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// viewMatchesFreshRebuild forces the cache to rebuild and verifies that the
// incremental frame is identical to a cold render of the same state.
func viewMatchesFreshRebuild(t *testing.T, m Model) {
	t.Helper()

	got := m.View()
	m.cache.dirty = true

	if want := m.View(); got != want {
		t.Fatalf("cached view differs from a fresh rebuild\n--- cached ---\n%s\n--- fresh ---\n%s",
			escapeANSI(got), escapeANSI(want))
	}
}

func TestRenderCacheMatchesFreshRebuild(t *testing.T) {
	cases := map[string]func() Model{
		"fresh": func() Model {
			m := newTestModelAt(80, 24)
			m.sessionUID = "SESS-1"
			return m
		},
		"messages": func() Model {
			m := newTestModelAt(80, 40)
			return modelWithMessages(m,
				Message{Role: roleUser, Content: "hello"},
				Message{Role: roleAgent, Content: sampleMarkdown()},
			)
		},
		"busy": func() Model {
			m := newTestModelAt(80, 24)
			m = modelWithMessages(m, Message{Role: roleUser, Content: "hello"})
			m.busy = true
			m.spinnerFrame = 4
			return m
		},
		"approval": func() Model {
			m := newTestModelAt(80, 24)
			m = modelWithMessages(m, Message{Role: roleUser, Content: "hello"})
			m.busy = true
			m.pendingApproval = true
			m.hovered = "a"
			return m
		},
		"scrolled": func() Model {
			m := newTestModelAt(40, 10)
			for i := 0; i < 20; i++ {
				m.messages = append(m.messages, Message{Role: roleUser, Content: "line"})
			}
			m.scroll = 7
			return m
		},
		"narrow": func() Model {
			m := newTestModelAt(3, 3)
			return modelWithMessages(m, Message{Role: roleAgent, Content: "# h\n\nbody"})
		},
	}

	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			viewMatchesFreshRebuild(t, build())
		})
	}
}

func TestRenderCacheInvalidatesOnWidthChange(t *testing.T) {
	m := newTestModelAt(80, 24)
	m = modelWithMessages(m, Message{Role: roleAgent, Content: sampleMarkdown()})

	_ = m.View()
	m, _ = update(m, tea.WindowSizeMsg{Width: 40, Height: 24})
	got := m.View()

	fresh := newTestModelAt(40, 24)
	fresh = modelWithMessages(fresh, Message{Role: roleAgent, Content: sampleMarkdown()})
	if want := fresh.View(); got != want {
		t.Fatalf("width change did not re-render\n--- got ---\n%s\n--- want ---\n%s",
			escapeANSI(got), escapeANSI(want))
	}
}

func TestRenderCacheInvalidatesOnAppend(t *testing.T) {
	m := newTestModelAt(80, 24)
	m = modelWithMessages(m, Message{Role: roleUser, Content: "first"})
	_ = m.View()

	m, _ = update(m, logLineMsg{text: "second"})
	got := m.View()

	fresh := newTestModelAt(80, 24)
	fresh = modelWithMessages(fresh,
		Message{Role: roleUser, Content: "first"},
		Message{Role: roleEvent, Content: "second"},
	)
	if want := fresh.View(); got != want {
		t.Fatalf("append did not re-render\n--- got ---\n%s\n--- want ---\n%s",
			escapeANSI(got), escapeANSI(want))
	}
}

func TestRenderCacheInvalidatesOnSwapWithSameCount(t *testing.T) {
	m := newTestModelAt(80, 24)
	m = modelWithMessages(m, Message{Role: roleUser, Content: "old conversation"})
	_ = m.View()

	m, _ = update(m, agentFinishedMsg{swap: []Message{
		{Role: roleUser, Content: "new conversation"},
	}})
	got := m.View()

	if strings.Contains(plainText(got), "old conversation") {
		t.Fatalf("swap with the same message count kept stale content:\n%s", escapeANSI(got))
	}
	if !strings.Contains(plainText(got), "new conversation") {
		t.Fatalf("swapped conversation missing:\n%s", escapeANSI(got))
	}
}

func TestRenderCacheInvalidatesOnSessionChange(t *testing.T) {
	m := newTestModelAt(80, 24)
	m.sessionUID = "OLD"
	_ = m.View()

	m, _ = update(m, sessionChangedMsg{uid: "NEW"})
	got := m.View()

	if !strings.Contains(plainText(got), "NEW") {
		t.Fatalf("new session UID missing:\n%s", escapeANSI(got))
	}
	if strings.Contains(plainText(got), "OLD") {
		t.Fatalf("stale session UID still rendered:\n%s", escapeANSI(got))
	}
}

func TestMouseClickUsesRenderedApprovalRow(t *testing.T) {
	m := newTestModelAt(80, 24)
	m = modelWithMessages(m, Message{Role: roleUser, Content: "run the tests"})
	m.busy = true
	m.pendingApproval = true
	m.clicks = &clickTargets{}

	ch := make(chan string, 1)
	m.approvalCh = ch

	screen := m.View()
	row := approvalButtonRow(screen)
	if row < 0 || len(m.clicks.opts) == 0 {
		t.Fatal("approval buttons not rendered")
	}

	_, cmd := update(m, tea.MouseMsg{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
		X:      m.clicks.opts[0].start,
		Y:      row,
	})
	if cmd == nil {
		t.Fatal("clicking the rendered button should answer the approval")
	}
	cmd()

	select {
	case got := <-ch:
		if got != "y" {
			t.Fatalf("clicked answer = %q, want y", got)
		}
	case <-time.After(time.Second):
		t.Fatal("approval answer was never sent")
	}
}

func TestRenderCacheDoNotAccumulateDynamicLines(t *testing.T) {
	m := newTestModelAt(80, 24)
	m = modelWithMessages(m, Message{Role: roleUser, Content: "hello"})
	m.busy = true
	m.spinnerFrame = 2

	first := m.View()
	second := m.View()
	if first != second {
		t.Fatalf("repeated busy frames differ\n--- first ---\n%s\n--- second ---\n%s",
			escapeANSI(first), escapeANSI(second))
	}

	m.busy = false
	withoutSpinner := m.View()

	fresh := newTestModelAt(80, 24)
	fresh = modelWithMessages(fresh, Message{Role: roleUser, Content: "hello"})
	if want := fresh.View(); withoutSpinner != want {
		t.Fatalf("spinner leaked into the idle frame\n--- got ---\n%s\n--- want ---\n%s",
			escapeANSI(withoutSpinner), escapeANSI(want))
	}
}
