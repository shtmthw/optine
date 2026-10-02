# TUI changes

The agent interface now runs as a Bubble Tea TUI instead of the plain text
loop. The agent loop / harness internals are untouched; the TUI is bolted on
through the existing seams (the `*bufio.Reader` inputs and `log.Println`
output).

## What changed

- `internals/tui/model.go`
  - Title is now `Optine` (top of the view).
  - `busy` shows a `thinking...` indicator while the agent runs.
  - New message roles:
    - `event` — text that used to go to stdout via `log.Println` (provider
      replies per turn, `The agent wants to run: tool/args/description`,
      approval prompt, etc.) is rendered faint/italic in the TUI.
    - approval prompt state: when an `Allow?` line passes through, the TUI
      switches to an `approval required` mode.
  - Enter behaviour while busy:
    - if an approval is pending, the typed line is routed to the harness as
      the approval answer (`y` / `a` / `n`);
    - otherwise input is ignored while the agent thinks.
  - `AgentFunc` gained a `*bufio.Reader` argument so approvals typed in the
    TUI reach the harness.
  - Empty agent responses (e.g. a `/funfact` command that only logs) no
    longer append a blank message.

- `internals/tui/run.go` (new)
  - `tui.Run(provider, modelName, agent)` launches the Bubble Tea program
    with `tea.WithAltScreen()`, so the TUI owns the whole terminal screen
    while it runs and the previous screen is restored on exit.
  - Redirects the standard `log` package output into the TUI via
    `tuiLogSink` (a `tea.Cmd`-style `p.Send` of `logLineMsg`s), so the tool
    call requests shown today through `log.Println`/stdout appear as `event`
    messages. It also watches for the `Allow?` line to flag approval state.
  - `lineSource` adapts the TUI's approval answers channel into an
    `io.Reader`, wrapped in a `*bufio.Reader` shared with the harness, so
    `harnessPermissions.Ask` / `ReadFilePolicy` block on the TUI instead of
    stdin.
  - Restores the previous log output/flags when the program exits.

- `internals/cli/selection.go`
  - Instead of `agentInterface(reader, agentConf)`, both provider branches
    (`Ollama`, `vLLM`) now build an `AgentFunc` that:
      - routes `/commands` to `cli.RunCommand` (output lands in the TUI via
        the redirected log), and
      - calls `harnessCore.AgentLoop` unchanged otherwise,
    then starts `tui.Run(...)`.
  - Imports `context`, `harnessCore`, and `tui`.
  - The agent lambda's unused params are `_`-blanked instead of named.

## Interaction details

- The program renders in the terminal's alternate screen (full screen).
- Thinking shows a braille spinner: `⠋ thinking...`.
- Keybinds in the chat input:
  - `enter` — send message / answer approval prompt
  - `backspace`, `ctrl+h`, `delete` — delete one char
  - `ctrl+w` — delete last word, `ctrl+u` — clear the line
  - `ctrl+c` — quit
- While an approval prompt is pending, the options `[y] once / [a] always /
  [N] no` are shown with a purple background and are mouse-clickable
  (program runs with `tea.WithMouseCellMotion()`); type the letter or
  click the option.
- The chat scrolls with the mouse wheel: wheel up moves up through the
  history, wheel down returns toward the prompt, which is pinned at the
  bottom. Scrolling uses the terminal height (`WindowSizeMsg`) so the
  viewport stays inside the full-screen TUI.

## Extending it

- New message kinds: add a role to the `Message.Role` switch in `View()`
  and a style in the `var (...)` style block.
- Anything the harness prints via `log.Println`/`log.Printf` automatically
  appears as a faint `event` line — that's where tool call requests show up;
  change how they're formatted in the harness, or special-case a line in
  `tuiLogSink.Write` (e.g. like the `Allow?` detection) to give it its own
  role/style.
- New keybinds: add a case to the `tea.KeyMsg` switch in `Update()`.
- New clickable hotspots: mirror the `clickTargets` pattern (record spans
  while rendering in `View`, test them in the `tea.MouseMsg` case of
  `Update`).
- Agent behaviour: wrap/replace the `AgentFunc` you pass to `tui.Run` in
  `cli/selection.go`; the TUI never looks inside it.

## Cut as unneeded

- `internals/cli/interface.go` (old stdin loop) — deleted in the original
  change.
- Duplicate newline append on approval answers — `lineSource` already
  guarantees a trailing `\n`.
- Named-but-unused lambda parameters in `selection.go` — blanked with `_`.

- `internals/cli/interface.go`
  - Deleted. The old `agentInterface` stdin loop is replaced by the TUI;
    command dispatch lives in `cli.RunCommand` and provider selection is
    still the plain stdout flow that runs before the TUI starts.

## Centered title, bottom-locked prompt

- `internals/tui/model.go`
  - `Model` now stores the terminal `width` from `tea.WindowSizeMsg`.
  - Title is rendered with `Width(width).Align(Center)` (new
    `centeredTitle` helper), so it always stays in the middle at any
    resolution. The fixed `Padding(10, 50)` is gone; the hidden border's
    2 cells are accounted for so the block is exactly terminal-wide
    (truncated + centered when narrower than the text).
  - The prompt (`"> " + input` with the `█` cursor) is padded with blank
    rows so it lands on the last screen row, and long input is windowed
    around the cursor (hard-truncated to the width as a backstop), so `>`
    stays bottom-left at any resolution.
  - Nothing else touched: cursor movement, hover highlight, approval
    clicks, scrolling, and all keybinds behave exactly as before.

## Behaviour notes

- Provider/model selection still happens in the plain CLI flow (`/local`);
  once a provider and model are chosen, the chat interface is the TUI.
- While the agent works you get `thinking...`; when a tool call needs
  permission you get `approval required` plus the usual
  `The agent wants to run:` details as events — type `y`/`a`/`n`, hit enter.
- `ctrl+c` quits the TUI.
- No changes to `harnessCore`, `harnessDispatch`, `harnessPermissions`, or
  `provider` packages were required to make this work.
