# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Run

```bash
go build -o pilot ./cmd/pilot      # Build binary
go vet ./...                       # Lint
go test ./...                      # Run all tests
go run ./cmd/pilot                 # Run without building
go run ./cmd/pilot -model gpt-4o  # Override default model
go run ./cmd/pilot -provider anthropic  # Use Anthropic
```

Zero external dependencies — pure Go standard library.

## Architecture

Pilot is a terminal-based AI coding agent. The main loop is a REPL that passes user input through an agentic tool-use cycle:

```
cmd/pilot/main.go (REPL + slash commands + signal handling)
  → /help, /model, /compact, /clear, /context, /quit handled directly
  → agent.Agent.Run()
      → compactIfNeeded()                — auto-compact at 80% context window
      → llm.LLMClient.StreamMessage()   — sends messages, returns SSE event channel
      → llm.AccumulateStream()           — collects events, calls onText for live display
      → tools.Registry.Execute()         — dispatches tool calls
      → loop back until stop/no tools/50 iterations
```

**Package dependencies** (strict, no cycles):

| Package | Responsibility | Dependencies |
|---------|---------------|--------------|
| `cmd/pilot` | CLI entrypoint, REPL, flags | agent, config, llm, tools, ui |
| `agent` | Agentic loop, message history, context compaction, memory | llm, tools, ui |
| `llm` | LLM client interface, OpenAI + Anthropic implementations, streaming | none (internal) |
| `tools` | Tool registry, all tool implementations, path security | llm (types only) |
| `config` | Configuration loading, .env parsing, API key management | none (internal) |
| `ui` | Terminal output, colors, diffs, confirmations | none (internal) |

## Critical Patterns

**`Message.Content` is `*string`, not `string`** — OpenAI API requires distinguishing `null` (omit) from `""` (empty). JSON `omitempty` on a plain string drops empty strings. Always use helper constructors: `llm.TextMessage(role, content)`, `llm.ToolResultMessage(id, content)`.

**`NeedsConfirmation` error for deferred writes** — Write, edit, and bash tools don't execute immediately. They return a `*tools.NeedsConfirmation` error containing an `Execute()` closure. The agent loop type-asserts this error, shows the user a preview, and only calls `Execute()` on approval. This keeps tool logic separate from UI confirmation.

**`tools.ValidatePath()` is mandatory** — Every file-operating tool must call `ValidatePath(workDir, requestedPath)` to sandbox paths within the working directory. Skipping this enables path traversal.

**`tools.AtomicWrite()`** — Shared by write and edit tools. Writes to a temp file in the same directory, then `os.Rename` for atomicity.

**Tool registry is an ordered slice** — Not a map. Registration order (glob → grep → ls → read → write → edit → bash) is deterministic, which affects LLM behavior.

**Streaming accumulates tool calls by index** — `AccumulateStream()` maps tool call deltas by their `Index` field since multiple tool calls arrive interleaved across SSE chunks. The `onText` callback enables real-time display during accumulation.

## Context Management

The agent auto-compacts when token usage exceeds 80% of the context window (`ContextBuffer = 0.2`). It uses API-reported `TotalTokens` from the most recent response (`lastTokensUsed` in `agent/agent.go`), falling back to a chars/4 heuristic (`EstimateTokens` in `agent/context.go`) before the first API call.

**Compaction flow** (`doCompact` in `agent/agent.go`):
1. `serializeHistory()` formats all messages into readable text (truncating long tool results and system prompts)
2. `SendMessage()` with a `compactionPrompt()` asks the LLM to summarize — no tools, pure text
3. History is replaced with: `[system prompt, summary as user message, last user message]`

**Triggers:**
- **Auto**: `compactIfNeeded()` runs at the top of every agent loop iteration
- **Manual**: `Compact()` exported method, called by `/compact` REPL command

`Clear()` resets history to just the system prompt (no LLM call).

## Multi-Provider LLM Support

The `LLMClient` interface abstracts provider differences:

```go
type LLMClient interface {
    SendMessage(ctx, messages, tools) (*Response, error)
    StreamMessage(ctx, messages, tools) (<-chan StreamEvent, error)
}
```

Key differences handled internally:
- **Message format**: OpenAI uses `content` string; Anthropic uses content block arrays
- **Tool calls**: OpenAI has separate `tool_calls` field; Anthropic uses `tool_use` content blocks
- **System prompt**: OpenAI puts it in messages; Anthropic uses top-level `system` field
- **Streaming events**: Different SSE event types mapped to common `StreamEvent`
- **Tool results**: OpenAI uses `role: "tool"`; Anthropic uses `tool_result` content blocks in user messages

## Concurrent Tool Execution

When the LLM returns multiple tool calls, Pilot checks if all are read-only (glob, grep, ls, read). If so, they execute concurrently via goroutines with `sync.WaitGroup`. Results are collected into a pre-allocated slice indexed by position — no mutex needed.

Write tools (write, edit, bash) execute sequentially because they return `NeedsConfirmation` errors requiring interactive user input.

## Security Model

- **Path sandboxing**: `ValidatePath()` resolves to absolute, verifies within working directory, rejects traversal
- **Deferred writes**: `NeedsConfirmation` error type separates tool logic from user confirmation
- **Bash safety**: All commands require confirmation, 30s default timeout (120s max), output truncated to 10,000 chars
- **Atomic writes**: Temp file + `os.Rename` prevents partial writes on crash
