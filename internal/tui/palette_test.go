package tui

import (
	"bufio"
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattthew/optine/internal/slashCommands"
)

// paletteModel is a sized chat model with the local-session commands and an
// agent that records every message it is sent.
func paletteModel(t *testing.T) (Model, *[]string) {
	t.Helper()

	var sent []string
	agent := func(_ context.Context, _, _, msg string, _ *bufio.Reader) (AgentResult, error) {
		sent = append(sent, msg)
		return AgentResult{}, nil
	}

	m := New(context.Background(), "Ollama", "m", "/w", nil, agent, bufio.NewReader(strings.NewReader("")), make(chan string, 1))
	m.palette.commands = slashCommands.For(slashCommands.LocalSession)
	m, _ = update(m, tea.WindowSizeMsg{Width: 80, Height: 30})

	return m, &sent
}

// runBatch executes the agent half of a submit batch.
func runBatch(t *testing.T, cmd tea.Cmd) {
	t.Helper()

	if cmd == nil {
		t.Fatal("expected a command")
	}

	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatal("expected a batch")
	}

	// The first command is the agent run; the second is the spinner tick.
	batch[0]()
}

func key(k tea.KeyType) tea.KeyMsg { return tea.KeyMsg{Type: k} }

// Typing "/" opens the palette directly above the prompt, which stays on the
// last row.
func TestPaletteOpensAbovePrompt(t *testing.T) {
	m, _ := paletteModel(t)

	if m.paletteShown() {
		t.Fatal("palette should be closed on an empty prompt")
	}

	m = typeKey(m, "/")
	if !m.paletteShown() {
		t.Fatal("typing / should open the palette")
	}

	lines := strings.Split(plainText(m.View()), "\n")
	if len(lines) != 30 {
		t.Fatalf("view has %d rows, want the full 30", len(lines))
	}
	if !strings.HasPrefix(lines[len(lines)-1], "> /") {
		t.Fatalf("prompt is not on the last row: %q", lines[len(lines)-1])
	}
	if !strings.Contains(lines[len(lines)-2], paletteHint) {
		t.Fatalf("palette hint is not right above the prompt: %q", lines[len(lines)-2])
	}
	if !strings.Contains(strings.Join(lines, "\n"), "/revert") {
		t.Fatal("palette rows are not rendered")
	}

	if !m.clicks.paletteVisible || m.clicks.paletteRows == 0 {
		t.Fatal("palette hitbox was not recorded")
	}
	if row := lines[m.clicks.paletteFirstRow]; !strings.Contains(row, "/model") {
		t.Fatalf("recorded first palette row %d is %q, want /model", m.clicks.paletteFirstRow, row)
	}
}

// Arrow keys move the highlight and Enter runs the highlighted command.
func TestPaletteEnterRunsHighlighted(t *testing.T) {
	m, sent := paletteModel(t)

	m = typeKey(m, "/re")
	m, _ = update(m, key(tea.KeyDown))
	m, cmd := update(m, key(tea.KeyEnter))
	runBatch(t, cmd)

	if len(*sent) != 1 || (*sent)[0] != "/revert" {
		t.Fatalf("agent got %v, want [/revert]", *sent)
	}
	if len(m.messages) != 0 {
		t.Fatalf("a command must not be echoed as a user message: %+v", m.messages)
	}
}

// Tab completes the highlighted command without running it.
func TestPaletteTabCompletes(t *testing.T) {
	m, sent := paletteModel(t)

	m = typeKey(m, "/mo")
	m, cmd := update(m, key(tea.KeyTab))

	if m.input != "/model" || cmd != nil || len(*sent) != 0 {
		t.Fatalf("tab should complete to /model without running it: input=%q sent=%v", m.input, *sent)
	}
}

// Esc closes the palette but keeps the input; the next edit reopens it.
func TestPaletteEscHidesUntilEdit(t *testing.T) {
	m, _ := paletteModel(t)

	m = typeKey(m, "/re")
	m, _ = update(m, key(tea.KeyEsc))
	if m.paletteShown() || m.input != "/re" {
		t.Fatalf("esc should close the palette and keep the input: shown=%v input=%q", m.paletteShown(), m.input)
	}

	m = typeKey(m, "s")
	if !m.paletteShown() {
		t.Fatal("editing the input should reopen the palette")
	}
}

// The palette stays out of the way while a run is in flight.
func TestPaletteHiddenWhileBusy(t *testing.T) {
	m, _ := paletteModel(t)

	m = typeKey(m, "/")
	m.busy = true
	if m.paletteShown() {
		t.Fatal("palette should be hidden while the agent is running")
	}
}

// A mouse click on a palette row runs that command.
func TestPaletteClickRunsRow(t *testing.T) {
	m, sent := paletteModel(t)

	m = typeKey(m, "/")
	_ = m.View()

	row := m.clicks.paletteFirstRow + 2 // third suggestion: /resume
	m, cmd := update(m, tea.MouseMsg{X: 4, Y: row, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	runBatch(t, cmd)

	if len(*sent) != 1 || (*sent)[0] != "/resume" {
		t.Fatalf("agent got %v, want [/resume]", *sent)
	}
}

// /help and /quit are answered by the interface without starting a run.
func TestHelpAndQuitAreLocal(t *testing.T) {
	m, sent := paletteModel(t)

	m = typeKey(m, "/help")
	m, cmd := update(m, key(tea.KeyEnter))
	if cmd != nil || m.busy || len(*sent) != 0 {
		t.Fatal("/help must not start an agent run")
	}
	if len(m.messages) != 1 || m.messages[0].Role != roleHelp {
		t.Fatalf("/help should add one help message, got %+v", m.messages)
	}
	if view := plainText(m.View()); !strings.Contains(view, "/revert") || !strings.Contains(view, "/help") {
		t.Fatalf("help list not rendered:\n%s", view)
	}

	m = typeKey(m, "/q")
	m, cmd = update(m, key(tea.KeyEnter))
	if !m.quitRequested || cmd == nil {
		t.Fatal("/q should request quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("/q should return tea.Quit")
	}
	if len(*sent) != 0 {
		t.Fatal("/quit must not reach the agent")
	}
}

// The launcher prompt submits the highlighted command and refuses chat text.
func TestCommandPromptSubmit(t *testing.T) {
	m := commandPromptModel{palette: commandPalette{commands: slashCommands.For(slashCommands.Launcher)}, width: 80}

	step := func(m commandPromptModel, msg tea.Msg) commandPromptModel {
		next, _ := m.Update(msg)
		return next.(commandPromptModel)
	}

	for _, r := range "hello" {
		m = step(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m = step(m, key(tea.KeyEnter))
	if m.done || m.notice == "" {
		t.Fatal("chat text at the launcher should show a notice, not submit")
	}

	m = step(m, key(tea.KeyCtrlU))
	for _, r := range "/re" {
		m = step(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if m.notice != "" {
		t.Fatal("editing should clear the notice")
	}
	m = step(m, key(tea.KeyDown))
	m = step(m, key(tea.KeyEnter))
	if !m.done || m.input != "/resume" {
		t.Fatalf("enter should submit the highlighted /resume, got done=%v input=%q", m.done, m.input)
	}
	if view := plainText(m.View()); !strings.Contains(view, "/resume") || strings.Contains(view, paletteHint) {
		t.Fatalf("submitted prompt should leave a one-line record:\n%s", view)
	}
}

// The launcher list filters as you type and returns the chosen option.
func TestSelectModelFilterAndPick(t *testing.T) {
	m := newSelectModel("Model", []string{"llama3:8b", "qwen2.5-coder:7b", "mistral:7b"})

	step := func(m selectModel, msg tea.Msg) selectModel {
		next, _ := m.Update(msg)
		return next.(selectModel)
	}

	for _, r := range "7b" {
		m = step(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if len(m.choices) != 2 {
		t.Fatalf("filter 7b kept %v", m.choices)
	}

	m = step(m, key(tea.KeyDown))
	m = step(m, key(tea.KeyEnter))
	if !m.done || m.selected != "mistral:7b" {
		t.Fatalf("selected %q (done=%v), want mistral:7b", m.selected, m.done)
	}
}

// API keys never appear on screen.
func TestSecretModelMasksInput(t *testing.T) {
	m := secretModel{label: "OpenRouter API key"}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("sk-or-secret")})
	m = next.(secretModel)

	if strings.Contains(m.View(), "secret") {
		t.Fatal("the key is visible while typing")
	}

	next, _ = m.Update(key(tea.KeyEnter))
	m = next.(secretModel)
	if !m.done || string(m.value) != "sk-or-secret" || strings.Contains(m.View(), "secret") {
		t.Fatal("the key should be kept but not shown after enter")
	}
}

// Every logo row has the same width, so the launcher banner never shears.
func TestLogoRowsAligned(t *testing.T) {
	for i, row := range logoRows {
		if w := runeLen(row); w != logoWidth {
			t.Fatalf("logo row %d is %d wide, want %d", i, w, logoWidth)
		}
	}

	if lines := logoLines(logoWidth - 1); len(lines) != 1 || !strings.Contains(plainText(lines[0]), "optine") {
		t.Fatalf("a narrow terminal should get the wordmark, got %q", lines)
	}
}

// headerParts splits a header row into its title (which must start one cell
// in from the left), its center text and its right text, and reports the
// display column where the center text starts.
func headerParts(t *testing.T, line string) (title, center string, centerCol int, right string) {
	t.Helper()

	plain := plainText(line)
	for _, tt := range []string{titleSpaced, titleSmall} {
		if strings.HasPrefix(plain, strings.Repeat(" ", headerMargin)+tt) {
			title = tt
			break
		}
	}
	if title == "" {
		t.Fatalf("header row does not start with the title one cell in: %q", plain)
	}

	rest := plain[headerMargin+len(title):]
	fields := strings.Split(strings.TrimSpace(rest), "  ")
	var parts []string
	for _, f := range fields {
		if f = strings.TrimSpace(f); f != "" {
			parts = append(parts, f)
		}
	}

	switch len(parts) {
	case 2:
		center, right = parts[0], parts[1]
	case 1:
		right = parts[0]
		return title, "", -1, right
	default:
		t.Fatalf("unexpected header parts %q in %q", parts, plain)
	}

	centerCol = lipgloss.Width(plain[:strings.Index(plain, center)])

	return title, center, centerCol, right
}

// The header row has the title at the top left, provider, model and session
// exactly centered, and the directory at the top right. On narrower screens
// the center and directory shrink first, then the title.
func TestHeaderBarLayout(t *testing.T) {
	oldHome := userHome
	userHome = "/home/u"
	t.Cleanup(func() { userHome = oldHome })

	const (
		provider = "Ollama"
		model    = "qwen2.5-coder:7b"
		uid      = "20260101T000000Z-abcd1234"
		dir      = "/home/u/projects/optine"
	)

	cases := []struct {
		width                int
		title, center, right string
	}{
		{200, titleSpaced, "Ollama · qwen2.5-coder:7b · sess 20260101T000000Z-abcd1234", "Directory: /home/u/projects/optine"},
		{110, titleSpaced, "Ollama · qwen2.5-coder:7b · sess 20260101T000000Z-abcd1234", "~/projects/optine"},
		{80, titleSpaced, "Ollama · qwen2.5-coder:7b · sess abcd1234", "optine"},
		{60, titleSpaced, "Ollama · qwen2.5-coder:7b", "optine"},
		{50, titleSpaced, "qwen2.5-coder:7b", "optine"},
		{40, titleSmall, "qwen2.5-coder…", "optine"},
		{30, titleSmall, "qwe…", "optine"},
	}

	for _, tc := range cases {
		line := headerBar(tc.width, provider, model, uid, dir)
		if w := lipgloss.Width(line); w > tc.width {
			t.Fatalf("width %d: row is %d cells wide", tc.width, w)
		}

		title, center, col, right := headerParts(t, line)
		if title != tc.title || center != tc.center || right != tc.right {
			t.Fatalf("width %d: got title=%q center=%q right=%q\nrow: %q", tc.width, title, center, right, plainText(line))
		}
		if want := (tc.width - lipgloss.Width(center)) / 2; col != want {
			t.Fatalf("width %d: center starts at %d, want %d", tc.width, col, want)
		}
	}

	// Tiny screens keep just the title.
	if got := plainText(headerBar(5, provider, model, uid, dir)); got != "Optin" {
		t.Fatalf("tiny row = %q", got)
	}

	// The title is purple 99; the info is the light grey.
	if !strings.Contains(titleStyle.Render("x"), "38;5;99") && !strings.Contains(titleStyle.Render("x"), "135;95;255") {
		t.Fatalf("title should be purple 99: %q", titleStyle.Render("x"))
	}
}

// The empty prompt shows the key hints; typing or a pending approval hides
// them.
func TestPromptPlaceholder(t *testing.T) {
	m := newTestModelAt(80, 24)

	if got := plainText(m.renderPrompt()); got != "> "+cursorGlyph+promptPlaceholder {
		t.Fatalf("empty prompt = %q", got)
	}

	if got := plainText(typeKey(m, "hi").renderPrompt()); strings.Contains(got, "for commands") {
		t.Fatalf("placeholder should disappear once typing starts: %q", got)
	}

	m.pendingApproval = true
	if got := plainText(m.renderPrompt()); strings.Contains(got, "for commands") {
		t.Fatalf("placeholder should be hidden while an approval is pending: %q", got)
	}

	m.pendingApproval = false
	m.width = 20
	if got := plainText(m.renderPrompt()); runeLen(got) != 20 || !strings.HasSuffix(got, "…") {
		t.Fatalf("placeholder should be cut to the row width: %q", got)
	}
}

// /new and /resume announce the new session's UID with the model change;
// the header must pick it up. A change without a UID (/model) keeps it.
func TestModelChangedCarriesSessionUID(t *testing.T) {
	m := newTestModelAt(160, 24)

	m, _ = update(m, modelChangedMsg{provider: "Ollama", model: "m2", UID: "20261107T050044Z-e058c50d"})
	if m.sessionUID != "20261107T050044Z-e058c50d" || !strings.Contains(plainText(m.View()), "20261107T050044Z-e058c50d") {
		t.Fatalf("session UID not shown after a model change: %q", m.sessionUID)
	}

	m, _ = update(m, modelChangedMsg{provider: "Ollama", model: "m3"})
	if m.sessionUID != "20261107T050044Z-e058c50d" {
		t.Fatalf("a change without a UID must keep the current one, got %q", m.sessionUID)
	}
}

// The header row never runs past the screen at any width, and the title
// stays at the left edge whenever it fits.
func TestHeaderBarFitsEveryWidth(t *testing.T) {
	sides := [][4]string{
		{"Openrouter", "liquid/lfm-2.5-2.6b:free", "20261107T050044Z-e4bedc98", "/home/matthew/optine"},
		{"Ollama", "qwen2.5-coder:32b-instruct-q4_K_M", "20261107T050044Z-e4bedc98", "/home/matthew/projects/some/very/deep/workspace/directory"},
		{"vLLM", "m", "", "/"},
	}

	for _, sd := range sides {
		for width := 1; width <= 300; width++ {
			line := headerBar(width, sd[0], sd[1], sd[2], sd[3])
			if w := lipgloss.Width(line); w > width {
				t.Fatalf("width %d: header is %d cells: %q", width, w, plainText(line))
			}

			if width >= headerMargin+lipgloss.Width(titleSmall) {
				plain := plainText(line)
				pad := strings.Repeat(" ", headerMargin)
				if !strings.HasPrefix(plain, pad+titleSpaced) && !strings.HasPrefix(plain, pad+titleSmall) {
					t.Fatalf("width %d: title is not at the left edge: %q", width, plain)
				}
			}
		}
	}
}

// Lines a slash command prints are gathered into one grey box titled with
// the command; lines from a normal agent run stay plain events.
func TestCommandOutputIsBoxed(t *testing.T) {
	m, _ := paletteModel(t)

	m = typeKey(m, "/revert")
	m, _ = update(m, key(tea.KeyEnter))
	m, _ = update(m, logLineMsg{text: "Reverted: a.go"})
	m, _ = update(m, logLineMsg{text: "Reverted: b.go"})
	m, _ = update(m, agentFinishedMsg{})

	if len(m.messages) != 1 || m.messages[0].Role != roleCommand || m.messages[0].Content != "/revert\nReverted: a.go\nReverted: b.go" {
		t.Fatalf("command output = %+v", m.messages)
	}

	box := m.renderMessage(m.messages[0])
	if !strings.Contains(box, "48;5;237") && !strings.Contains(box, "58;58;58") {
		t.Fatalf("command output should have the grey background: %q", box)
	}
	for _, want := range []string{"/revert", "Reverted: a.go", "Reverted: b.go"} {
		if !strings.Contains(plainText(box), want) {
			t.Fatalf("box lacks %q:\n%s", want, plainText(box))
		}
	}

	// A normal message's log lines are not boxed.
	m = typeKey(m, "hello")
	m, _ = update(m, key(tea.KeyEnter))
	m, _ = update(m, logLineMsg{text: "session saved"})
	if last := m.messages[len(m.messages)-1]; last.Role != roleEvent {
		t.Fatalf("agent-run log line should stay an event, got %+v", last)
	}
}
