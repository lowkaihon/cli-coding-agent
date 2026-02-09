# Pilot

A terminal-based AI coding agent built from scratch in Go. Pilot takes natural language instructions, explores your codebase using tools, and writes/edits files — all through a conversational REPL.

No agent frameworks. No LangChain. Just an LLM API client and a hand-rolled tool orchestration loop.

## Features

- **Agentic tool-use loop** — the LLM decides which tools to call, executes them, and iterates until the task is done
- **Streaming responses** — real-time token output via SSE, not batch responses
- **6 built-in tools** — glob, grep, ls, read, write, edit
- **User confirmation** — write/edit operations require explicit `y/n` approval before modifying files
- **Path sandboxing** — all file operations are validated to stay within the working directory
- **Atomic file writes** — write and edit use temp file + rename to prevent corruption
- **Context window management** — automatic history truncation to stay within token limits
- **Zero external dependencies** — pure Go standard library

## Setup

**Requirements:** Go 1.25+ and an OpenAI API key.

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

On first run, Pilot will prompt for your OpenAI API key and save it to `~/.config/pilot/credentials`.

You can also set it manually via environment variable:

```bash
export OPENAI_API_KEY="sk-..."
```

**Lookup order:** environment variable → `.env` in current directory → `~/.config/pilot/credentials`

## Usage

Navigate to any project directory and run `pilot`:

```bash
cd your-project/
pilot

# Or with a specific model
pilot -model gpt-4o
```

If developing Pilot itself, you can also use:

```bash
go run ./cmd/pilot
go run ./cmd/pilot -model gpt-4o
```

Once running, type natural language instructions at the `>` prompt:

```
> What files are in this project?
> Find all functions that return an error
> Read main.go
> Add a /health endpoint to server.go
> Write a test for the handler
```

Type `exit` or press Ctrl+D to quit. Ctrl+C for graceful shutdown.

## Tools

| Tool | Description |
|------|-------------|
| `glob` | Find files by pattern (`**/*.go`, `src/**/*.ts`) — max 100 results |
| `grep` | Search file contents with RE2 regex — max 50 results |
| `ls` | List directory contents with sizes |
| `read` | Read file with line numbers (1-indexed), supports line ranges |
| `write` | Create/overwrite files (requires confirmation) |
| `edit` | Replace exact string match in a file (requires confirmation) |

## Project Structure

```
cli-coding-agent/
├── cmd/
│   └── pilot/
│       └── main.go   # Entry point, REPL, signal handling
├── agent/
│   ├── agent.go      # Core agent loop (max 50 iterations/turn)
│   ├── context.go    # Token estimation + history truncation
│   └── messages.go   # Message history access
├── llm/
│   ├── client.go     # OpenAI client with retry/backoff
│   ├── stream.go     # SSE streaming parser + accumulator
│   └── types.go      # Message, ToolCall, Response types
├── tools/
│   ├── registry.go   # Tool registration + dispatch
│   ├── pathutil.go   # Path validation + atomic writes
│   ├── glob.go       # Glob tool
│   ├── grep.go       # Grep tool
│   ├── list.go       # Ls tool
│   ├── read.go       # Read tool
│   ├── write.go      # Write tool
│   └── edit.go       # Edit tool
├── config/
│   └── config.go     # Config from env/.env file
├── ui/
│   ├── terminal.go   # ANSI colors, spinner, output
│   └── diff.go       # Diff display + confirmation
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
│  1. Send messages → LLM     │
│  2. Stream response         │
│  3. If tool_calls → execute │
│  4. Append results          │
│  5. Repeat until done       │
└──────┬──────────────┬───────┘
       │              │
       ▼              ▼
  LLM Client     Tool Registry
  (OpenAI API)   (glob, grep, ls,
                  read, write, edit)
```

**Key design decisions:**
- `LLMClient` interface for testability (mock in tests)
- `*string` for `Message.Content` to distinguish empty string from absent (OpenAI API requirement)
- Ordered slice registry (not map) for deterministic tool definition order
- `NeedsConfirmation` error type to trigger user prompts from tool code
- `context.Context` threaded through all I/O for cancellation support

## Configuration

| Setting | Default | Source |
|---------|---------|--------|
| API Key | — | `OPENAI_API_KEY` env var or `.env` file |
| Model | `gpt-4o-mini` | `-model` flag |
| Max Tokens | 4096 | hardcoded |
| Base URL | `https://api.openai.com/v1` | hardcoded |
