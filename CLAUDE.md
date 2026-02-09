# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Run

```bash
go build -o pilot ./cmd/pilot      # Build binary
go vet ./...                       # Lint
go test ./...                      # Run all tests
go run ./cmd/pilot                 # Run without building
go run ./cmd/pilot -model gpt-4o  # Override default model
```

Zero external dependencies — pure Go standard library.

## Architecture

Pilot is a terminal-based AI coding agent. The main loop is a REPL that passes user input through an agentic tool-use cycle:

```
cmd/pilot/main.go (REPL + signal handling)
  → agent.Agent.Run()
      → llm.LLMClient.StreamMessage()  — sends messages, returns SSE event channel
      → llm.AccumulateStream()          — collects events, calls onText for live display
      → tools.Registry.Execute()        — dispatches tool calls
      → loop back until stop/no tools/50 iterations
```

**Package dependencies** (strict, no cycles):
- `main` → agent, config, llm, tools, ui
- `agent` → llm, tools, ui
- `tools` → llm (for type definitions only)
- `llm`, `config`, `ui` → no internal deps

## Critical Patterns

**`Message.Content` is `*string`, not `string`** — OpenAI API requires distinguishing `null` (omit) from `""` (empty). JSON `omitempty` on a plain string drops empty strings. Always use helper constructors: `llm.TextMessage(role, content)`, `llm.ToolResultMessage(id, content)`.

**`NeedsConfirmation` error for deferred writes** — Write and edit tools don't execute immediately. They return a `*tools.NeedsConfirmation` error containing an `Execute()` closure. The agent loop type-asserts this error, shows the user a preview, and only calls `Execute()` on approval. This keeps tool logic separate from UI confirmation.

**`tools.ValidatePath()` is mandatory** — Every file-operating tool must call `ValidatePath(workDir, requestedPath)` to sandbox paths within the working directory. Skipping this enables path traversal.

**`tools.AtomicWrite()`** — Shared by write and edit tools. Writes to a temp file in the same directory, then `os.Rename` for atomicity.

**Tool registry is an ordered slice** — Not a map. Registration order (glob → grep → ls → read → write → edit) is deterministic, which affects LLM behavior.

**Streaming accumulates tool calls by index** — `AccumulateStream()` maps tool call deltas by their `Index` field since multiple tool calls arrive interleaved across SSE chunks. The `onText` callback enables real-time display during accumulation.
