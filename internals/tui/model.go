package tui

import (
	"context"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// AgentFunc is the interface between the TUI and the agent.
// The TUI does not care how the agent works internally.
type AgentFunc func(
	ctx context.Context,
	provider string,
	modelName string,
	userMessage string,
) (string, error)

type Message struct {
	Role    string
	Content string
}

type Model struct {
	provider  string
	modelName string
	agent     AgentFunc

	input    string
	messages []Message
	busy     bool
}

type agentFinishedMsg struct {
	response string
	err      error
}

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true)

	userStyle = lipgloss.NewStyle().
			Bold(true)

	agentStyle = lipgloss.NewStyle()

	errorStyle = lipgloss.NewStyle().
			Bold(true)

	statusStyle = lipgloss.NewStyle().
			Italic(true)
)

func New(
	provider string,
	modelName string,
	agent AgentFunc,
) Model {
	return Model{
		provider:  provider,
		modelName: modelName,
		agent:     agent,
		messages:  make([]Message, 0),
	}
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.KeyMsg:
		switch msg.String() {

		case "ctrl+c":
			return m, tea.Quit

		case "enter":
			if m.busy {
				return m, nil
			}

			userMessage := strings.TrimSpace(m.input)

			if userMessage == "" {
				return m, nil
			}

			m.messages = append(m.messages, Message{
				Role:    "user",
				Content: userMessage,
			})

			m.input = ""
			m.busy = true

			return m, runAgent(
				m.agent,
				m.provider,
				m.modelName,
				userMessage,
			)

		case "backspace":
			runes := []rune(m.input)

			if len(runes) == 0 {
				return m, nil
			}

			m.input = string(runes[:len(runes)-1])

		default:
			if len(msg.Runes) > 0 {
				m.input += string(msg.Runes)
			}
		}

	case agentFinishedMsg:
		m.busy = false

		if msg.err != nil {
			m.messages = append(m.messages, Message{
				Role:    "error",
				Content: msg.err.Error(),
			})

			return m, nil
		}

		m.messages = append(m.messages, Message{
			Role:    "agent",
			Content: msg.response,
		})
	}

	return m, nil
}

func (m Model) View() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("optine"))
	b.WriteString("\n\n")

	for _, message := range m.messages {
		switch message.Role {

		case "user":
			b.WriteString(
				userStyle.Render("> " + message.Content),
			)

		case "agent":
			b.WriteString(
				agentStyle.Render(message.Content),
			)

		case "error":
			b.WriteString(
				errorStyle.Render("error: " + message.Content),
			)
		}

		b.WriteString("\n\n")
	}

	if m.busy {
		b.WriteString(
			statusStyle.Render("thinking..."),
		)
		b.WriteString("\n\n")
	}

	b.WriteString("> ")
	b.WriteString(m.input)
	b.WriteString("█")

	return b.String()
}

func runAgent(
	agent AgentFunc,
	provider string,
	modelName string,
	userMessage string,
) tea.Cmd {
	return func() tea.Msg {
		response, err := agent(
			context.Background(),
			provider,
			modelName,
			userMessage,
		)

		return agentFinishedMsg{
			response: response,
			err:      err,
		}
	}
}
