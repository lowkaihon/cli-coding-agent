# Pilot

A terminal-based AI coding agent built from scratch in Go. Pilot takes natural language instructions, explores your codebase using tools, and writes/edits files — all through a conversational REPL.

No agent frameworks. No LangChain. Zero external dependencies. Just an LLM API client, a hand-rolled tool orchestration loop, and the Go standard library.

## Demo

https://github.com/user-attachments/assets/b86646f0-b549-4ad4-8fda-19adbbfaca70

## Architecture

```
User input
    │
    ▼
┌─────────────────────────────────┐
│          Agent Loop             │
│                                 │
│  0. Auto-compact if > 80% ctx  │
│  1. Send messages → LLM        │
│  2. Stream response (SSE)      │
│  3. If tool_calls → execute    │
│     ├─ read-only? → parallel   │
│     └─ write/bash? → confirm   │
│  4. Append results to history  │
│  5. Repeat until done (≤50)    │
└──────┬──────────────┬──────────┘
       │              │
       ▼              ▼
  LLM Client     Tool Registry
  (OpenAI or     (glob, grep, ls,
   Anthropic)     read, write, edit,
                  bash, explore,
                  + task planning)
                       │
                       ▼
                 Explore Sub-Agent
                 (read-only tools,
                  isolated context,
                  ≤30 iterations)
```

**Package dependency graph** (strict DAG, no cycles):

| Package | Responsibility | Depends on |
|---------|---------------|------------|
| `cmd/pilot` | CLI entrypoint, REPL, signal handling | agent, config, llm, tools, ui |
| `agent` | Agent loop, compaction, checkpoints, sessions | llm, tools, ui |
| `llm` | LLM client interface, OpenAI + Anthropic, streaming, retry | — |
| `tools` | Tool registry, 11 tool implementations, path security | llm (types only) |
| `config` | API key management, .env loading, provider defaults | — |
| `ui` | Terminal output, colors, diffs, raw mode (cross-platform) | llm (types only) |

## Engineering Highlights

**LLM abstraction layer** — A single `LLMClient` interface (`SendMessage` + `StreamMessage`) abstracts provider differences. OpenAI uses the Responses API; Anthropic uses the Messages API. Message formats, tool call conventions, system prompt placement, and streaming event types are all translated internally. Swapping providers requires no changes outside `llm/`.

**Streaming with delta accumulation** — SSE streams are parsed into a common `StreamEvent` type. Tool call deltas arrive interleaved across chunks (indexed by position) and are accumulated into complete `ToolCall` objects. An `onText` callback enables real-time terminal display during accumulation.

**Retry with exponential backoff** — A shared `doWithRetry` function handles 429 and 5xx errors for both providers. Uses exponential backoff with jitter (2s base, 60s cap). Respects `Retry-After` headers as a one-shot delay override without permanently altering the backoff curve. Auth errors (401/403) fail immediately.

**Concurrent tool execution** — When all tool calls in a response are read-only, they execute in parallel via goroutines. Results are collected into a pre-allocated, position-indexed slice (no mutex needed). Write tools execute sequentially because each triggers an interactive confirmation prompt.

**Context management** — Token usage is tracked from API responses, with a chars/4 heuristic as fallback. At 80% of the context window, the agent auto-compacts by asking the LLM to summarize the conversation history (semantic compression, not mechanical truncation). History is replaced with `[system prompt, summary, last user message]`.

**Explore sub-agent** — The `explore` tool spawns a child agent with an isolated read-only tool registry. It uses non-streaming `SendMessage` to avoid interleaved terminal output, runs up to 30 iterations, and returns a summary. The callback is injected via `SetExploreFunc()` to break circular dependencies between `agent` and `tools`.

**Deferred write confirmation** — Write, edit, and bash tools don't execute immediately. They return a `NeedsConfirmation` error containing an `Execute()` closure. The agent loop type-asserts this error, shows the user a preview/diff, and only calls `Execute()` on approval. This cleanly separates tool logic from UI flow.

**Security model** — `ValidatePath()` resolves paths to absolute and verifies they're within the working directory (prevents traversal). `AtomicWrite()` writes to a temp file in the same directory, then renames (prevents partial writes on crash). Bash commands have a 30s default timeout, 120s max, and output is truncated at 10K chars.

**Session persistence & checkpoints** — Conversations auto-save to `.pilot/` as JSON. `/resume` reloads a previous session. Each turn creates a checkpoint with file snapshots, and `/rewind` can restore code, conversation, or both to any checkpoint.

**Task-based planning** — Three tools (`write_tasks`, `update_task`, `read_tasks`) let the LLM plan multi-step work by creating and tracking a task list. Tasks are stored outside the message history, persist in sessions, and survive context compaction. The system prompt instructs the LLM to create tasks before complex work and skip them for simple requests. Callbacks are injected via `SetTaskCallbacks()` following the same pattern as `SetExploreFunc()`.

## Features

- **Agentic tool-use loop** — the LLM decides which tools to call, executes them, and iterates until done
- **Streaming responses** — real-time token output via SSE
- **11 built-in tools** — glob, grep, ls, read, write, edit, bash, explore, + task planning (write_tasks, update_task, read_tasks)
- **Multi-provider** — OpenAI (Responses API) and Anthropic (Messages API), switchable at runtime via `/model`
- **Persistent memory** — project-scoped knowledge in `MEMORY.md`, injected into the system prompt
- **Session persistence** — auto-save conversations, resume previous sessions
- **Task-based planning** — LLM creates task lists for complex work, tracks progress via tools
- **Checkpoints & rewind** — restore code, conversation, or both to any previous turn
- **Context compaction** — LLM-based semantic summarization when approaching limits
- **Concurrent read-only tools** — parallel execution via goroutines
- **Cross-platform** — Windows, macOS, Linux (platform-specific raw mode and stdin handling)
- **Zero external dependencies** — pure Go standard library

## Tools

| Tool | Description |
|------|-------------|
| `glob` | Find files by pattern (`**/*.go`, `src/**/*.ts`) |
| `grep` | Search file contents with RE2 regex |
| `ls` | List directory contents with sizes |
| `read` | Read file with line numbers, supports line ranges |
| `write` | Create/overwrite files (requires confirmation) |
| `edit` | Replace exact string match in a file (requires confirmation) |
| `bash` | Execute shell commands (requires confirmation, 30s timeout) |
| `explore` | Spawn read-only sub-agent to research codebase |
| `write_tasks` | Create/replace task list for planning multi-step work |
| `update_task` | Update task status (pending → in_progress → completed) |
| `read_tasks` | Read current task list |

## Commands

| Command | Description |
|---------|-------------|
| `/help` | Show available commands |
| `/model` | Switch LLM model/provider |
| `/compact` | Force conversation compaction |
| `/clear` | Clear conversation history |
| `/context` | Show context window usage |
| `/tasks` | Show current task list |
| `/resume` | Resume a previously saved session |
| `/rewind` | Rewind to a previous checkpoint |
| `/quit` | Exit Pilot |

## Setup

**Requirements:** Go 1.25+ and an OpenAI or Anthropic API key.

```bash
# Install
go install github.com/lowkaihon/cli-coding-agent/cmd/pilot@latest

# Or build from source
git clone https://github.com/lowkaihon/cli-coding-agent.git
cd cli-coding-agent
go build -o pilot ./cmd/pilot
```

On first run, Pilot prompts for your API key and saves it to `~/.config/pilot/credentials`. You can also set it via environment variable:

```bash
export OPENAI_API_KEY="sk-..."       # OpenAI (default provider)
export ANTHROPIC_API_KEY="sk-ant-..."  # Anthropic
```

**Lookup order:** environment variable → `.env` in current directory → `~/.config/pilot/credentials`

## Usage

```bash
cd your-project/
pilot
```

```
> What files are in this project?
> Find all functions that return an error
> Add a /health endpoint to server.go
> Write a test for the handler
```

## Project Structure

```
cli-coding-agent/
├── cmd/pilot/
│   └── main.go                     # Entrypoint, REPL, slash commands, signal handling
├── agent/
│   ├── agent.go                    # Agent loop, tool execution, explore sub-agent
│   ├── context.go                  # Token estimation, compaction prompt
│   ├── checkpoint.go               # Checkpoint creation and rewind
│   ├── session.go                  # Session persistence (save/load/resume)
│   ├── task.go                    # Task type, planning state management
│   ├── messages.go                 # Message history accessor
│   ├── agent_test.go               # Agent loop + compaction tests
│   ├── checkpoint_test.go          # Checkpoint tests
│   └── session_test.go             # Session persistence tests
├── llm/
│   ├── types.go                    # LLMClient interface, Message, ToolCall, Response
│   ├── openai_responses.go         # OpenAI Responses API client
│   ├── openai_responses_stream.go  # OpenAI SSE streaming
│   ├── anthropic.go                # Anthropic Messages API client
│   ├── anthropic_stream.go         # Anthropic SSE streaming
│   ├── retry.go                    # Shared retry with exponential backoff + jitter
│   ├── stream.go                   # Stream accumulator (delta → complete response)
│   ├── openai_responses_test.go    # OpenAI client tests
│   ├── retry_test.go               # Retry logic tests
│   └── stream_test.go              # Stream accumulation tests
├── tools/
│   ├── registry.go                 # Tool registration, dispatch, read-only detection
│   ├── pathutil.go                 # ValidatePath (sandboxing) + AtomicWrite
│   ├── walk.go                     # Shared directory traversal skip list
│   ├── glob.go                     # Glob tool (** pattern matching)
│   ├── grep.go                     # Grep tool (RE2 regex)
│   ├── list.go                     # Ls tool
│   ├── read.go                     # Read tool (line ranges)
│   ├── write.go                    # Write tool (deferred confirmation)
│   ├── edit.go                     # Edit tool (exact string replacement)
│   ├── bash.go                     # Bash tool (sandboxed shell execution)
│   ├── explore.go                  # Explore tool + read-only registry
│   ├── task.go                    # Task planning tools (write_tasks, update_task, read_tasks)
│   └── tools_test.go              # Tool tests (all tools + path validation)
├── config/
│   ├── config.go                   # Provider config, .env loading, API key prompting
│   └── config_test.go              # Config tests
├── ui/
│   ├── terminal.go                 # ANSI colors, output, menus, escape listener
│   ├── diff.go                     # Diff display + confirmation prompt
│   ├── rawmode_unix.go             # Unix terminal raw mode (termios)
│   ├── rawmode_windows.go          # Windows terminal raw mode (Console API)
│   ├── rawmode_ioctl_linux.go      # Linux ioctl constants
│   ├── rawmode_ioctl_darwin.go     # macOS ioctl constants
│   ├── stdin_unix.go               # Unix stdin reader
│   └── stdin_windows.go            # Windows stdin reader
└── go.mod
```
