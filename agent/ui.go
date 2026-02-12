package agent

import (
	"context"

	"github.com/lowkaihon/cli-coding-agent/ui"
)

// UI abstracts the terminal output and interaction methods used by the agent.
// This interface is satisfied by *ui.Terminal and enables testing with mock implementations.
type UI interface {
	StartEscapeListener(parent context.Context) (context.Context, ui.Interrupter, error)
	PrintSpinner()
	ClearSpinner()
	PrintAssistant(text string)
	PrintAssistantDone()
	PrintWarning(msg string)
	PrintToolCall(name, args string)
	PrintToolResult(result string)
	PrintSubAgentToolCall(name, args string)
	PrintSubAgentStatus(msg string)
	PrintDiff(path, oldContent, newContent string)
	PrintFilePreview(path, content string)
	ConfirmAction(prompt string) bool
}

// noopInterrupter is a no-op implementation used when escape listening is unavailable.
type noopInterrupter struct{}

func (noopInterrupter) Stop()   {}
func (noopInterrupter) Pause()  {}
func (noopInterrupter) Resume() {}
