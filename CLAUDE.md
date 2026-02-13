# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Run

```bash
go build -o pilot ./cmd/pilot      # Build binary
go vet ./...                       # Lint
go test ./...                      # Run all tests
go run ./cmd/pilot                 # Run without building
```

Zero external dependencies — pure Go standard library.

## Architecture

Pilot is a terminal-based AI coding agent. The main loop is a REPL that passes user input through an agentic tool-use cycle:

```
cmd/pilot/main.go (REPL + slash commands + signal handling)
  → /help, /model, /compact, /clear, /context, /resume, /rewind, /quit handled directly
  → agent.CreateCheckpoint()           — snapshot files + conversation before each turn
  → agent.Agent.Run()
      → StartEscapeListener()          — wrap context with Esc key cancellation
      → compactIfNeeded()              — auto-compact at 80% context window
      → llm.LLMClient.StreamMessage()  — sends messages, returns SSE event channel
      → llm.AccumulateStream()         — collects events, calls onText for live display
      → tools.Registry.Execute()       — dispatches tool calls
      → loop back until stop/no tools/50 iterations
  → agent.SaveSession()                — auto-save conversation to ~/.pilot/
```

**Package dependencies** (strict, no cycles):

| Package | Responsibility | Dependencies |
|---------|---------------|--------------|
| `cmd/pilot` | CLI entrypoint, REPL, slash commands | agent, config, llm, tools, ui |
| `agent` | Agentic loop, message history, context compaction, sessions, checkpoints, memory | llm, tools, ui |
| `llm` | LLM client interface, OpenAI + Anthropic implementations, streaming | none (internal) |
| `tools` | Tool registry, all tool implementations, path security | llm (types only) |
| `config` | Configuration loading, .env parsing, API key management | none (internal) |
| `ui` | Terminal output, colors, diffs, confirmations | llm (types only) |

## Critical Patterns

**`Message.Content` is `*string`, not `string`** — OpenAI API requires distinguishing `null` (omit) from `""` (empty). JSON `omitempty` on a plain string drops empty strings. Always use helper constructors: `llm.TextMessage(role, content)`, `llm.ToolResultMessage(id, content)`.

**`NeedsConfirmation` error for deferred writes** — Write, edit, and bash tools return a `*tools.NeedsConfirmation` error containing an `Execute()` closure instead of executing immediately. The agent loop type-asserts this, shows a preview, and calls `Execute()` on approval.

**`tools.ValidatePath()` is mandatory** — Every file-operating tool must call `ValidatePath(workDir, requestedPath)` to sandbox paths within the working directory. Skipping this enables path traversal.

**`tools.AtomicWrite()`** — Shared by write and edit tools. Writes to a temp file in the same directory, then `os.Rename` for atomicity.

**Tool registry is an ordered slice** — Not a map. Registration order (glob → grep → ls → read → write → edit → bash → explore) is deterministic, which affects LLM behavior.

**Explore sub-agent** — The `explore` tool spawns a child agent with a read-only tool registry (glob, grep, ls, read). Uses non-streaming `SendMessage()` to avoid terminal output conflicts, up to 30 iterations. Callback injected via `SetExploreFunc()` to break circular dependency between agent and tools packages.

**Streaming accumulates tool calls by index** — `AccumulateStream()` maps tool call deltas by their `Index` field since multiple tool calls arrive interleaved across SSE chunks. The `onText` callback enables real-time display during accumulation.

**Retry logic is centralized** — `llm/retry.go` provides `doWithRetry()` with exponential backoff (2s base, 60s max) and jitter. Used by both providers for 429 and 5xx handling. Retry-After headers are consumed as a one-shot override without altering the backoff curve.

**Esc key interrupt** — `term.StartEscapeListener(ctx)` in `ui/terminal.go` wraps context with Esc key cancellation. Listener paused/resumed around `ConfirmAction()` to avoid raw mode conflicts with `fmt.Scanln`.

**Shared skip-dir logic** — `tools/walk.go` defines `shouldSkipDir()` used by both glob and grep to consistently skip `.git`, `node_modules`, `.venv`, `__pycache__` during directory traversal.

**Persistent memory** — `systemPrompt()` in `agent/agent.go` reads `MEMORY.md` from the working directory and appends its contents to the system prompt. No dedicated "remember" tool; the LLM uses `edit` on MEMORY.md directly.

**Session persistence & checkpoints** — Sessions auto-save to `~/.pilot/projects/<hash>/sessions/` as JSON (`agent/session.go`), where `<hash>` is a SHA256 prefix of the project's absolute path. `CreateCheckpoint()` snapshots conversation + modified files before each turn (`agent/checkpoint.go`). `captureFileBeforeModification()` populates `fileOriginals` map before write/edit execution. `/rewind` offers: restore code+conversation, conversation only, code only, or summarize-from via `SummarizeFrom()`. On `/resume`, `rebuildCheckpoints()` reconstructs checkpoint entries from the restored message history (conversation-only — no file snapshots).

## Go Style Conventions

Follow [Effective Go](https://go.dev/doc/effective_go), [Go Code Review Comments](https://go.dev/wiki/CodeReviewComments), and [Twelve Go Best Practices](https://go.dev/talks/2013/bestpractices.slide). Project-specific conventions:

- **Error strings** — Lowercase, no trailing punctuation: `fmt.Errorf("resolve home dir: %w", err)`. Wrap with `%w` for error chains.
- **Receiver names** — One or two letters from the type initial, consistent across all methods: `a` for Agent, `t` for Terminal, `r` for Registry, `c` for clients. Never `self`, `this`, or `me`.
- **Empty slices** — `var t []string` (nil) when appending later; `make([]T, n)` when length/capacity is known.
- **Imports** — stdlib first, blank line, then project imports. Alphabetical within each group.
- **Comments** — All exported symbols get doc comments starting with the symbol name, ending with a period. Unexported helpers only need comments when the logic is non-obvious.
- **Error flow** — Handle errors first with early returns; keep the happy path at minimal indentation.
- **`any` over `interface{}`** — Use the Go 1.18+ `any` alias everywhere.
- **Defer for cleanup** — Always `defer f.Close()` immediately after acquiring a resource. No manual cleanup in the happy path.
- **Named returns** — Avoid unless returning multiple values of the same type where names add documentation value.
- **Context** — Always the first parameter: `func Foo(ctx context.Context, ...)`.
- **Tool input parsing** — All tool functions use `parseInput[T](input)` from `tools/parse.go` to unmarshal JSON input. Per-field validation (e.g., "path is required") stays in each tool function.
- **Accept interfaces, return structs** — Functions accept interface parameters (`UI`, `LLMClient`) and return concrete types (`*Agent`, `*Registry`). Define interfaces in the consumer package (`agent.UI`), not the provider. Exception: `ui.Interrupter` lives in `ui` because multiple packages need it.
- **Important code first** — Order declarations: package doc → exports (constructors, key functions, types) → unexported helpers. Example: `config.go` orders `Config` → `Load()` → helper types → private functions.

## Context Management

Auto-compacts at 80% of context window (`ContextBuffer = 0.2`). Uses API-reported `TotalTokens` (`lastTokensUsed`), falling back to chars/4 heuristic (`EstimateTokens` in `agent/context.go`).

**Compaction flow** (`doCompact` in `agent/agent.go`):
1. `serializeHistory()` formats all messages into readable text (truncating long tool results and system prompts)
2. `SendMessage()` with a `compactionPrompt()` asks the LLM to summarize — no tools, pure text
3. History is replaced with: `[system prompt, summary as user message, last user message]`

**Triggers:**
- **Auto**: `compactIfNeeded()` runs at the top of every agent loop iteration
- **Manual**: `Compact()` exported method, called by `/compact` REPL command

`Clear()` resets history to just the system prompt and clears all checkpoints (no LLM call).

## Multi-Provider LLM Support

The `LLMClient` interface abstracts provider differences:

```go
type LLMClient interface {
    SendMessage(ctx, messages, tools) (*Response, error)
    StreamMessage(ctx, messages, tools) (<-chan StreamEvent, error)
}
```

OpenAI uses Responses API; Anthropic uses Messages API. Key differences handled internally:
- **Message format**: OpenAI uses `content` string; Anthropic uses content block arrays
- **Tool calls**: OpenAI has separate `tool_calls` field; Anthropic uses `tool_use` content blocks
- **System prompt**: OpenAI puts it in messages; Anthropic uses top-level `system` field
- **Streaming events**: Different SSE event types mapped to common `StreamEvent`
- **Tool results**: OpenAI uses `role: "tool"`; Anthropic uses `tool_result` content blocks in user messages

## Concurrent Tool Execution

When the LLM returns multiple tool calls, Pilot checks if all are read-only (glob, grep, ls, read, explore). If so, they execute concurrently via goroutines with `sync.WaitGroup`. Results are collected into a pre-allocated slice indexed by position — no mutex needed.

Write tools (write, edit, bash) execute sequentially because they return `NeedsConfirmation` errors requiring interactive user input. The `explore` sub-agent also runs read-only tools concurrently internally.
