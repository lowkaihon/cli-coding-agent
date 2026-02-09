package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/lowkaihon/cli-coding-agent/agent"
	"github.com/lowkaihon/cli-coding-agent/config"
	"github.com/lowkaihon/cli-coding-agent/llm"
	"github.com/lowkaihon/cli-coding-agent/tools"
	"github.com/lowkaihon/cli-coding-agent/ui"
)

func main() {
	model := flag.String("model", "", "Model name (default depends on provider)")
	provider := flag.String("provider", "", "LLM provider: openai (default) or anthropic")
	flag.Parse()

	rootCtx := context.Background()

	// Set up signal handling: Ctrl+C cancels current operation first, exits on double-tap
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	cfg, err := config.Load(*provider)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		os.Exit(1)
	}

	if *model != "" {
		cfg.Model = *model
	}

	client := newClient(cfg.Provider, cfg.APIKey, cfg.Model, cfg.MaxTokens, cfg.BaseURL)
	currentModel := cfg.Model
	currentProvider := cfg.Provider

	workDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting working directory: %s\n", err)
		os.Exit(1)
	}

	registry := tools.NewRegistry(workDir)
	ag := agent.New(client, registry, workDir, cfg.ContextWindow)

	term := ui.NewTerminal()
	term.PrintBanner(currentModel, workDir)

	reader := bufio.NewReader(os.Stdin)

	// Track whether agent is currently running, protected by mutex
	var mu sync.Mutex
	var runCancel context.CancelFunc
	var lastInterrupt time.Time

	// Background goroutine to handle Ctrl+C signals
	go func() {
		for range sigCh {
			mu.Lock()
			cancel := runCancel
			now := time.Now()
			doubleTap := now.Sub(lastInterrupt) < 2*time.Second
			lastInterrupt = now
			mu.Unlock()

			if cancel != nil {
				// Agent is running — cancel the current operation
				cancel()
			} else if doubleTap {
				// Not running + double-tap — exit program
				fmt.Println("\nExiting.")
				os.Exit(0)
			} else {
				// Not running — print hint
				fmt.Println("\n(Press Ctrl+C again to exit)")
				term.PrintPrompt()
			}
		}
	}()

	running := true
	for running {
		term.PrintPrompt()

		input, err := readInput(reader)
		if err != nil {
			// EOF (Ctrl+D) or error
			fmt.Println()
			break
		}

		if input == "" {
			continue
		}

		switch input {
		case "/help":
			term.PrintHelp()
		case "/model":
			handleModelSwitch(reader, term, ag, &currentModel, &currentProvider)
		case "/quit":
			running = false
		case "/compact":
			if err := ag.Compact(rootCtx, term); err != nil {
				term.PrintError(err)
			}
		case "/clear":
			ag.Clear(term)
		case "/context":
			s := ag.ContextUsage()
			term.PrintContextUsage(s.TotalTokens, s.ContextWindow, s.Threshold,
				s.MessageCount, s.SystemTokens, s.ToolDefTokens,
				s.MessageTokens, s.ActualTokens)
		default:
			// Create a per-run cancellable context
			runCtx, cancel := context.WithCancel(rootCtx)

			mu.Lock()
			runCancel = cancel
			mu.Unlock()

			err := ag.Run(runCtx, input, term)

			mu.Lock()
			runCancel = nil
			mu.Unlock()

			cancel() // clean up context resources

			if err != nil {
				if err == context.Canceled || runCtx.Err() != nil {
					fmt.Println("Operation cancelled.")
					fmt.Println()
				} else {
					term.PrintError(err)
				}
			}
		}
	}
}

// readInput reads one or more lines from the reader, combining pasted
// multi-line input into a single string. It detects paste by checking
// both the bufio buffer and the OS-level stdin buffer for pending data.
func readInput(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}

	line = strings.TrimRight(line, "\r\n")

	// Check if more data is available (paste detection).
	// reader.Buffered() catches data already read into Go's bufio buffer.
	// ui.StdinHasData() catches data still in the OS console/stdin buffer
	// (needed on Windows where ReadFile returns one line at a time).
	if reader.Buffered() == 0 && !ui.StdinHasData() {
		return strings.TrimSpace(line), nil
	}

	// More data available — this is a paste. Collect all lines.
	lines := []string{line}
	for reader.Buffered() > 0 || ui.StdinHasData() {
		next, err := reader.ReadString('\n')
		if err != nil {
			lines = append(lines, strings.TrimRight(next, "\r\n"))
			break
		}
		lines = append(lines, strings.TrimRight(next, "\r\n"))
	}

	// Brief wait to catch any OS-level stragglers for large pastes
	time.Sleep(50 * time.Millisecond)
	for reader.Buffered() > 0 || ui.StdinHasData() {
		next, err := reader.ReadString('\n')
		if err != nil {
			lines = append(lines, strings.TrimRight(next, "\r\n"))
			break
		}
		lines = append(lines, strings.TrimRight(next, "\r\n"))
	}

	return strings.Join(lines, "\n"), nil
}

func newClient(provider, apiKey, model string, maxTokens int, baseURL string) llm.LLMClient {
	switch provider {
	case "anthropic":
		return llm.NewAnthropicClient(apiKey, model, maxTokens, baseURL)
	default:
		if strings.HasPrefix(model, "gpt-5") {
			return llm.NewOpenAIResponsesClient(apiKey, model, maxTokens, baseURL)
		}
		return llm.NewOpenAIClient(apiKey, model, maxTokens, baseURL)
	}
}

func handleModelSwitch(reader *bufio.Reader, term *ui.Terminal, ag *agent.Agent, currentModel, currentProvider *string) {
	models := config.KnownModels()
	options := make([]ui.ModelOption, len(models))
	for i, m := range models {
		options[i] = ui.ModelOption{
			Label:   m.Label,
			Current: m.Model == *currentModel,
		}
	}
	term.PrintModelMenu(options)

	fmt.Print("Choice: ")
	choice, err := reader.ReadString('\n')
	if err != nil {
		return
	}
	choice = strings.TrimSpace(choice)
	if choice == "" {
		return
	}

	var selectedModel, selectedProvider string

	n, err := strconv.Atoi(choice)
	if err == nil {
		if n == 0 {
			// Custom model name
			fmt.Print("Model name: ")
			custom, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			custom = strings.TrimSpace(custom)
			if custom == "" {
				return
			}
			selectedModel = custom
			selectedProvider = *currentProvider // assume same provider
		} else if n >= 1 && n <= len(models) {
			selectedModel = models[n-1].Model
			selectedProvider = models[n-1].Provider
		} else {
			term.PrintWarning("Invalid choice.")
			return
		}
	} else {
		term.PrintWarning("Invalid choice.")
		return
	}

	if selectedModel == *currentModel {
		term.PrintWarning(fmt.Sprintf("Already using %s.", selectedModel))
		return
	}

	// Get API key for the target provider
	apiKey := config.APIKeyForProvider(selectedProvider)
	if apiKey == "" {
		term.PrintWarning(fmt.Sprintf("No API key found for %s. Set the environment variable or add it to credentials.", selectedProvider))
		return
	}

	baseURL, maxTokens, contextWindow := config.ProviderDefaults(selectedProvider)
	client := newClient(selectedProvider, apiKey, selectedModel, maxTokens, baseURL)
	ag.SetClient(client, contextWindow)
	*currentModel = selectedModel
	*currentProvider = selectedProvider

	term.PrintModelSwitch(selectedModel)
}
