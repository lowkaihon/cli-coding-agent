// Package ui provides terminal output formatting, colorized diffs, user prompts,
// keyboard interrupt handling, and all user-facing display logic.
package ui

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/lowkaihon/cli-coding-agent/llm"
)

// ANSI color codes
const (
	Reset   = "\033[0m"
	Bold    = "\033[1m"
	Dim     = "\033[2m"
	Red     = "\033[31m"
	Green   = "\033[32m"
	Yellow  = "\033[33m"
	Blue    = "\033[34m"
	Magenta = "\033[35m"
	Cyan    = "\033[36m"
	Gray    = "\033[90m"
	White   = "\033[97m"
)

// Terminal handles all user-facing output.
type Terminal struct {
	color bool
}

// NewTerminal creates a terminal with color detection.
func NewTerminal() *Terminal {
	return &Terminal{
		color: isTerminal(),
	}
}

func isTerminal() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

func (t *Terminal) c(code, text string) string {
	if !t.color {
		return text
	}
	return code + text + Reset
}

// PrintBanner prints the startup banner.
func (t *Terminal) PrintBanner(model, workDir, version string) {
	banner := `
    ____  _ __      __ 
   / __ \(_) /___  / /_
  / /_/ / / / __ \/ __/
 / ____/ / / /_/ / /_  
/_/   /_/_/\____/\__/  
`
	fmt.Print(t.c(Bold+Cyan, banner))
	
	versionStr := ""
	if version != "" && version != "dev" {
		versionStr = " v" + version
	}
	
	fmt.Println(t.c(Bold+White, "AI Coding Agent") + t.c(Gray, versionStr))
	fmt.Println()
	fmt.Println(t.c(Gray, "  Model:   ") + t.c(Cyan, model))
	fmt.Println(t.c(Gray, "  Dir:     ") + t.c(White, workDir))
	fmt.Println()
	fmt.Println(t.c(Gray, "  Type ") + t.c(Cyan, "/help") + t.c(Gray, " for commands"))
	fmt.Println()
}

// Prompt returns the formatted prompt string.
func (t *Terminal) Prompt() string {
	return t.c(Bold+Blue, "> ")
}

// PrintPrompt prints the input prompt.
func (t *Terminal) PrintPrompt() {
	fmt.Print(t.Prompt())
}

// ReadLine reads a line of input using standard buffered I/O.
// The OS terminal handles line editing (arrow keys, Home/End, backspace).
func (t *Terminal) ReadLine(prompt string) (string, error) {
	fmt.Print(prompt)
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// PrintAssistant prints assistant text.
func (t *Terminal) PrintAssistant(text string) {
	fmt.Print(text)
}

// PrintAssistantDone signals end of assistant output.
func (t *Terminal) PrintAssistantDone() {
	fmt.Println()
	fmt.Println()
}

// PrintToolCall prints a tool invocation.
func (t *Terminal) PrintToolCall(name string, args string) {
	fmt.Println(t.c(Yellow, fmt.Sprintf("  ↳ %s", name)) + t.c(Gray, fmt.Sprintf(" %s", truncate(args, 100))))
}

// PrintToolResult prints a tool's result (truncated).
func (t *Terminal) PrintToolResult(result string) {
	lines := strings.Split(result, "\n")
	if len(lines) > 5 {
		for _, line := range lines[:5] {
			fmt.Println(t.c(Gray, "    "+truncate(line, 120)))
		}
		fmt.Println(t.c(Gray, fmt.Sprintf("    ... (%d more lines)", len(lines)-5)))
	} else {
		for _, line := range lines {
			fmt.Println(t.c(Gray, "    "+truncate(line, 120)))
		}
	}
}

// PrintSubAgentToolCall prints a sub-agent's tool invocation with deeper indentation.
func (t *Terminal) PrintSubAgentToolCall(name string, args string) {
	fmt.Println(t.c(Dim+Yellow, fmt.Sprintf("      ↳ %s", name)) + t.c(Gray, fmt.Sprintf(" %s", truncate(args, 80))))
}

// PrintSubAgentStatus prints a sub-agent status line.
func (t *Terminal) PrintSubAgentStatus(msg string) {
	fmt.Println(t.c(Gray, "      "+msg))
}

// PrintError prints an error message.
func (t *Terminal) PrintError(err error) {
	fmt.Fprintln(os.Stderr, t.c(Red, "Error: "+err.Error()))
	fmt.Println()
}

// PrintWarning prints a warning message.
func (t *Terminal) PrintWarning(msg string) {
	fmt.Println(t.c(Yellow, "Warning: "+msg))
}

// PrintSpinner prints a thinking indicator.
func (t *Terminal) PrintSpinner() {
	fmt.Print(t.c(Gray, "  thinking..."))
}

// ClearSpinner clears the thinking indicator.
func (t *Terminal) ClearSpinner() {
	fmt.Print("\r\033[K")
}

// PrintHelp prints all available slash commands.
func (t *Terminal) PrintHelp() {
	fmt.Println(t.c(Bold, "Commands"))
	fmt.Println(t.c(Cyan, "  /help   ") + " Show this help message")
	fmt.Println(t.c(Cyan, "  /model  ") + " Switch LLM model")
	fmt.Println(t.c(Cyan, "  /compact") + " Compact conversation (LLM summarizes history)")
	fmt.Println(t.c(Cyan, "  /clear  ") + " Clear conversation history")
	fmt.Println(t.c(Cyan, "  /context") + " Show context window usage")
	fmt.Println(t.c(Cyan, "  /tasks  ") + " Show current task list")
	fmt.Println(t.c(Cyan, "  /resume ") + " Resume a previous session")
	fmt.Println(t.c(Cyan, "  /rewind ") + " Rewind to a previous checkpoint")
	fmt.Println(t.c(Cyan, "  /quit   ") + " Exit Pilot")
	fmt.Println()
}

// ModelOption represents a model choice in the /model menu.
type ModelOption struct {
	Label   string
	Current bool
}

// PrintModelMenu prints the numbered model selection menu.
func (t *Terminal) PrintModelMenu(options []ModelOption) {
	fmt.Println(t.c(Bold, "Select a model:"))
	for i, opt := range options {
		marker := "  "
		if opt.Current {
			marker = t.c(Green, "→ ")
		}
		fmt.Printf("%s%s %s\n", marker, t.c(Cyan, fmt.Sprintf("[%d]", i+1)), opt.Label)
	}
	fmt.Printf("  %s %s\n", t.c(Cyan, "[0]"), "Enter a custom model name")
	fmt.Println(t.c(Gray, "  Ctrl+C to cancel"))
	fmt.Println()
}

// PrintModelSwitch prints a model switch confirmation.
func (t *Terminal) PrintModelSwitch(model string) {
	fmt.Println(t.c(Green, fmt.Sprintf("Switched to %s", model)))
	fmt.Println()
}

// PrintContextUsage prints context usage statistics.
func (t *Terminal) PrintContextUsage(total, window, threshold, msgCount, systemTokens, toolDefTokens, messageTokens, actualTokens int) {
	fmt.Println(t.c(Bold, "Context Usage"))
	if actualTokens > 0 {
		pct := 0.0
		if window > 0 {
			pct = float64(actualTokens) / float64(window) * 100
		}
		fmt.Printf("  Tokens: %s / %s (%.1f%%)\n", formatNum(actualTokens), formatNum(window), pct)
		fmt.Printf("  Compact at: %s (80%%)\n", formatNum(threshold))
		fmt.Printf("  Messages: %d\n", msgCount)
	} else {
		pct := 0.0
		if window > 0 {
			pct = float64(total) / float64(window) * 100
		}
		fmt.Printf("  Tokens: ~%s / %s (~%.1f%%)\n", formatNum(total), formatNum(window), pct)
		fmt.Printf("  Compact at: %s (80%%)\n", formatNum(threshold))
		fmt.Println()
		fmt.Printf("    %s\n", t.c(Bold, "Breakdown (estimated):"))
		fmt.Printf("      %s  ~%s tokens\n", t.c(Gray, "System prompt   "), formatNum(systemTokens))
		fmt.Printf("      %s  ~%s tokens\n", t.c(Yellow, "Tool definitions"), formatNum(toolDefTokens))
		fmt.Printf("      %s  ~%s tokens\n", t.c(Cyan, fmt.Sprintf("Messages (%d)   ", msgCount)), formatNum(messageTokens))
	}
	fmt.Println()
}

func formatNum(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	return fmt.Sprintf("%d,%03d", n/1000, n%1000)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

// Interrupter controls an escape key listener during agent execution.
type Interrupter interface {
	Stop()
	Pause()
	Resume()
}

var _ Interrupter = (*InterruptListener)(nil)

// InterruptListener watches for Esc key presses during agent execution
// and cancels a derived context when detected.
type InterruptListener struct {
	rawMode *RawMode
	cancel  context.CancelFunc
	stopCh  chan struct{} // closed to signal readLoop to exit
	done    chan struct{} // closed when readLoop has exited
	mu      sync.Mutex
	active  bool
}

// StartEscapeListener creates a derived context that cancels when Esc is pressed.
// Returns the derived context, the listener (for Pause/Resume/Stop), and any error.
// If raw mode cannot be initialized (e.g., no TTY), returns the original context
// and a nil listener.
func (t *Terminal) StartEscapeListener(parent context.Context) (context.Context, Interrupter, error) {
	rm, err := NewRawMode()
	if err != nil {
		return parent, nil, err
	}

	if err := rm.Enable(); err != nil {
		return parent, nil, err
	}

	ctx, cancel := context.WithCancel(parent)
	il := &InterruptListener{
		rawMode: rm,
		cancel:  cancel,
		stopCh:  make(chan struct{}),
		done:    make(chan struct{}),
		active:  true,
	}

	go il.readLoop()

	return ctx, il, nil
}

func (il *InterruptListener) readLoop() {
	defer close(il.done)
	for {
		ch, err := il.rawMode.ReadKeyContext(il.stopCh)
		if err != nil {
			return // ErrStopped or read error
		}

		il.mu.Lock()
		active := il.active
		il.mu.Unlock()

		if !active {
			continue
		}

		if ch == 0x1B {
			il.cancel()
			return
		}
	}
}

// Stop shuts down the listener and restores terminal mode.
func (il *InterruptListener) Stop() {
	il.mu.Lock()
	il.active = false
	il.mu.Unlock()

	// Restore terminal mode first so Ctrl+C works even if goroutine is slow to exit
	il.rawMode.Disable()

	// Signal the readLoop to stop, then wait for it
	close(il.stopCh)
	<-il.done

	il.cancel()
}

// Pause temporarily disables raw mode (e.g., for confirmation prompts).
func (il *InterruptListener) Pause() {
	il.mu.Lock()
	il.active = false
	il.mu.Unlock()
	il.rawMode.Disable()
}

// Resume re-enables raw mode after a Pause.
func (il *InterruptListener) Resume() {
	il.rawMode.Enable()
	il.mu.Lock()
	il.active = true
	il.mu.Unlock()
}

// SessionListItem represents a session entry for display.
type SessionListItem struct {
	ID       string
	Updated  time.Time
	Preview  string
	MsgCount int
}

// PrintSessionList displays a numbered list of recent sessions.
func (t *Terminal) PrintSessionList(items []SessionListItem) {
	fmt.Println(t.c(Bold, "Recent sessions:"))
	for i, item := range items {
		age := formatAge(item.Updated)
		preview := item.Preview
		if len(preview) > 60 {
			preview = preview[:60] + "..."
		}
		fmt.Printf("  %s  %s  %s  %s\n",
			t.c(Cyan, fmt.Sprintf("[%d]", i+1)),
			t.c(Gray, fmt.Sprintf("%-8s", age)),
			t.c(White, fmt.Sprintf("%q", preview)),
			t.c(Gray, fmt.Sprintf("(%d messages)", item.MsgCount)),
		)
	}
	fmt.Println(t.c(Gray, "  Ctrl+C to cancel"))
	fmt.Println()
}

// PrintSessionResumed prints a confirmation after resuming a session.
func (t *Terminal) PrintSessionResumed(msgCount int, preview string) {
	if len(preview) > 60 {
		preview = preview[:60] + "..."
	}
	fmt.Println(t.c(Green, fmt.Sprintf("Resumed session: %q (%d messages)", preview, msgCount)))
	fmt.Println()
}

func formatAge(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// CheckpointListItem represents a checkpoint entry for display.
type CheckpointListItem struct {
	Turn      int
	Timestamp time.Time
	Preview   string
}

// PrintCheckpointList displays a numbered list of checkpoints.
func (t *Terminal) PrintCheckpointList(items []CheckpointListItem) {
	fmt.Println(t.c(Bold, "Checkpoints:"))
	for _, item := range items {
		age := formatAge(item.Timestamp)
		preview := item.Preview
		if len(preview) > 60 {
			preview = preview[:60] + "..."
		}
		fmt.Printf("  %s  %s  %s\n",
			t.c(Cyan, fmt.Sprintf("[%d]", item.Turn)),
			t.c(Gray, fmt.Sprintf("%-8s", age)),
			t.c(White, fmt.Sprintf("%q", preview)),
		)
	}
	fmt.Println(t.c(Gray, "  Ctrl+C to cancel"))
	fmt.Println()
}

// PrintRewindActions displays the rewind action menu.
func (t *Terminal) PrintRewindActions() {
	fmt.Println(t.c(Bold, "Choose action:"))
	fmt.Printf("  %s  Restore code and conversation\n", t.c(Cyan, "[1]"))
	fmt.Printf("  %s  Restore conversation only\n", t.c(Cyan, "[2]"))
	fmt.Printf("  %s  Restore code only\n", t.c(Cyan, "[3]"))
	fmt.Printf("  %s  Summarize from here\n", t.c(Cyan, "[4]"))
	fmt.Printf("  %s  Never mind\n", t.c(Cyan, "[5]"))
	fmt.Println()
}

// PrintProviderPrompt prints a provider selection prompt for custom model entry.
func (t *Terminal) PrintProviderPrompt(current string) {
	fmt.Printf("  %s openai  %s anthropic  (current: %s)\n",
		t.c(Cyan, "[1]"), t.c(Cyan, "[2]"), current)
}

// PrintConversationHistory replays a stored conversation to the terminal.
func (t *Terminal) PrintConversationHistory(messages []llm.Message) {
	fmt.Println(t.c(Gray, "--- Conversation history ---"))
	fmt.Println()
	for _, msg := range messages {
		switch msg.Role {
		case "system":
			continue
		case "user":
			if msg.ToolCallID != "" {
				continue // skip tool-result-in-user-message (Anthropic format)
			}
			if msg.Content != nil && *msg.Content != "" {
				fmt.Println(t.c(Bold+Blue, "> ") + *msg.Content)
				fmt.Println()
			}
		case "assistant":
			if msg.Content != nil && *msg.Content != "" {
				t.PrintAssistant(*msg.Content)
				t.PrintAssistantDone()
			}
			for _, tc := range msg.ToolCalls {
				t.PrintToolCall(tc.Function.Name, tc.Function.Arguments)
			}
		case "tool":
			if msg.Content != nil {
				t.PrintToolResult(*msg.Content)
			}
		}
	}
	fmt.Println(t.c(Gray, "--- End of history ---"))
	fmt.Println()
}

// TaskListItem represents a task entry for display.
type TaskListItem struct {
	ID          int
	Content     string
	Description string
	Status      string
	ActiveForm  string
}

// PrintTaskList displays the current task list grouped by status.
func (t *Terminal) PrintTaskList(tasks []TaskListItem) {
	fmt.Println(t.c(Bold, "Tasks"))

	pending, inProgress, completed := 0, 0, 0
	for _, task := range tasks {
		var marker string
		switch task.Status {
		case "in_progress":
			inProgress++
			marker = t.c(Yellow, "● ")
		case "completed":
			completed++
			marker = t.c(Green, "✓ ")
		default:
			pending++
			marker = t.c(Cyan, "○ ")
		}
		fmt.Printf("  %s%s %s\n", marker, t.c(Gray, fmt.Sprintf("[%d]", task.ID)), task.Content)
		if task.Description != "" {
			desc := task.Description
			if len(desc) > 200 {
				desc = desc[:197] + "..."
			}
			fmt.Printf("       %s\n", t.c(Gray, desc))
		}
	}
	fmt.Println()
	fmt.Printf("  %d tasks (%d pending, %d in progress, %d completed)\n",
		len(tasks), pending, inProgress, completed)
	fmt.Println()
}

// PrintTaskPlan displays the proposed task plan before confirmation.
func (t *Terminal) PrintTaskPlan(plan string) {
	fmt.Println()
	fmt.Println(t.c(Bold, "Proposed Task Plan"))
	fmt.Println(plan)
}

// PrintRewindComplete prints a confirmation message after a rewind operation.
func (t *Terminal) PrintRewindComplete(action string) {
	fmt.Println(t.c(Green, fmt.Sprintf("Rewind complete: %s", action)))
	fmt.Println()
}
