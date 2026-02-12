// Package agent implements the agentic loop that orchestrates LLM conversations
// with tool execution, context management, session persistence, and checkpointing.
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

// MaxIterationsPerTurn limits the number of LLM round-trips per user message
// to prevent runaway tool-use loops.
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
	term           UI                        // stored for sub-agent visibility
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

	// Wire the explore sub-agent callback into the tool registry
	registry.SetExploreFunc(a.runExplore)

	return a
}

// SetClient swaps the LLM client and context window (e.g., after /model).
func (a *Agent) SetClient(client llm.LLMClient, contextWindow int) {
	a.client = client
	a.contextWindow = contextWindow
}

// Run processes a user message through the agent loop.
func (a *Agent) Run(ctx context.Context, userMessage string, term UI) error {
	a.term = term
	a.messages = append(a.messages, llm.TextMessage("user", userMessage))

	// Start escape listener for Esc key cancellation
	opCtx, listener, escErr := term.StartEscapeListener(ctx)
	if escErr != nil {
		// No TTY or raw mode unavailable — fall back to parent context
		opCtx = ctx
		listener = noopInterrupter{}
	}
	defer listener.Stop()

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

		spinnerCleared := false
		clearSpinner := func() {
			if !spinnerCleared {
				term.ClearSpinner()
				spinnerCleared = true
			}
		}

		resp, err := llm.AccumulateStream(events, func(text string) {
			clearSpinner()
			term.PrintAssistant(text)
		})
		clearSpinner() // ensure cleared after stream ends (e.g. tool-only responses)
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
func (a *Agent) executeToolCalls(ctx context.Context, calls []llm.ToolCall, term UI, listener ui.Interrupter) []toolResult {
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

func (a *Agent) handleConfirmation(confirm *tools.NeedsConfirmation, term UI, listener ui.Interrupter) string {
	switch confirm.Tool {
	case "write":
		if confirm.Preview == "" {
			term.PrintFilePreview(confirm.Path, confirm.NewContent)
		} else {
			term.PrintDiff(confirm.Path, confirm.Preview, confirm.NewContent)
		}
	case "edit":
		term.PrintDiff(confirm.Path, confirm.Preview, confirm.NewContent)
	case "bash":
		fmt.Println()
	}

	// Pause raw mode so fmt.Scanln works for y/n input
	listener.Pause()
	approved := term.ConfirmAction(fmt.Sprintf("Apply %s to %s?", confirm.Tool, confirm.Path))
	listener.Resume()

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
func (a *Agent) compactIfNeeded(ctx context.Context, term UI) {
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
func (a *Agent) Compact(ctx context.Context, term UI) error {
	if len(a.messages) <= 1 {
		term.PrintWarning("Nothing to compact.")
		return nil
	}
	term.PrintWarning("Compacting conversation...")
	a.doCompact(ctx, term)
	return nil
}

// Clear resets the conversation history to just the system prompt.
func (a *Agent) Clear(term UI) {
	a.messages = []llm.Message{a.messages[0]}
	a.checkpoints = nil
	a.lastTokensUsed = 0
	term.PrintWarning("Conversation cleared.")
}

// doCompact performs the actual LLM-based compaction.
func (a *Agent) doCompact(ctx context.Context, term UI) {
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

// MaxExploreIterations is the iteration limit for the explore sub-agent.
const MaxExploreIterations = 30

// runExplore spawns a child agent with read-only tools to research the codebase.
// It uses non-streaming SendMessage to avoid interleaved terminal output.
func (a *Agent) runExplore(ctx context.Context, task string) (string, error) {
	roRegistry := tools.NewReadOnlyRegistry(a.workDir)
	toolDefs := roRegistry.Definitions()

	messages := []llm.Message{
		llm.TextMessage("system", exploreSystemPrompt(a.workDir)),
		llm.TextMessage("user", task),
	}

	totalSteps := 0

	for iteration := 0; iteration < MaxExploreIterations; iteration++ {
		resp, err := a.client.SendMessage(ctx, messages, toolDefs)
		if err != nil {
			return "", fmt.Errorf("explore sub-agent LLM error: %w", err)
		}

		messages = append(messages, resp.Message)

		// If no tool calls, the sub-agent is done — return its final text
		if len(resp.Message.ToolCalls) == 0 {
			if a.term != nil {
				a.term.PrintSubAgentStatus(fmt.Sprintf("Explore complete (%d tool calls)", totalSteps))
			}
			return resp.Message.ContentString(), nil
		}

		// Print all tool calls, then execute in parallel
		for _, tc := range resp.Message.ToolCalls {
			totalSteps++
			if a.term != nil {
				a.term.PrintSubAgentToolCall(tc.Function.Name, tc.Function.Arguments)
			}
		}

		outputs := make([]string, len(resp.Message.ToolCalls))
		var wg sync.WaitGroup
		for i, tc := range resp.Message.ToolCalls {
			wg.Add(1)
			go func(idx int, tc llm.ToolCall) {
				defer wg.Done()
				input := json.RawMessage(tc.Function.Arguments)
				output, toolErr := roRegistry.Execute(ctx, tc.Function.Name, input)
				if toolErr != nil {
					output = fmt.Sprintf("Error: %s", toolErr)
				}
				outputs[idx] = output
			}(i, tc)
		}
		wg.Wait()

		for i, tc := range resp.Message.ToolCalls {
			messages = append(messages, llm.ToolResultMessage(tc.ID, outputs[i]))
		}
	}

	if a.term != nil {
		a.term.PrintSubAgentStatus(fmt.Sprintf("Explore reached max iterations (%d tool calls)", totalSteps))
	}
	return "Explore sub-agent reached maximum iterations without completing.", nil
}

func exploreSystemPrompt(workDir string) string {
	return fmt.Sprintf(`You are an exploration sub-agent. Your job is to thoroughly research the codebase to answer the given question.

Working directory: %s

This is a READ-ONLY exploration task. You only have access to: glob, grep, ls, read.

Guidelines:
- Use glob for broad file pattern matching (prefer over repeated ls calls)
- Use grep for searching file contents with regex
- Use read when you know the specific file path
- Use ls only when you need to see directory structure

You are meant to be a fast agent. To achieve this:
- Make efficient use of your tools — be smart about how you search
- Wherever possible, call multiple tools in parallel. When you find several files to read, read them ALL in one response instead of one at a time
- Start broad (glob, grep) then narrow down to specific reads

When you have gathered enough information, provide a clear, structured summary of your findings. Do not ask follow-up questions — just research and report.`, workDir)
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

	// Section 1: Identity
	sb.WriteString(`You are Pilot, an AI coding assistant running in the terminal. You help users with software engineering tasks. Use the instructions below and the tools available to you to assist the user.

IMPORTANT: Assist with authorized security testing, defensive security, CTF challenges, and educational contexts. Refuse requests for destructive techniques, DoS attacks, mass targeting, supply chain compromise, or detection evasion for malicious purposes.

# Doing tasks
The user will primarily request you to perform software engineering tasks. These include solving bugs, adding new functionality, refactoring code, explaining code, and more.
- NEVER propose changes to code you haven't read. If a user asks about or wants you to modify a file, read it first. Understand existing code before suggesting modifications.
- Be careful not to introduce security vulnerabilities such as command injection, XSS, SQL injection, and other OWASP top 10 vulnerabilities. If you notice that you wrote insecure code, immediately fix it.
- Avoid over-engineering. Only make changes that are directly requested or clearly necessary. Keep solutions simple and focused.
  - Don't add features, refactor code, or make "improvements" beyond what was asked. A bug fix doesn't need surrounding code cleaned up. A simple feature doesn't need extra configurability. Don't add docstrings, comments, or type annotations to code you didn't change. Only add comments where the logic isn't self-evident.
  - Don't add error handling, fallbacks, or validation for scenarios that can't happen. Trust internal code and framework guarantees. Only validate at system boundaries (user input, external APIs). Don't use feature flags or backwards-compatibility shims when you can just change the code.
  - Don't create helpers, utilities, or abstractions for one-time operations. Don't design for hypothetical future requirements. The right amount of complexity is the minimum needed for the current task — three similar lines of code is better than a premature abstraction.
- Avoid backwards-compatibility hacks like renaming unused ` + "`_vars`" + `, re-exporting types, adding ` + "`// removed`" + ` comments for removed code, etc. If something is unused, delete it completely.

# Executing actions with care

Carefully consider the reversibility and blast radius of actions. Generally you can freely take local, reversible actions like editing files or running tests. But for actions that are hard to reverse, affect shared systems beyond your local environment, or could otherwise be risky or destructive, check with the user before proceeding. The cost of pausing to confirm is low, while the cost of an unwanted action (lost work, unintended messages sent, deleted branches) can be very high.

Examples of risky actions that warrant user confirmation:
- Destructive operations: deleting files/branches, dropping database tables, killing processes, rm -rf, overwriting uncommitted changes
- Hard-to-reverse operations: force-pushing, git reset --hard, amending published commits, removing or downgrading packages/dependencies
- Actions visible to others or that affect shared state: pushing code, creating/closing/commenting on PRs or issues, sending messages, modifying shared infrastructure

When you encounter an obstacle, do not use destructive actions as a shortcut. Try to identify root causes and fix underlying issues rather than bypassing safety checks (e.g. --no-verify). If you discover unexpected state like unfamiliar files, branches, or configuration, investigate before deleting or overwriting, as it may represent the user's in-progress work. When in doubt, ask before acting.

# Tool usage policy
- You can call multiple tools in a single response. If you intend to call multiple tools and there are no dependencies between them, make all independent tool calls in parallel. However, if some tool calls depend on previous calls, do NOT call these tools in parallel — call them sequentially instead.
- Use dedicated tools instead of bash for file operations: read for reading files (not cat/head/tail), edit for editing (not sed/awk), write for creating files (not echo/cat with heredoc). Reserve bash exclusively for system commands and terminal operations that require shell execution.
- NEVER use bash echo or other command-line tools to communicate with the user. Output all communication directly in your response text.
- Do not create files unless they're absolutely necessary for achieving your goal. ALWAYS prefer editing an existing file to creating a new one, including markdown files.
- For broad codebase exploration questions (project structure, how a feature works, finding patterns across files), use the explore tool to delegate the research to a focused sub-agent. This keeps the main conversation focused and avoids cluttering context with intermediate search results.

# Tone and style
- Only use emojis if the user explicitly requests it.
- Your output will be displayed on a command line interface. Responses should be short and concise. You can use Github-flavored markdown for formatting.
- Do not use a colon before tool calls. Text like "Let me read the file:" followed by a tool call should just be "Let me read the file." with a period.
- Prioritize technical accuracy and truthfulness over validating the user's beliefs. Provide direct, objective technical info without unnecessary praise or emotional validation. Disagree when necessary — objective guidance and respectful correction are more valuable than false agreement.
- Never give time estimates or predictions for how long tasks will take. Focus on what needs to be done, not how long it might take.

# Git workflow
When asked to create git commits:
- Only commit when the user explicitly requests it
- NEVER force-push, reset --hard, use --no-verify, or amend unless the user explicitly asks
- Prefer staging specific files over ` + "`git add -A`" + ` or ` + "`git add .`" + `
- NEVER use interactive flags (` + "`-i`" + `) since they require interactive input
- Use HEREDOC for multi-line commit messages
When asked to create pull requests:
- Use ` + "`gh pr create`" + ` with a clear title and structured body
- Keep PR titles short (under 70 characters)

`)

	// Section: Working directory
	sb.WriteString("# Environment\n\nWorking directory: ")
	sb.WriteString(a.workDir)
	sb.WriteString("\n\n")

	// Section: Memory
	sb.WriteString(`# Memory

Project knowledge is stored in MEMORY.md at the project root. This file is human-editable and version-controlled.
To persist important context (conventions, architecture decisions, gotchas), use the edit tool to update MEMORY.md.
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
