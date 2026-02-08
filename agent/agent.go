package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"strings"

	"github.com/kaiho/pilot/llm"
	"github.com/kaiho/pilot/tools"
	"github.com/kaiho/pilot/ui"
)

const MaxIterationsPerTurn = 50

// Agent orchestrates the LLM conversation and tool execution loop.
type Agent struct {
	client   llm.LLMClient
	tools    *tools.Registry
	messages []llm.Message
	workDir  string
}

// New creates a new Agent with the system prompt initialized.
func New(client llm.LLMClient, registry *tools.Registry, workDir string) *Agent {
	a := &Agent{
		client:  client,
		tools:   registry,
		workDir: workDir,
	}
	a.messages = []llm.Message{
		llm.TextMessage("system", a.systemPrompt()),
	}
	return a
}

// Run processes a user message through the agent loop.
func (a *Agent) Run(ctx context.Context, userMessage string, term *ui.Terminal) error {
	a.messages = append(a.messages, llm.TextMessage("user", userMessage))

	for iteration := 0; iteration < MaxIterationsPerTurn; iteration++ {
		term.PrintSpinner()

		events, err := a.client.StreamMessage(ctx, a.messages, a.tools.Definitions())
		if err != nil {
			term.ClearSpinner()
			return fmt.Errorf("LLM request failed: %w", err)
		}

		term.ClearSpinner()

		resp, err := llm.AccumulateStream(events, func(text string) {
			term.PrintAssistant(text)
		})
		if err != nil {
			return fmt.Errorf("stream error: %w", err)
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

		for _, tc := range resp.Message.ToolCalls {
			if !json.Valid([]byte(tc.Function.Arguments)) {
				errMsg := fmt.Sprintf("Error: invalid JSON in tool arguments: %s", tc.Function.Arguments)
				a.messages = append(a.messages, llm.ToolResultMessage(tc.ID, errMsg))
				term.PrintToolCall(tc.Function.Name, "invalid JSON")
				continue
			}

			term.PrintToolCall(tc.Function.Name, tc.Function.Arguments)

			input := json.RawMessage(tc.Function.Arguments)
			output, toolErr := a.tools.Execute(ctx, tc.Function.Name, input)

			if toolErr != nil {
				// Check if it's a confirmation request
				if confirm, ok := toolErr.(*tools.NeedsConfirmation); ok {
					output = a.handleConfirmation(confirm, term)
				} else {
					output = fmt.Sprintf("Error: %s", toolErr)
				}
			}

			term.PrintToolResult(output)
			a.messages = append(a.messages, llm.ToolResultMessage(tc.ID, output))
		}
	}

	return fmt.Errorf("agent loop exceeded maximum iterations (%d)", MaxIterationsPerTurn)
}

func (a *Agent) handleConfirmation(confirm *tools.NeedsConfirmation, term *ui.Terminal) string {
	switch confirm.Tool {
	case "write":
		term.PrintFilePreview(confirm.Path, confirm.Preview)
	case "edit":
		// Preview is the original content; we show the tool name and path
		fmt.Println()
	}

	if !term.ConfirmAction(fmt.Sprintf("Apply %s to %s?", confirm.Tool, confirm.Path)) {
		return "User denied the operation."
	}

	result, err := confirm.Execute()
	if err != nil {
		return fmt.Sprintf("Error: %s", err)
	}
	return result
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

## Guidelines

1. **Explore before editing**: Always read and understand relevant files before making changes.
2. **Minimal edits**: Make the smallest change that achieves the goal. Don't refactor surrounding code.
3. **Explain your reasoning**: Tell the user what you're doing and why before taking action.
4. **Use the right tool**: Use glob to find files, grep to search content, read to understand code, then edit/write to make changes.
5. **Be precise with edits**: Include enough context in old_str to uniquely identify the location. If an edit fails due to multiple matches, include more surrounding lines.
6. **One step at a time**: Don't try to do everything in one response. Break complex tasks into steps.
`)
	return sb.String()
}
