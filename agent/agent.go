package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/lowkaihon/cli-coding-agent/llm"
	"github.com/lowkaihon/cli-coding-agent/tools"
	"github.com/lowkaihon/cli-coding-agent/ui"
)

const MaxIterationsPerTurn = 50

// Agent orchestrates the LLM conversation and tool execution loop.
type Agent struct {
	client         llm.LLMClient
	tools          *tools.Registry
	messages       []llm.Message
	workDir        string
	contextWindow  int
	lastTokensUsed int // TotalTokens from most recent API response
	sessionID      string
	sessionCreated time.Time
	checkpoints    []Checkpoint              // ordered by turn
	fileOriginals  map[string]*FileSnapshot  // pre-session state of each modified file
}

// New creates a new Agent with the system prompt initialized.
func New(client llm.LLMClient, registry *tools.Registry, workDir string, contextWindow int) *Agent {
	a := &Agent{
		client:         client,
		tools:          registry,
		workDir:        workDir,
		contextWindow:  contextWindow,
		sessionID:      generateSessionID(),
		sessionCreated: time.Now(),
		fileOriginals:  make(map[string]*FileSnapshot),
	}
	a.messages = []llm.Message{
		llm.TextMessage("system", a.systemPrompt()),
	}
	return a
}

// SetClient swaps the LLM client and context window (e.g., after /model).
func (a *Agent) SetClient(client llm.LLMClient, contextWindow int) {
	a.client = client
	a.contextWindow = contextWindow
}

// Run processes a user message through the agent loop.
func (a *Agent) Run(ctx context.Context, userMessage string, term *ui.Terminal) error {
	a.messages = append(a.messages, llm.TextMessage("user", userMessage))

	// Start escape listener for Esc key cancellation
	opCtx, listener, escErr := term.StartEscapeListener(ctx)
	if escErr != nil {
		// No TTY or raw mode unavailable — fall back to parent context
		opCtx = ctx
	}
	if listener != nil {
		defer listener.Stop()
	}

	for iteration := 0; iteration < MaxIterationsPerTurn; iteration++ {
		a.compactIfNeeded(opCtx, term)
		term.PrintSpinner()

		events, err := a.client.StreamMessage(opCtx, a.messages, a.tools.Definitions())
		if err != nil {
			term.ClearSpinner()
			if opCtx.Err() != nil {
				fmt.Println()
				return context.Canceled
			}
			return fmt.Errorf("LLM request failed: %w", err)
		}

		term.ClearSpinner()

		resp, err := llm.AccumulateStream(events, func(text string) {
			term.PrintAssistant(text)
		})
		if err != nil {
			if opCtx.Err() != nil {
				fmt.Println()
				return context.Canceled
			}
			return fmt.Errorf("stream error: %w", err)
		}

		if resp.Usage.TotalTokens > 0 {
			a.lastTokensUsed = resp.Usage.TotalTokens
		}

		a.messages = append(a.messages, resp.Message)

		switch resp.FinishReason {
		case "length":
			term.PrintAssistantDone()
			term.PrintWarning("Response was truncated due to token limit.")
			return nil
		case "stop":
			term.PrintAssistantDone()
			return nil
		}

		if len(resp.Message.ToolCalls) == 0 {
			term.PrintAssistantDone()
			return nil
		}

		// Print newline after any streamed text before tool output
		if resp.Message.Content != nil && *resp.Message.Content != "" {
			fmt.Println()
		}

		results := a.executeToolCalls(opCtx, resp.Message.ToolCalls, term, listener)
		if opCtx.Err() != nil {
			// Cancelled during tool execution — still record any results we got
			for _, r := range results {
				if r.output != "" {
					a.messages = append(a.messages, llm.ToolResultMessage(r.id, r.output))
				}
			}
			fmt.Println()
			return context.Canceled
		}
		for _, r := range results {
			a.messages = append(a.messages, llm.ToolResultMessage(r.id, r.output))
		}
	}

	return fmt.Errorf("agent loop exceeded maximum iterations (%d)", MaxIterationsPerTurn)
}

type toolResult struct {
	id     string
	output string
}

// executeToolCalls runs tool calls, parallelizing read-only ones.
func (a *Agent) executeToolCalls(ctx context.Context, calls []llm.ToolCall, term *ui.Terminal, listener *ui.InterruptListener) []toolResult {
	results := make([]toolResult, len(calls))

	// Check if all calls are read-only
	allReadOnly := true
	for _, tc := range calls {
		if !a.tools.IsReadOnly(tc.Function.Name) {
			allReadOnly = false
			break
		}
	}

	if allReadOnly && len(calls) > 1 {
		// Execute read-only tools concurrently
		for i, tc := range calls {
			term.PrintToolCall(tc.Function.Name, tc.Function.Arguments)
			results[i].id = tc.ID
		}

		var wg sync.WaitGroup
		for i, tc := range calls {
			if !json.Valid([]byte(tc.Function.Arguments)) {
				results[i].output = fmt.Sprintf("Error: invalid JSON in tool arguments: %s", tc.Function.Arguments)
				continue
			}
			wg.Add(1)
			go func(idx int, tc llm.ToolCall) {
				defer wg.Done()
				input := json.RawMessage(tc.Function.Arguments)
				output, err := a.tools.Execute(ctx, tc.Function.Name, input)
				if err != nil {
					output = fmt.Sprintf("Error: %s", err)
				}
				results[idx].output = output
			}(i, tc)
		}
		wg.Wait()

		for _, r := range results {
			term.PrintToolResult(r.output)
		}
	} else {
		// Execute sequentially (write tools need confirmation one at a time)
		for i, tc := range calls {
			results[i].id = tc.ID

			if !json.Valid([]byte(tc.Function.Arguments)) {
				errMsg := fmt.Sprintf("Error: invalid JSON in tool arguments: %s", tc.Function.Arguments)
				results[i].output = errMsg
				term.PrintToolCall(tc.Function.Name, "invalid JSON")
				continue
			}

			term.PrintToolCall(tc.Function.Name, tc.Function.Arguments)

			input := json.RawMessage(tc.Function.Arguments)
			output, toolErr := a.tools.Execute(ctx, tc.Function.Name, input)

			if toolErr != nil {
				if confirm, ok := toolErr.(*tools.NeedsConfirmation); ok {
					output = a.handleConfirmation(confirm, term, listener)
				} else {
					output = fmt.Sprintf("Error: %s", toolErr)
				}
			}

			term.PrintToolResult(output)
			results[i].output = output
		}
	}

	return results
}

func (a *Agent) handleConfirmation(confirm *tools.NeedsConfirmation, term *ui.Terminal, listener *ui.InterruptListener) string {
	switch confirm.Tool {
	case "write":
		term.PrintFilePreview(confirm.Path, confirm.Preview)
	case "edit":
		// Preview is the original content; we show the tool name and path
		fmt.Println()
	case "bash":
		fmt.Println()
	}

	// Pause raw mode so fmt.Scanln works for y/n input
	if listener != nil {
		listener.Pause()
	}

	approved := term.ConfirmAction(fmt.Sprintf("Apply %s to %s?", confirm.Tool, confirm.Path))

	// Resume raw mode after confirmation
	if listener != nil {
		listener.Resume()
	}

	if !approved {
		return "User denied the operation."
	}

	// Capture file state before modification for checkpointing
	if confirm.Tool == "write" || confirm.Tool == "edit" {
		a.captureFileBeforeModification(confirm.Path)
	}

	result, err := confirm.Execute()
	if err != nil {
		return fmt.Sprintf("Error: %s", err)
	}
	return result
}

// compactIfNeeded checks if conversation tokens exceed 80% of the context window
// and, if so, asks the LLM to produce a summary to replace the history.
func (a *Agent) compactIfNeeded(ctx context.Context, term *ui.Terminal) {
	if a.contextWindow <= 0 {
		return
	}

	threshold := int(float64(a.contextWindow) * (1 - ContextBuffer))
	current := a.lastTokensUsed
	if current == 0 {
		current = EstimateTotalTokens(a.messages)
	}
	if current <= threshold {
		return
	}

	term.PrintWarning("Context is large, compacting conversation...")
	a.doCompact(ctx, term)
}

// Compact forces an LLM-based compaction of the conversation history.
func (a *Agent) Compact(ctx context.Context, term *ui.Terminal) error {
	if len(a.messages) <= 1 {
		term.PrintWarning("Nothing to compact.")
		return nil
	}
	term.PrintWarning("Compacting conversation...")
	a.doCompact(ctx, term)
	return nil
}

// Clear resets the conversation history to just the system prompt.
func (a *Agent) Clear(term *ui.Terminal) {
	a.messages = []llm.Message{a.messages[0]}
	a.lastTokensUsed = 0
	term.PrintWarning("Conversation cleared.")
}

// doCompact performs the actual LLM-based compaction.
func (a *Agent) doCompact(ctx context.Context, term *ui.Terminal) {
	history := serializeHistory(a.messages)
	compactMessages := []llm.Message{
		llm.TextMessage("system", compactionPrompt()),
		llm.TextMessage("user", history),
	}

	resp, err := a.client.SendMessage(ctx, compactMessages, nil)
	if err != nil {
		term.PrintWarning("Compaction failed, continuing with full history.")
		return
	}

	summary := ""
	if resp.Message.Content != nil {
		summary = *resp.Message.Content
	}

	// Replace history: keep system prompt, add summary, preserve last user message
	systemMsg := a.messages[0]

	var lastUserMsg *llm.Message
	for i := len(a.messages) - 1; i >= 0; i-- {
		if a.messages[i].Role == "user" {
			lastUserMsg = &a.messages[i]
			break
		}
	}

	a.messages = []llm.Message{systemMsg}
	if summary != "" {
		a.messages = append(a.messages, llm.TextMessage("user",
			"[Conversation compacted] Here is a summary of our conversation so far:\n\n"+summary))
	}
	if lastUserMsg != nil {
		a.messages = append(a.messages, *lastUserMsg)
	}

	a.lastTokensUsed = 0
	term.PrintWarning("Context compacted successfully.")
}

// ContextStats holds context usage statistics.
type ContextStats struct {
	TotalTokens   int // actual from API, or estimated
	ContextWindow int
	Threshold     int
	MessageCount  int
	SystemTokens  int // system prompt estimate
	ToolDefTokens int // tool definitions estimate
	MessageTokens int // all user + assistant + tool result messages
	ActualTokens  int // from latest API response (0 if no call yet)
}

// ContextUsage returns current context usage statistics.
func (a *Agent) ContextUsage() ContextStats {
	stats := ContextStats{
		ContextWindow: a.contextWindow,
		Threshold:     int(float64(a.contextWindow) * (1 - ContextBuffer)),
		MessageCount:  len(a.messages),
		ActualTokens:  a.lastTokensUsed,
	}
	for _, msg := range a.messages {
		tokens := EstimateTokens(msg)
		if msg.Role == "system" {
			stats.SystemTokens += tokens
		} else {
			stats.MessageTokens += tokens
		}
	}
	stats.ToolDefTokens = EstimateToolDefTokens(a.tools.Definitions())
	stats.TotalTokens = stats.ActualTokens
	if stats.TotalTokens == 0 {
		stats.TotalTokens = stats.SystemTokens + stats.ToolDefTokens + stats.MessageTokens
	}
	return stats
}

func (a *Agent) systemPrompt() string {
	var sb strings.Builder
	sb.WriteString(`You are Pilot, an AI coding assistant running in the terminal.
You help users understand, explore, and modify codebases.

## Working Directory
`)
	sb.WriteString(a.workDir)
	sb.WriteString(`

## Available Tools

You have the following tools available:

- **glob**: Find files by glob pattern (supports ** for recursive). Use this first to understand project structure.
- **grep**: Search file contents with RE2 regex. Note: RE2 does not support lookaheads or lookbehinds.
- **ls**: List directory contents with file sizes.
- **read**: Read file contents with line numbers. Use start_line/end_line for large files.
- **write**: Create new files. User confirmation required.
- **edit**: Edit files by exact string replacement. The old_str must match exactly once. User confirmation required.
- **bash**: Execute shell commands (builds, tests, git, etc.). User confirmation required. Timeout: 30s default, 120s max.

## Memory

Project knowledge is stored in MEMORY.md at the project root. This file is human-editable and version-controlled.
To persist important context (conventions, architecture decisions, gotchas), use the **edit** tool to update MEMORY.md.
The current contents of MEMORY.md (if any) are shown below under "Project Memory".

## Guidelines

1. **Explore before editing**: Always read and understand relevant files before making changes.
2. **Minimal edits**: Make the smallest change that achieves the goal. Don't refactor surrounding code.
3. **Explain your reasoning**: Tell the user what you're doing and why before taking action.
4. **Use the right tool**: Use glob to find files, grep to search content, read to understand code, then edit/write to make changes.
5. **Be precise with edits**: Include enough context in old_str to uniquely identify the location. If an edit fails due to multiple matches, include more surrounding lines.
6. **One step at a time**: Don't try to do everything in one response. Break complex tasks into steps.
`)

	// Inject project memory if available
	memoryPath := filepath.Join(a.workDir, "MEMORY.md")
	if data, err := os.ReadFile(memoryPath); err == nil && len(data) > 0 {
		sb.WriteString("\n## Project Memory (MEMORY.md)\n\n")
		sb.WriteString(string(data))
		sb.WriteString("\n")
	}

	return sb.String()
}
