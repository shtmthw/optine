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
models get the same harness.

## Features

1. Provider loops for Ollama and vLLM, with a JSON envelope fallback for models without native tool calling
2. Five tools: web_search, read_file, bash, edit_file, write_file
3. A bash classifier that parses every command into the paths it touches and the effects it has, and denies anything it cannot analyze
4. Session scoped grants per exact command and directory for bash, per directory for the file tools
5. A JSONL audit log recorded before any action executes
6. Long term casual memory the model keeps in a plain file you can watch
7. A terminal interface with clickable approval prompts, markdown output and copy shortcuts

## Requirements

1. Go 1.27.2, pinned in go.mod
2. Linux or macOS for the bash tool
3. Ollama running on localhost:11434, or vLLM on localhost:8000

## Build and run

```
go build ./...
./optine
```

Type /local to start. /help lists commands, /quit exits.

## Approvals

When the model wants to run something risky, optine asks. Answer y to allow
it once, a to allow it for this session, or anything else to deny. A granted
bash command is remembered as that exact command in that exact directory,
never as the whole tool. For file tools, a allows the current directory for
the rest of the session. Sensitive paths are denied outright and are never
grantable.

## The safety model

The workspace root is set once at startup from your working directory, and
the model never chooses it. Every bash command is parsed into a list of
actions with read, write, delete, network and exec classes. Reads and
writes inside the workspace run without a prompt, anything outside asks,
and unknown programs ask because their effects cannot be known. File
writes are create only, and any symlink in the parent chain is rejected,
so the model cannot write through a link you never checked. Every decision
is recorded before the action runs, and if the record cannot be written,
the action does not run. Grants are exact, session scoped and gone when
the process exits.

The audit log and the memory notebook live in your user config directory
under optine, for example ~/.config/optine/audit.jsonl and casualMemory.md
on Linux.

## Project layout

1. internals/harnessCore, the provider agent loops, including recovery tests for malformed replies
2. internals/harnessDispatch, tool call routing
3. internals/harnessPermissions, the classifier, path policies, approvals and the audit log
4. internals/harnessTools, tool execution
5. internals/harnessMemory, the casual memory notebook
6. internals/systemPrompts, prompt text
7. internals/provider, server probing and capability checks
8. internals/cli, provider and model pickers
9. internals/tui, the terminal interface

## Status

Skeleton stage, built solo by Matthew Baroi. Planned next: per session
memory with undo and revert, transcript compaction, and a bash sandbox.

## License

MIT. See LICENSE.
