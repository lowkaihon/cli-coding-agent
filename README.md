# Pilot

A terminal-based AI coding agent built from scratch in Go. Pilot takes natural language instructions, explores your codebase using tools, and writes/edits files — all through a conversational REPL.

No agent frameworks. No LangChain. Just an LLM API client and a hand-rolled tool orchestration loop.

## Features

- **Agentic tool-use loop** — the LLM decides which tools to call, executes them, and iterates until the task is done
- **Streaming responses** — real-time token output via SSE, not batch responses
- **8 built-in tools** — glob, grep, ls, read, write, edit, bash, explore
- **Multi-provider** — OpenAI and Anthropic support (`-provider anthropic`)
- **User confirmation** — write/edit/bash operations require explicit `y/n` approval before executing
- **Path sandboxing** — all file operations are validated to stay within the working directory
- **Atomic file writes** — write and edit use temp file + rename to prevent corruption
- **Context management** — LLM-based conversation compaction when approaching context limits
- **Persistent sessions** — conversations auto-save in `.pilot/`, and `/resume` lets you reload a previous run
- **Checkpoints & rewind** — each turn can create checkpoints; `/rewind` lets you restore code, conversation, or summarize from a checkpoint
- **Persistent memory** — project-scoped knowledge in `MEMORY.md`, loaded into the system prompt
- **Concurrent tools** — read-only tool calls execute in parallel
- **Zero external dependencies** — pure Go standard library

## Setup

**Requirements:** Go 1.25+ and an OpenAI or Anthropic API key.

### Install

```bash
# Option A: go install (recommended)
go install github.com/lowkaihon/cli-coding-agent/cmd/pilot@latest

# Option B: build from source
git clone https://github.com/lowkaihon/cli-coding-agent.git
cd cli-coding-agent
go build -o pilot ./cmd/pilot
# Copy pilot (or pilot.exe on Windows) to a directory on your PATH
```

### API Key

On first run, Pilot will prompt for your API key and save it to `~/.config/pilot/credentials`.

You can also set it manually via environment variable:

```bash
# OpenAI (default)
export OPENAI_API_KEY="sk-..."

# Anthropic
export ANTHROPIC_API_KEY="sk-ant-..."
```

**Lookup order:** environment variable → `.env` in current directory → `~/.config/pilot/credentials`

## Usage

Navigate to any project directory and run `pilot`:

```bash
cd your-project/
pilot
```

If developing Pilot itself, you can also use:

```bash
go run ./cmd/pilot
```

Once running, type natural language instructions at the `>` prompt:

```
> What files are in this project?
> Find all functions that return an error
> Read main.go
> Add a /health endpoint to server.go
> Write a test for the handler
```

### Commands

| Command | Description |
|---------|-------------|
| `/help` | Show available commands |
| `/model` | Switch LLM model |
| `/compact` | Force conversation compaction (LLM summarizes history) |
| `/clear` | Clear conversation history (fresh start) |
| `/context` | Show context window usage |
| `/resume` | Resume a previously saved session |
| `/rewind` | Rewind to a previous checkpoint |
| `/quit` | Exit Pilot |

## Tools

| Tool | Description |
|------|-------------|
| `glob` | Find files by pattern (`**/*.go`, `src/**/*.ts`) — max 100 results |
| `grep` | Search file contents with RE2 regex — max 50 results |
| `ls` | List directory contents with sizes |
| `read` | Read file with line numbers (1-indexed), supports line ranges |
| `write` | Create/overwrite files (requires confirmation) |
| `edit` | Replace exact string match in a file (requires confirmation) |
| `bash` | Execute shell commands — builds, tests, git, etc. (requires confirmation, 30s timeout) |
| `explore` | Spawn read-only sub-agent to research codebase (max 30 iterations) |

## Project Structure

```
cli-coding-agent/
├── cmd/
│   └── pilot/
│       └── main.go           # Entry point, REPL, slash commands, signal handling
├── agent/
│   ├── agent.go              # Core agent loop, compaction logic
│   ├── agent_test.go         # Agent + compaction tests
│   ├── context.go            # Token estimation, compaction prompt, history serialization
│   └── messages.go           # Message history access
├── llm/
│   ├── types.go                 # Message, ToolCall, Response, StreamEvent types
│   ├── openai_responses.go      # OpenAI Responses client (GPT-4o, GPT-5.x)
│   ├── openai_responses_stream.go # Responses SSE streaming
│   ├── openai_responses_test.go # Responses client tests
│   ├── anthropic.go             # Anthropic client
│   ├── anthropic_stream.go      # Anthropic SSE streaming
│   ├── retry.go                 # Shared retry logic with exponential backoff
│   ├── stream.go                # Stream accumulator (shared)
│   └── stream_test.go           # Streaming tests
├── tools/
│   ├── registry.go           # Tool registration + dispatch
│   ├── tools_test.go         # Tool tests
│   ├── pathutil.go           # Path validation + atomic writes
│   ├── glob.go               # Glob tool
│   ├── grep.go               # Grep tool
│   ├── list.go               # Ls tool
│   ├── read.go               # Read tool
│   ├── write.go              # Write tool
│   ├── edit.go               # Edit tool
│   ├── bash.go               # Bash tool
│   └── explore.go            # Explore sub-agent tool
├── config/
│   ├── config.go             # Config from env/.env/credentials
│   └── config_test.go        # Config tests
├── ui/
│   ├── terminal.go           # ANSI colors, spinner, output
│   └── diff.go               # Diff display + confirmation
└── go.mod
```

## Architecture

```
User input
    │
    ▼
┌─────────────────────────────┐
│        Agent Loop           │
│                             │
│  0. Compact if over limit   │
│  1. Send messages → LLM     │
│  2. Stream response         │
│  3. If tool_calls → execute │
│  4. Append results          │
│  5. Repeat until done       │
└──────┬──────────────┬───────┘
       │              │
       ▼              ▼
  LLM Client     Tool Registry
  (OpenAI or     (glob, grep, ls,
   Anthropic)     read, write, edit,
                  bash)
```

**Key design decisions:**
- `LLMClient` interface for testability (mock in tests)
- `*string` for `Message.Content` to distinguish empty string from absent (OpenAI API requirement)
- Ordered slice registry (not map) for deterministic tool definition order
- `NeedsConfirmation` error type to trigger user prompts from tool code
- `context.Context` threaded through all I/O for cancellation support
- LLM-based compaction instead of mechanical truncation — preserves semantic context
