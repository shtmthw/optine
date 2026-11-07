package tui

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Every tool call shows its name in bold grey. bash, read_file and web_search
// add a colored block: the call is the block title and its result the body.

var (
	toolNameStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("245"))

	bashTitleStyle = editTitleBase.Background(lipgloss.Color("237"))
	bashBodyStyle  = editBodyBase.Background(lipgloss.Color("237"))

	webTitleStyle = editTitleBase.Foreground(lipgloss.Color("11")).Background(lipgloss.Color("58"))
	webBodyStyle  = editBodyBase.Background(lipgloss.Color("58"))
)

type toolCall struct {
	name string
	args map[string]any
}

// parseToolCall reads the canonical "tool call: <name> <json>" line.
func parseToolCall(content string) (toolCall, bool) {
	rest, ok := strings.CutPrefix(content, "tool call: ")
	if !ok {
		return toolCall{}, false
	}
	name, raw, _ := strings.Cut(rest, " ")

	call := toolCall{name: name}
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &call.args)
	}
	return call, true
}

func (c toolCall) colored() bool {
	switch c.name {
	case "bash", "read_file", "web_search":
		return true
	}
	return false
}

// hidesResult is true for web_search: only its query is shown.
func (c toolCall) hidesResult() bool {
	return c.name == "web_search"
}

func (c toolCall) styles() (title, body lipgloss.Style) {
	switch c.name {
	case "bash":
		return bashTitleStyle, bashBodyStyle
	case "web_search":
		return webTitleStyle, webBodyStyle
	default:
		return editNewTitleStyle, editNewStyle
	}
}

func (c toolCall) stringArg(key string) string {
	s, _ := c.args[key].(string)
	return strings.TrimSpace(s)
}

func (c toolCall) intArg(key string) int {
	switch v := c.args[key].(type) {
	case float64:
		return int(v)
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(v))
		return n
	}
	return 0
}

func (c toolCall) title() string {
	switch c.name {
	case "bash":
		return strings.TrimSpace("$ " + c.stringArg("command"))

	case "web_search":
		title := c.stringArg("query")
		if title == "" {
			title = "(no query)"
		}
		if crawler := c.stringArg("crawler"); crawler != "" {
			title += "  via " + crawler
		}
		return title

	default:
		title := c.stringArg("path")
		if title == "" {
			title = "(no path)"
		}
		start, end := c.intArg("start_line"), c.intArg("end_line")
		switch {
		case start > 0 && end > 0:
			title += "  lines " + strconv.Itoa(start) + "-" + strconv.Itoa(end)
		case start > 1:
			title += "  from line " + strconv.Itoa(start)
		}
		return title
	}
}

func renderToolTitle(call toolCall, width int) string {
	style, _ := call.styles()
	return renderPanel(call.title(), width, style)
}

func renderToolBody(call toolCall, result string, width int) string {
	_, style := call.styles()
	if call.name == "read_file" {
		return renderPanel(strings.Join(readRows(call, result, width), "\n"), width, style)
	}
	return renderPanel(strings.ReplaceAll(result, "\t", "  "), width, style)
}

// readRows numbers file content from start_line like the write view. Notices
// and failures are shown as-is.
func readRows(call toolCall, result string, width int) []string {
	if result == "(no output)" ||
		strings.HasPrefix(result, "[read_file:") ||
		strings.HasPrefix(result, "Tool execution failed:") {
		return []string{result}
	}

	lines := strings.Split(result, "\n")
	note := ""
	if last := lines[len(lines)-1]; strings.HasPrefix(last, "[read_file:") {
		note = last
		lines = lines[:len(lines)-1]
		for len(lines) > 0 && lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
	}

	start := max(call.intArg("start_line"), 1)
	contentW := 0
	if width > 0 {
		contentW = width - 2
	}
	rows := panelRows(lines, start, len(strconv.Itoa(start+len(lines)-1)), contentW, "(empty file)")
	if note != "" {
		rows = append(rows, note)
	}
	return rows
}

func renderToolName(name string, width int) string {
	if width > 0 {
		name = truncateRunes(name, width)
	}
	return toolNameStyle.Render(name)
}

// renderHistoryMessage renders one history message. open is the colored call
// still waiting for its result; the returned value replaces it. header is
// true for a tool call, which a directly following result attaches to. An
// empty rendered string means the message is not shown.
func (m Model) renderHistoryMessage(message Message, open *toolCall) (rendered string, next *toolCall, header bool) {
	switch message.Role {
	case roleToolCall:
		call, ok := parseToolCall(message.Content)
		if !ok {
			return m.renderMessage(message), nil, false
		}
		name := renderToolName(call.name, m.width)
		if !call.colored() {
			return name, nil, true
		}
		return name + "\n" + renderToolTitle(call, m.width), &call, !call.hidesResult()

	case roleToolResult:
		if open != nil && open.hidesResult() {
			return "", nil, false
		}
		if open != nil {
			return renderToolBody(*open, message.Content, m.width), nil, false
		}
	}
	return m.renderMessage(message), open, false
}
