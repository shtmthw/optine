package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func benchmarkModel(messages int) Model {
	m := newTestModelAt(100, 40)
	body := sampleMarkdown()

	m.messages = make([]Message, 0, messages)
	for i := 0; i < messages; i++ {
		role := roleAgent
		if i%2 == 0 {
			role = roleUser
		}
		m.messages = append(m.messages, Message{
			Role:    role,
			Content: fmt.Sprintf("message %d\n\n%s", i, body),
		})
	}
	m.clicks = &clickTargets{}

	return m
}

func BenchmarkViewModelTyping(b *testing.B) {
	for _, messages := range []int{10, 200} {
		b.Run(fmt.Sprintf("%d_messages", messages), func(b *testing.B) {
			m := benchmarkModel(messages)
			m, _ = update(m, tea.WindowSizeMsg{Width: 100, Height: 40})
			_ = m.View() // warm the render cache, like the first frame
			b.ReportAllocs()
			b.ResetTimer()

			var view string

			for i := 0; i < b.N; i++ {
				m.input = strings.Repeat("x", i%80)
				m.cursor = len([]rune(m.input))
				view = m.View()
			}

			_ = view
		})
	}
}

func BenchmarkViewBusyTick(b *testing.B) {
	m := benchmarkModel(200)
	m.busy = true
	_ = m.View()
	b.ReportAllocs()
	b.ResetTimer()

	var view string

	for i := 0; i < b.N; i++ {
		m.spinnerFrame = i % len(spinnerFrames)
		view = m.View()
	}

	_ = view
}

func BenchmarkViewApprovalHover(b *testing.B) {
	m := benchmarkModel(200)
	m.busy = true
	m.pendingApproval = true
	_ = m.View()
	b.ReportAllocs()
	b.ResetTimer()

	var view string

	for i := 0; i < b.N; i++ {
		m.hovered = []string{"", "y", "a", "n"}[i%4]
		view = m.View()
	}

	_ = view
}
