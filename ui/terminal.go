package ui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
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
func (t *Terminal) PrintBanner(model, workDir string) {
	fmt.Println(t.c(Bold+Cyan, "pilot") + t.c(Gray, " — AI coding agent"))
	fmt.Println(t.c(Gray, fmt.Sprintf("Model: %s | Dir: %s", model, workDir)))
	fmt.Println(t.c(Gray, "Type '/help' for commands."))
	fmt.Println()
}

// PrintPrompt prints the input prompt.
func (t *Terminal) PrintPrompt() {
	fmt.Print(t.c(Bold+Blue, "> "))
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
	fmt.Println()
}

// PrintModelSwitch prints a model switch confirmation.
func (t *Terminal) PrintModelSwitch(model string) {
	fmt.Println(t.c(Green, fmt.Sprintf("Switched to %s", model)))
	fmt.Println()
}

// PrintContextUsage prints context usage statistics.
func (t *Terminal) PrintContextUsage(total, window, threshold, msgCount, systemTokens, toolDefTokens, messageTokens, actualTokens int) {
	pct := 0.0
	if window > 0 {
		pct = float64(total) / float64(window) * 100
	}
	fmt.Println(t.c(Bold, "Context Usage"))
	fmt.Printf("  Tokens: %s / %s (%.1f%%)\n", formatNum(total), formatNum(window), pct)
	fmt.Printf("  Compact at: %s (80%%)\n", formatNum(threshold))
	fmt.Println()
	fmt.Printf("    %s  ~%s tokens\n", t.c(Gray, "System prompt   "), formatNum(systemTokens))
	fmt.Printf("    %s  ~%s tokens\n", t.c(Yellow, "Tool definitions"), formatNum(toolDefTokens))
	fmt.Printf("    %s  ~%s tokens\n", t.c(Cyan, fmt.Sprintf("Messages (%d)   ", msgCount)), formatNum(messageTokens))
	fmt.Println()
	if actualTokens > 0 {
		fmt.Printf("    %s  %s tokens (from API)\n", t.c(Green, "Actual usage    "), formatNum(actualTokens))
	} else {
		fmt.Printf("    %s\n", t.c(Gray, "No API usage data yet"))
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
func (t *Terminal) StartEscapeListener(parent context.Context) (context.Context, *InterruptListener, error) {
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
