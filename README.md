# optine

optine is a local first, open source terminal agent for self hosted LLMs.
It runs entirely on your machine, talks to Ollama or vLLM, and gives the
model tools while keeping every mutating action behind your approval.

## What optine does

Type /local and pick a running provider. optine probes your models, checks
whether they support tool calling, and drops you into a terminal interface
where the model can search the web, read files, edit files, create files
and run bash commands. Models with native tool calling use it directly.
Models without it fall back to a JSON envelope protocol, so small local
models get the same harness. Every conversation is a session: history and
file snapshots are saved as you go, /resume brings one back after a restart,
and /revert restores the files from the latest run.

## Features

1. Provider loops for Ollama and vLLM, with a JSON envelope fallback for models without native tool calling
2. Five tools: web_search, read_file, bash, edit_file, write_file
3. A bash classifier that parses every command into the paths it touches and the effects it has, and denies anything it cannot analyze
4. Shared directory approvals for the file tools, plus session scoped grants per exact command and directory for bash
5. A JSONL audit log recorded before any action executes
6. Long term casual memory the model keeps in a plain file you can watch
7. A terminal interface with clickable approval prompts, markdown output, copy shortcuts and cached conversation rendering
8. Sessional memory: conversation history saved under your user config directory and reloaded with /resume from the same working directory, including the snapshot backend chosen when the session started, and replayed in the TUI
9. Before/after file snapshots for every run and /revert to restore changed or deleted files from the latest run and remove files it created

## Requirements

1. Go 1.27.2, pinned in go.mod
2. Linux or macOS for the bash tool
3. Ollama running on localhost:11434, or vLLM on localhost:8000
4. Optional: git for the git-backed snapshot backend; without it, snapshots use a content-addressed store

## Build and run

```
go build ./...
./optine
```

Type /local to start. /help lists commands, /resume lists saved sessions,
/revert restores files from the latest run, /quit exits.

## Sessions

Each conversation is a session with its own UID. The full history, in the
provider's native format, is saved after every run under your user config
directory, for example ~/.config/optine/sessions/ on Linux, along with the
provider, model, directory and prompt token count. Sessions are written
atomically, so quitting early cannot leave half a file behind.

/resume lists saved sessions for the current provider, tool mode and working
directory, and /resume last loads the newest one. A resumed session's saved
conversation is shown in the terminal interface before you type again, so the
previous user and assistant messages are visible in context. Sessions are
scoped to the directory they started in because both the tools and the
snapshot history belong to that working directory. Resume from the same
directory you launched optine from; sessions started elsewhere are not offered.

The snapshot backend is chosen once when a session is created and stored with
it. Resuming never re-detects git, so a session created with git snapshots
keeps them, and a session created with content-addressed snapshots keeps those,
even if git is installed or removed later.

## Reverting files

optine captures the working directory before and after every agent run, using
the session's saved snapshot backend. /revert uses the latest complete pair to
put changed or deleted files back and remove files created by that run.
Snapshots belong to the session, so this works after /resume as long as you
are in the same directory. /revert only touches files; it does not remove
messages from the transcript.

If the latest run has only a before snapshot or no snapshots at all, /revert
leaves the working directory alone and reports that there is no complete pair
to restore.

## Approvals

When the model wants to run something risky, optine asks. Answer y to allow
it once, a to remember it, or anything else to deny. A granted bash command
is remembered as that exact command in that exact directory for this session,
never as the whole tool. For file tools, a approves that directory for all of
read_file, edit_file and write_file, and safe bash reads and writes there, for
the rest of the process. Sensitive file reads, edits and writes always prompt,
even when their directory is approved; bash refuses sensitive paths outright.

## The safety model

The workspace root is set once at startup from your working directory, and
the model never chooses it. Every bash command is parsed into a list of
actions with read, write, delete, network and exec classes. Reads and writes
inside the workspace run without a prompt unless the path is sensitive, and
safe reads and writes are also allowed in directories you approve with a file
tool. Anything outside stays behind a prompt; delete, network and exec actions
always ask because their effects cannot be fully known. File writes are create
only, and any symlink in the parent chain is rejected, so the model cannot
write through a link you never checked. Every decision is recorded before the
action runs, and if the record cannot be written, the action does not run. Bash
grants are exact and session scoped; directory approvals from file tools are
shared with the other file tools and safe bash reads and writes for the rest of
the process.

The audit log, the memory notebook, saved sessions and file snapshots live in
your user config directory under optine, for example ~/.config/optine/audit.jsonl,
casualMemory.md, sessions/ and the snapshot stores on Linux.

## Project layout

1. internal/harnessCore, the provider agent loops, including recovery tests for malformed replies
2. internal/harnessSession, session history, system prompt cache and disk persistence
3. internal/harnessSnapshotter, git and content-addressed file snapshots plus revert
4. internal/harnessDispatch, tool call routing
5. internal/harnessPermissions, the classifier, path policies, approvals and the audit log
6. internal/harnessTools, tool execution
7. internal/harnessMemory, the casual memory notebook
8. internal/systemPrompts, prompt text
9. internal/provider, server probing and capability checks
10. internal/cli, provider and model pickers
11. internal/tui, the terminal interface

## Status

Skeleton stage, built solo by Matthew Baroi. Sessions survive restarts and can
be resumed with /resume; /revert restores file changes from the latest run.
Planned next: transcript undo, transcript compaction, and a bash sandbox.

## License

MIT. See LICENSE.
