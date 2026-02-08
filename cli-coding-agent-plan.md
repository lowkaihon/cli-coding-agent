# CLI Coding Agent in Go — Implementation Plan

## Overview

A terminal-based AI coding agent that takes natural language instructions, explores a codebase using tools (grep, glob, ls, read), and writes/edits files. Built from scratch in Go with no agent framework — just an LLM API client and a hand-rolled tool orchestration loop.

**Name suggestion:** `pilot`

---

## Architecture

```
┌─────────────────────────────────────────────────┐
│                   CLI (main.go)                  │
│  - Parse flags/args                              │
│  - Initialize config (API key, model, etc.)      │
│  - Signal handling (graceful shutdown)            │
│  - Launch REPL or single-shot mode               │
└──────────────────────┬──────────────────────────┘
                       │
                       ▼
┌─────────────────────────────────────────────────┐
│              Agent Loop (agent.go)               │
│                                                  │
│  1. Send messages + tool defs → LLM API          │
│  2. Check finish_reason for stop/tool_calls/length│
│  3. If response = text only → print & wait       │
│  4. If response = tool_use → execute tool        │
│  5. Append tool_result to messages               │
│  6. Check iteration limit (max 50 per turn)      │
│  7. Go to 1                                      │
│                                                  │
│  All functions accept context.Context            │
│                                                  │
└────────┬────────────────────────┬───────────────┘
         │                        │
         ▼                        ▼
┌─────────────────┐    ┌─────────────────────────┐
│   LLM Client    │    │     Tool Registry        │
│  (llm.go)       │    │     (tools.go)           │
│                 │    │                           │
│  - HTTP client  │    │  "grep"  → GrepTool()    │
│  - Streaming    │    │  "glob"  → GlobTool()    │
│  - Retry w/     │    │  "ls"    → ListTool()    │
│    backoff      │    │  "read"  → ReadTool()    │
│  - Error types  │    │  "write" → WriteTool()   │
│    (rate limit, │    │  "edit"  → EditTool()    │
│     auth, etc.) │    │                           │
│                 │    │  Path validation on all   │
│  Implements     │    │  file-based tools         │
│  LLMClient      │    │                           │
│  interface      │    │  (bash tool in Phase 6)   │
└─────────────────┘    └─────────────────────────┘
```

---

## Project Structure

```
pilot/
├── main.go              # Entry point, CLI parsing, REPL, signal handling
├── agent/
│   ├── agent.go         # Core agent loop
│   ├── messages.go      # Message history management
│   └── context.go       # Context window management / truncation
├── llm/
│   ├── client.go        # HTTP client for OpenAI API (implements LLMClient)
│   ├── types.go         # Request/response structs
│   └── stream.go        # SSE streaming parser
├── tools/
│   ├── registry.go      # Tool registration + dispatch
│   ├── pathutil.go      # Path validation (sandbox to working dir)
│   ├── grep.go          # Regex search across files (RE2 syntax)
│   ├── glob.go          # File pattern matching
│   ├── list.go          # Directory listing
│   ├── read.go          # Read file contents (with 1-indexed line ranges)
│   ├── write.go         # Write new files (atomic)
│   ├── edit.go          # String-replace edits on existing files (atomic)
│   └── bash.go          # (Phase 6) Run shell commands
├── config/
│   └── config.go        # API key, model selection, settings
├── ui/
│   ├── terminal.go      # Color output, spinners, formatting
│   └── diff.go          # Diff display for edits
├── go.mod
└── go.sum
```

---

## Phased Implementation

### Phase 1: Foundation (Get a working conversation loop)

**Goal:** You can type a message, it goes to the LLM, the response streams back to your terminal.

#### 1a. Project setup + config
- `go mod init github.com/<you>/pilot`
- Config struct: API key (from env var `OPENAI_API_KEY`), model name (`gpt-4o-mini`), max tokens
- CLI flag parsing with `flag` stdlib (keep it simple — no cobra needed yet)
- Signal handling: use `signal.NotifyContext` to create a root `context.Context` that cancels on SIGINT/SIGTERM

#### 1b. LLM client interface + implementation
- Define the `LLMClient` interface from the start (needed for testing with mocks):
  ```go
  // llm/client.go
  type LLMClient interface {
      SendMessage(ctx context.Context, messages []Message, tools []ToolDef) (*Response, error)
      StreamMessage(ctx context.Context, messages []Message, tools []ToolDef) (<-chan StreamEvent, error)
  }
  ```
- Implement `OpenAIClient` struct that satisfies `LLMClient`:
  - `func (c *OpenAIClient) SendMessage(ctx context.Context, messages []Message, tools []ToolDef) (*Response, error)`
  - Construct the HTTP request to `https://api.openai.com/v1/chat/completions`
  - Use `http.NewRequestWithContext(ctx, ...)` for cancellation support
  - Set headers: `Authorization: Bearer <key>`, `content-type: application/json`
  - Serialize request body: `model` ("gpt-4o-mini"), `messages`, `tools`
  - Parse response — tool calls live in `choices[0].message.tool_calls`
- Error classification and retry logic:
  - HTTP 429 (rate limit): exponential backoff with jitter, retry up to 3 times
  - HTTP 401/403 (auth): fail fast with clear error message
  - HTTP 500+ (server error): retry up to 2 times with backoff
  - Network errors: retry up to 2 times
  - All other errors: return immediately

- Define types in `llm/types.go`:
  ```go
  // OpenAI message format
  type Message struct {
      Role       string     `json:"role"`                  // "system", "user", "assistant", "tool"
      Content    *string    `json:"content"`               // pointer to distinguish empty string from absent
      ToolCalls  []ToolCall `json:"tool_calls,omitempty"`  // present in assistant msgs
      ToolCallID string     `json:"tool_call_id,omitempty"` // present in tool result msgs
  }

  // Helper constructors to avoid dealing with *string everywhere
  func TextMessage(role, content string) Message {
      return Message{Role: role, Content: &content}
  }

  func ToolResultMessage(toolCallID, content string) Message {
      return Message{Role: "tool", Content: &content, ToolCallID: toolCallID}
  }

  type ToolCall struct {
      ID       string       `json:"id"`
      Type     string       `json:"type"`     // "function"
      Function FunctionCall `json:"function"`
  }

  type FunctionCall struct {
      Name      string `json:"name"`
      Arguments string `json:"arguments"` // JSON string, not object
  }

  type ToolDef struct {
      Type     string       `json:"type"`     // "function"
      Function FunctionDef  `json:"function"`
  }

  type FunctionDef struct {
      Name        string          `json:"name"`
      Description string          `json:"description"`
      Parameters  json.RawMessage `json:"parameters"` // JSON Schema
  }

  // API response types
  type APIResponse struct {
      ID      string   `json:"id"`
      Choices []Choice `json:"choices"`
      Usage   Usage    `json:"usage"`
  }

  type Choice struct {
      Index        int     `json:"index"`
      Message      Message `json:"message"`
      FinishReason string  `json:"finish_reason"` // "stop", "tool_calls", "length"
  }

  type Usage struct {
      PromptTokens     int `json:"prompt_tokens"`
      CompletionTokens int `json:"completion_tokens"`
      TotalTokens      int `json:"total_tokens"`
  }

  // Higher-level response returned by the client
  type Response struct {
      Message      Message
      FinishReason string // "stop", "tool_calls", "length"
      Usage        Usage
  }
  ```

#### 1c. Basic REPL
- `main.go`: Read user input in a loop using `bufio.NewReader` with `ReadString('\n')`
  - Note: `bufio.Scanner` has a default 64KB buffer that silently truncates large pastes. `NewReader` avoids this.
- Send to LLM client, print response text
- Maintain message history slice
- Pass root context (from signal handler) to all LLM calls
- No tools yet — just a chatbot that works in the terminal

**Milestone:** You can have a conversation with gpt-4o-mini in your terminal, with Ctrl+C graceful shutdown.

---

### Phase 2: Tool System + System Prompt (The core differentiator)

**Goal:** The LLM can call tools to explore and understand your codebase, guided by a system prompt.

#### 2a. System prompt
Write the system prompt early — tools won't work well without it:
- Describe all available tools and when to use each
- Instruct the agent to explore before editing (read first, then modify)
- Set the working directory context (inject `os.Getwd()` result)
- Tell the agent to make minimal, targeted edits
- Encourage explaining what it's doing and why
- Note that `grep` uses RE2 regex syntax (no lookaheads/lookbehinds)

#### 2b. Path validation utility
Implement `tools/pathutil.go` — all file-based tools must use this:
```go
// ValidatePath ensures the resolved path is within the allowed working directory.
// Prevents path traversal attacks (e.g., "../../.ssh/id_rsa", "/etc/passwd").
func ValidatePath(workDir, requestedPath string) (string, error) {
    absPath, err := filepath.Abs(filepath.Join(workDir, requestedPath))
    if err != nil {
        return "", err
    }
    // Ensure the resolved path is within workDir
    rel, err := filepath.Rel(workDir, absPath)
    if err != nil || strings.HasPrefix(rel, "..") {
        return "", fmt.Errorf("path %q is outside the working directory", requestedPath)
    }
    return absPath, nil
}
```

#### 2c. Tool registry
```go
// tools/registry.go
type ToolFunc func(ctx context.Context, input json.RawMessage) (string, error)

type toolEntry struct {
    name string
    fn   ToolFunc
    def  llm.ToolDef
}

type Registry struct {
    tools []toolEntry  // ordered slice, not map — preserves registration order
}

func (r *Registry) Register(name, description string, schema json.RawMessage, fn ToolFunc)
func (r *Registry) Execute(ctx context.Context, name string, input json.RawMessage) (string, error)
func (r *Registry) Definitions() []llm.ToolDef  // returns defs in stable registration order
```

#### 2d. Read-only tools (implement these first)

All file-based tools call `ValidatePath` before any file operation.

**`glob`** — Find files by pattern
- Input: `{ "pattern": "**/*.go" }`
- Use `doublestar` library (`github.com/bmatcuk/doublestar/v4`)
- Use `doublestar.GlobWalk` with `os.DirEntry` — skip symlinks that point to directories (prevents infinite loops)
- Return: list of matching file paths (relative to working dir)
- Cap at 100 results, add "... and N more matches" if truncated
- Respect `.gitignore` — use `go-gitignore` (`github.com/sabhiram/go-gitignore`)

**`grep`** — Search file contents
- Input: `{ "pattern": "func main", "path": ".", "include": "*.go" }`
- `pattern`: RE2 regex (Go's `regexp` package — no lookaheads/lookbehinds)
- `path`: directory to search in (validated via `ValidatePath`)
- `include`: glob filter for filenames (e.g., `"*.go"`, `"*.{ts,tsx}"`)
- Walk files, compile regex, search line by line
- Return: matching lines with file path + line number
- Cap at 50 results, add "... and N more matches" if truncated

**`ls`** — List directory contents
- Input: `{ "path": "." }`
- Use `os.ReadDir`, return names with file/dir indicator and file sizes

**`read`** — Read file content
- Input: `{ "path": "main.go", "start_line": 1, "end_line": 50 }`
- Line numbers are **1-indexed** (matching editor conventions — this is what LLMs expect)
- Read file, return contents with line numbers
- Support line range to avoid dumping huge files into context
- If no range specified, cap at ~500 lines and tell the LLM there's more

#### 2e. Agent loop with tool dispatch

Update `agent/agent.go`:
```go
const MaxIterationsPerTurn = 50

func (a *Agent) Run(ctx context.Context, userMessage string) error {
    a.messages = append(a.messages, llm.TextMessage("user", userMessage))

    for iteration := 0; iteration < MaxIterationsPerTurn; iteration++ {
        resp, err := a.llm.SendMessage(ctx, a.messages, a.tools.Definitions())
        if err != nil {
            return err
        }

        // Append the assistant message (includes any tool_calls)
        a.messages = append(a.messages, resp.Message)

        // Handle finish_reason
        switch resp.FinishReason {
        case "length":
            // Response was truncated — warn user
            fmt.Println("[warning] Response was truncated due to token limit")
            return nil
        case "stop":
            // Normal text-only completion
            return nil
        case "tool_calls":
            // Fall through to tool execution below
        }

        if len(resp.Message.ToolCalls) == 0 {
            return nil
        }

        // Validate tool call arguments are valid JSON before dispatching
        for _, tc := range resp.Message.ToolCalls {
            if !json.Valid([]byte(tc.Function.Arguments)) {
                a.messages = append(a.messages, llm.ToolResultMessage(
                    tc.ID,
                    fmt.Sprintf("Error: invalid JSON in tool arguments: %s", tc.Function.Arguments),
                ))
                continue
            }

            input := json.RawMessage(tc.Function.Arguments)
            output, toolErr := a.tools.Execute(ctx, tc.Function.Name, input)

            resultContent := output
            if toolErr != nil {
                resultContent = fmt.Sprintf("Error: %s", toolErr)
                // Log error locally (not just to LLM context)
                slog.Warn("tool execution failed",
                    "tool", tc.Function.Name,
                    "error", toolErr,
                )
            }

            a.messages = append(a.messages, llm.ToolResultMessage(tc.ID, resultContent))
        }
        // Loop back — send tool results to LLM for next step
    }

    return fmt.Errorf("agent loop exceeded maximum iterations (%d)", MaxIterationsPerTurn)
}
```

**Milestone:** You can say "What files are in this project?" or "Find all functions that handle HTTP requests" and the agent explores your codebase to answer, guided by the system prompt.

---

### Phase 3: Streaming + UX Polish

**Goal:** Real-time streaming output — critical for usable dogfooding.

#### 3a. SSE streaming
- Implement `llm/stream.go`:
  - Set `"stream": true` in the request body
  - Use `http.NewRequestWithContext(ctx, ...)` for cancellation
  - Parse `text/event-stream` response from OpenAI API
  - Each SSE line is `data: {json}` — parse `choices[0].delta`
  - Delta contains either `content` (text chunk) or `tool_calls` (accumulated incrementally)
  - `data: [DONE]` signals end of stream
  - Print text deltas to terminal in real-time
- **Streaming tool call accumulation** (this is the tricky part):
  - Tool calls in streaming arrive as deltas with an `index` field
  - Multiple concurrent tool calls have different indices
  - Maintain a `map[int]*ToolCall` to accumulate arguments per index
  - Arguments arrive as partial JSON strings — concatenate them per-index
  - Only dispatch tool calls after the stream completes and all arguments are fully assembled
  - Validate the concatenated JSON is valid before dispatching

#### 3b. Terminal UI
- Color output using raw ANSI codes (keep dependencies minimal):
  - Tool calls in yellow/dim
  - Tool results in gray
  - Agent text in white
  - Errors in red
  - Diffs with red/green highlighting
- Use `github.com/mattn/go-isatty` to detect terminal — disable colors when piped
- Spinner/indicator while the LLM is thinking
- Clear separation between agent turns

**Milestone:** Responses stream in real-time. Tool calls show progress. Feels like a real tool.

---

### Phase 4: Write Tools (Make it actually useful)

**Goal:** The agent can create and modify files.

#### 4a. Atomic file write utility
Shared by both `write` and `edit` tools:
```go
// tools/pathutil.go (or a new fileutil.go)
func AtomicWrite(targetPath string, content []byte, perm os.FileMode) error {
    // Create temp file in the SAME directory as target (required for rename to work)
    dir := filepath.Dir(targetPath)
    tmp, err := os.CreateTemp(dir, ".pilot-*")
    if err != nil {
        return err
    }
    tmpPath := tmp.Name()

    // Clean up temp file on any error
    defer func() {
        if tmpPath != "" {
            os.Remove(tmpPath)
        }
    }()

    if _, err := tmp.Write(content); err != nil {
        tmp.Close()
        return err
    }
    if err := tmp.Close(); err != nil {
        return err
    }
    if err := os.Chmod(tmpPath, perm); err != nil {
        return err
    }
    if err := os.Rename(tmpPath, targetPath); err != nil {
        return err
    }
    tmpPath = "" // prevent deferred cleanup
    return nil
}
```

#### 4b. Write tool
- Input: `{ "path": "pkg/new_file.go", "content": "package pkg\n..." }`
- Validate path with `ValidatePath`
- Create parent directories if needed (`os.MkdirAll`)
- Write file using `AtomicWrite`
- Return confirmation with path

#### 4c. Edit tool (string-replace approach)
- Input: `{ "path": "main.go", "old_str": "exact string to find", "new_str": "replacement" }`
- Validate path with `ValidatePath`
- Read file, count occurrences of `old_str`
- If 0 matches: return error with a hint (e.g., "No match found. Check for whitespace differences.")
- If 2+ matches: return error with match count and line numbers for each match, so the LLM can retry with more surrounding context
- If exactly 1 match: replace and write back using `AtomicWrite`
- Return a unified diff of the change for display

This is the same approach Claude Code and Aider use — it's simple, predictable, and the LLM is good at generating exact string matches.

#### 4d. User confirmation for writes
- Before executing `write` or `edit`, show the user what's about to happen
- Display a diff (for edits) or file preview (for writes)
- Prompt `[y/n]` to confirm
- This is important for trust and safety

**Milestone:** You can say "Add error handling to the HTTP handler in server.go" and the agent reads the file, proposes an edit, and applies it with your approval.

---

### Phase 5: Context Window Management

**Goal:** Handle long sessions without crashing or degrading.

#### 5a. Token counting
- Use a simple heuristic: 1 token ≈ 4 chars for English/code
  - This is good enough for context window management — exact counts aren't needed
  - Avoids the `tiktoken-go` dependency (which also wouldn't work for non-OpenAI models)
- Track approximate token usage of message history
- Know the model's context limit (128k for gpt-4o-mini)

#### 5b. Truncation strategy
When approaching the context limit:
1. Keep the system prompt (always)
2. Keep the first user message (establishes the task)
3. Keep the last N messages (recent context)
4. For middle messages: summarize tool results (replace full file contents with "Read main.go (245 lines)" summaries)
5. Drop old tool call/result pairs first — they're the biggest token consumers

#### 5c. Smart tool output capping
- `read`: If file is >300 lines and no range specified, return first/last 50 lines with a note
- `grep`: Cap at 50 results, add "... and N more matches"
- `glob`: Cap at 100 results
- This prevents a single tool call from consuming the entire context window

**Milestone:** The agent can handle multi-step tasks across long conversations without context window errors.

---

### Phase 6: Extras (Stretch goals for polish)

#### 6a. Bash tool (optional, guarded)
- Input: `{ "command": "go build ./..." }`
- Execute with `os/exec`, capture stdout+stderr
- Always require user confirmation
- Timeout after 30 seconds (use `context.WithTimeout`)
- **Security:** sanitize the child process environment — strip `OPENAI_API_KEY` and other sensitive env vars
- This lets the agent run tests, check compilation, etc.

#### 6b. Conversation persistence
- Save/load conversation history to `~/.pilot/history/`
- Allow resuming previous sessions
- `pilot --resume` to continue last conversation

#### 6c. `.pilotignore` / config file
- Read `.pilotignore` to exclude files/dirs from tool exploration
- `pilot.toml` for project-level config (model, custom system prompt additions, etc.)

#### 6d. Multi-model support
- The `LLMClient` interface is already defined from Phase 1
- Implement `AnthropicClient` (Anthropic uses different types — `tool_use` content blocks, not `tool_calls`)
- Select via `--model` flag

---

## Key Design Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| No agent framework | Build loop from scratch | Demonstrates understanding; this is what the role requires |
| String-replace edits | Not AST-based or patch-based | Same approach as Claude Code/Aider; LLMs are good at exact string matching |
| Streaming early (Phase 3) | Not deferred to later | UX is critical for a CLI tool; non-streaming feels broken; needed for dogfooding |
| User confirmation for writes | Not auto-apply | Trust/safety matters; shows mature engineering judgment |
| OpenAI API (gpt-4o-mini) | Cost-efficient for development/demo | Cheap to iterate; easy to swap models later via interface |
| Minimal dependencies | Stdlib where possible | Shows Go proficiency; keeps binary small |
| `context.Context` everywhere | Standard Go practice | Enables cancellation, timeouts, and graceful shutdown |
| `LLMClient` interface from Phase 1 | Not deferred to stretch goals | Needed for testing agent loop with mocks |
| Path validation on all tools | Security by default | Prevents LLM from reading/writing outside working directory |
| Atomic writes for all file ops | Both write and edit | Prevents file corruption on interrupted operations |

---

## Dependencies (Keep it lean)

```
go.mod:
- No agent frameworks
- No LangChain/LangGraph equivalents

Required dependencies:
- github.com/bmatcuk/doublestar/v4  (glob patterns with **)
- github.com/sabhiram/go-gitignore  (respecting .gitignore)
- github.com/mattn/go-isatty        (detect terminal for color output)

Optional:
- fatih/color (terminal colors) — or just use raw ANSI codes (preferred for minimal deps)

NOT using:
- tiktoken-go — heuristic (4 chars/token) is sufficient for context management

Everything else: stdlib (net/http, encoding/json, os, os/exec, regexp, bufio, filepath,
                         context, log/slog, os/signal)
```

---

## Logging

Use `log/slog` (standard library since Go 1.21) for structured logging:
- Log every tool call with name and arguments
- Log tool results (truncated) and errors
- Log LLM API calls with token usage
- Write to `~/.pilot/pilot.log` or stderr with `--verbose` flag
- This provides an audit trail — critical for a tool that modifies files

---

## Testing Strategy

- **Unit tests for each tool:** Use `os.MkdirTemp` for filesystem tests. Test edge cases (empty files, binary files, symlinks, permission errors).
- **Unit tests for LLM client:** Use `httptest.NewServer` to mock OpenAI API responses. Test error handling (429, 500, malformed JSON).
- **Integration tests for agent loop:** Mock `LLMClient` interface with scripted tool-call sequences. Verify the loop terminates, handles errors, and respects iteration limits.
- **Tool registry tests:** Verify registration order is preserved, unknown tool errors, and dispatch correctness.

---

## What This Demonstrates to the Interviewer

1. **Agent architecture from scratch** — You understand the tool-use loop, not just how to import a framework
2. **Go backend skills** — Clean project structure, interfaces, error handling, `context.Context`, `log/slog`
3. **LLM integration** — API client, streaming, token management, system prompt engineering
4. **Practical AI Coding tool** — Exactly what the team is building internally
5. **Production sensibilities** — User confirmation, error handling, context management, .gitignore support, path sandboxing
6. **Security awareness** — Path traversal prevention, env var sanitization, atomic writes
7. **Testing discipline** — Interface-based mocking, filesystem test isolation

---

## Suggested Timeline

| Week | Milestone |
|------|-----------|
| 1 | Phase 1 + 2 complete — working agent that can explore codebases with system prompt |
| 2 | Phase 3 + 4 complete — streaming UX, agent can edit files with confirmation |
| 3 | Phase 5 + polish — context management, README, demo recording |
| 4 | Phase 6 stretch goals if time permits |

Start using it on your own code from Week 1. Dogfooding surfaces real issues fast.

---

## Demo Script (For interviews / README)

Show the agent completing a real multi-step task:

1. "Add a `/health` endpoint to my Go HTTP server"
2. Agent uses `glob` to find Go files → `read` to understand the server structure → `read` the router setup → `edit` to add the handler → `edit` to register the route
3. "Now add a test for it"
4. Agent uses `read` to check existing test patterns → `write` to create the test file
5. (If bash tool is implemented) "Run the tests" → agent runs `go test ./...` and reports results

This demo shows exploration → understanding → modification → verification — the full coding agent loop.
