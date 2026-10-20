# Step 5 Cleanup — 2026-10-10

Cleanup pass on the opencode review findings. No behavioral change to the
agent, the permission pipeline, or the provider loops. Five review findings
addressed; the ones left out of scope on purpose are listed at the bottom.

## 1. Removed the dead `PolicyDenied` / `UserDenied` fields

- **`internals/harnessPermissions/bashPolicy.go`** — `BashOutcome` now holds
  `Text` only. The two bools were set in four places and read nowhere; a
  denial is already a successful dispatch whose text explains itself.
- **`internals/harnessTools/bash.go`** — dropped `PolicyDenied: true` from the
  class/deny outcomes and `UserDenied: true` from the approval-failure and
  user-denied outcomes.

Fixes: a type that promised information the dispatcher never consumed.

## 2. Replaced the `"Allow?"` substring coupling with a typed signal

This is the TUI issue. The approval buttons used to appear because the log
sink string-matched the word `Allow?` in the harness's prompt output, which
welded `askApproval.go`, `tui/run.go` and `tui/model.go` to one prompt string.

- **`internals/approval/approval.go`** (new) — leaf package importing only
  `sync`: `Listener` interface, `Set(l)`, `Notify()`. No listener registered
  (plain CLI path) means `Notify` is a no-op.
- **`internals/harnessPermissions/askApproval.go`** — calls
  `approval.Notify()` immediately before printing the prompt. The prompt text
  itself is unchanged.
- **`internals/tui/run.go`** — registers `approvalListener` (adapter doing
  `p.Send(approvalRequestedMsg{})`), cleared with `approval.Set(nil)` on exit;
  deleted the `strings.Contains(…, "Allow?")` block and its load-bearing
  comment. The log sink is now display-only, as it should be.
- **`internals/tui/model.go`** — comment on `approvalRequestedMsg` updated to
  name the notifier instead of the substring match.

Import direction stays clean: neither the harness nor the TUI imports the
other; both import `internals/approval`.

## 3. Quitting the TUI no longer hangs a pending approval prompt

The old shutdown left the agent goroutine blocked forever on the approval
channel if the user quit mid-prompt. Closing that channel was not safe —
`sendApproval` still pushes to it and a blocked sender would panic on close —
so the fix uses a second, write-only signal channel.

- **`internals/tui/run.go`** — adds `doneCh := make(chan struct{})`, passes it
  to `newLineSource`, and `close(doneCh)` in the existing deferred block (next
  to restoring log output and clearing the listener).
- **`internals/tui/run.go` (`lineSource`)** — `Read` now selects between the
  answer channel and `done`; on shutdown it returns `io.EOF`.
- **Effect** — `AskFunc`'s `ReadString` returns EOF, which it already maps to
  `No` (fail closed); file tools then return `ErrReadDenied` /
  `ErrEditDenied` / `ErrWriteDenied`, bash reports "the approval prompt
  failed", and every later approval fails instantly instead of hanging. The
  run unwinds. Nothing is sent on `doneCh`, so no close/send race is possible.

## 4. `os.Getwd` error is no longer swallowed

- **`internals/harnessPermissions/commandClassifier.go`** —
  `var cwd, _ = os.Getwd()` became `var cwd, cwdErr = os.Getwd()`, plus a
  `baseApprovedDirs()` helper that seeds the maps empty (and logs the reason)
  when the fetch failed.
- **`internals/harnessPermissions/readFilePolicy.go`**,
  **`internals/harnessPermissions/editPolicy.go`**,
  **`internals/harnessPermissions/writePolicy.go`** — the three per-tool
  approval maps are now seeded from `baseApprovedDirs()`.

Go's var-init ordering handles the `cwd` dependency. When `os.Getwd` succeeds
the behavior is identical; when it fails nothing is pre-approved, so every
directory is ask-once: fail closed, not open.

`homeDir` on the next line was left alone deliberately — a failure there
already fails closed through `NewBashSession`'s error path.

## 5. Repo and licensing

- **`LICENSE`** (new) — MIT, `Copyright (c) 2026 Matthew Baroi`. info.md says
  the project is fully open source but no LICENSE meant all rights reserved.
- **`.gitignore`** — added `step5CleanUp.md` (this file) and
  `/testRuns/tictactoe`.
- **`testRuns/tictactoe`** — `git rm --cached`, so the compiled scratch binary
  is no longer tracked. The file itself is untouched on disk, byte-identical.
- `internals/approval/` is a new tracked package; everything else modified.

## Left as-is on purpose (owner's call)

- **No tests** — testing is done by running the agent; `testRuns/` is the
  evidence directory.
- **The `.md` entries in `.gitignore`** (`whyInactive.md`, `changes.md`,
  `agentChanges.md`) — intentional, stays.
- **`go 1.26.6` in go.mod** — pinned on purpose.
- **Unsynchronized package-level approval state** (`allowList` in
  `harnessDispatch`, `approvedReadDirs` / `approvedEditDirs` /
  `approvedWriteDirs`) — known, deferred until concurrent agent turns are a
  real thing instead of a theoretical one.
- **The `commandClassifier.go` header comment** — stays as written.

## Verification

- `gofmt -l .` clean (after `gofmt -w` on `harnessTools/bash.go` for struct
  literal alignment and `tui/run.go` for import order).
- `go build ./...` and `go vet ./...` clean.
- No `PolicyDenied` / `UserDenied` references remain anywhere.
- The only remaining `Allow?` occurrences are the prompt text itself and one
  explanatory comment — nothing matches on it anymore.
