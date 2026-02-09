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
	"syscall"

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

	// Set up graceful shutdown
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

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

	running := true
	for running {
		term.PrintPrompt()

		input, err := reader.ReadString('\n')
		if err != nil {
			// EOF (Ctrl+D) or error
			fmt.Println()
			break
		}

		input = strings.TrimSpace(input)
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
			if err := ag.Compact(ctx, term); err != nil {
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
			if err := ag.Run(ctx, input, term); err != nil {
				if ctx.Err() != nil {
					fmt.Println("\nInterrupted.")
					running = false
				} else {
					term.PrintError(err)
				}
			}
		}
	}
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
