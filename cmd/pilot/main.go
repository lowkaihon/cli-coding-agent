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
		input, rewind, err := term.ReadLine(term.Prompt())
		if err != nil {
			// EOF (Ctrl+D) or error
			break
		}

		if rewind {
			handleRewind(reader, term, ag, rootCtx)
			continue
		}

		// Recreate reader after raw-mode ReadLine to avoid stale buffer
		reader = bufio.NewReader(os.Stdin)

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
		case "/resume":
			handleResume(reader, term, ag, workDir)
		case "/compact":
			if err := ag.Compact(rootCtx, term); err != nil {
				term.PrintError(err)
			} else {
				if err := ag.SaveSession(); err != nil {
					term.PrintWarning(fmt.Sprintf("Session save failed: %s", err))
				}
			}
		case "/clear":
			ag.Clear(term)
		case "/context":
			s := ag.ContextUsage()
			term.PrintContextUsage(s.TotalTokens, s.ContextWindow, s.Threshold,
				s.MessageCount, s.SystemTokens, s.ToolDefTokens,
				s.MessageTokens, s.ActualTokens)
		case "/rewind":
			handleRewind(reader, term, ag, rootCtx)
		default:
			ag.CreateCheckpoint(input)

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

			if saveErr := ag.SaveSession(); saveErr != nil {
				term.PrintWarning(fmt.Sprintf("Session save failed: %s", saveErr))
			}
		}
	}
}

func newClient(provider, apiKey, model string, maxTokens int, baseURL string) llm.LLMClient {
	switch provider {
	case "anthropic":
		return llm.NewAnthropicClient(apiKey, model, maxTokens, baseURL)
	default:
		return llm.NewOpenAIResponsesClient(apiKey, model, maxTokens, baseURL)
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
			// Ask which provider to use
			term.PrintProviderPrompt(*currentProvider)
			fmt.Print("Provider (Enter for current): ")
			pChoice, pErr := reader.ReadString('\n')
			if pErr != nil {
				return
			}
			switch strings.TrimSpace(pChoice) {
			case "1":
				selectedProvider = "openai"
			case "2":
				selectedProvider = "anthropic"
			case "":
				selectedProvider = *currentProvider
			default:
				term.PrintWarning("Invalid choice.")
				return
			}

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

func handleResume(reader *bufio.Reader, term *ui.Terminal, ag *agent.Agent, workDir string) {
	sessions, err := agent.ListSessions(workDir, 10)
	if err != nil {
		term.PrintError(fmt.Errorf("list sessions: %w", err))
		return
	}
	if len(sessions) == 0 {
		term.PrintWarning("No saved sessions found.")
		return
	}

	items := make([]ui.SessionListItem, len(sessions))
	for i, s := range sessions {
		items[i] = ui.SessionListItem{
			ID:       s.ID,
			Updated:  s.UpdatedAt,
			Preview:  s.Preview,
			MsgCount: s.MsgCount,
		}
	}
	term.PrintSessionList(items)

	fmt.Print("Choice: ")
	choice, err := reader.ReadString('\n')
	if err != nil {
		return
	}
	choice = strings.TrimSpace(choice)
	if choice == "" {
		return
	}

	n, err := strconv.Atoi(choice)
	if err != nil || n < 1 || n > len(sessions) {
		term.PrintWarning("Invalid choice.")
		return
	}

	selected := sessions[n-1]
	if err := ag.ResumeSession(selected.ID); err != nil {
		term.PrintError(fmt.Errorf("resume session: %w", err))
		return
	}

	term.PrintSessionResumed(selected.MsgCount, selected.Preview)
}

func handleRewind(reader *bufio.Reader, term *ui.Terminal, ag *agent.Agent, ctx context.Context) {
	items := ag.Checkpoints()
	if len(items) == 0 {
		term.PrintWarning("No checkpoints available. Checkpoints are created at the start of each turn.")
		return
	}

	// Convert to UI type
	uiItems := make([]ui.CheckpointListItem, len(items))
	for i, item := range items {
		uiItems[i] = ui.CheckpointListItem{
			Turn:      item.Turn,
			Timestamp: item.Timestamp,
			Preview:   item.Preview,
		}
	}
	term.PrintCheckpointList(uiItems)

	fmt.Print("Checkpoint number: ")
	choice, err := reader.ReadString('\n')
	if err != nil {
		return
	}
	choice = strings.TrimSpace(choice)
	if choice == "" {
		return
	}

	n, err := strconv.Atoi(choice)
	if err != nil || n < 1 || n > len(items) {
		term.PrintWarning("Invalid checkpoint number.")
		return
	}

	term.PrintRewindActions()

	fmt.Print("Action: ")
	action, err := reader.ReadString('\n')
	if err != nil {
		return
	}
	action = strings.TrimSpace(action)

	switch action {
	case "1":
		if err := ag.RewindAll(n); err != nil {
			term.PrintError(err)
			return
		}
		term.PrintRewindComplete("restored code and conversation")
	case "2":
		ag.RewindConversation(n)
		term.PrintRewindComplete("restored conversation only")
	case "3":
		if err := ag.RewindCode(n); err != nil {
			term.PrintError(err)
			return
		}
		term.PrintRewindComplete("restored code only")
	case "4":
		if err := ag.SummarizeFrom(ctx, n, term); err != nil {
			term.PrintError(err)
			return
		}
		term.PrintRewindComplete("summarized from checkpoint")
	case "5":
		// Never mind
		return
	default:
		term.PrintWarning("Invalid action.")
	}
}
