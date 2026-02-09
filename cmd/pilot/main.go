package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
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

	var client llm.LLMClient
	switch cfg.Provider {
	case "anthropic":
		client = llm.NewAnthropicClient(cfg.APIKey, cfg.Model, cfg.MaxTokens, cfg.BaseURL)
	default:
		client = llm.NewOpenAIClient(cfg.APIKey, cfg.Model, cfg.MaxTokens, cfg.BaseURL)
	}

	workDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting working directory: %s\n", err)
		os.Exit(1)
	}

	registry := tools.NewRegistry(workDir)
	ag := agent.New(client, registry, workDir, cfg.ContextWindow)

	term := ui.NewTerminal()
	term.PrintBanner(cfg.Model, workDir)

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
				s.MessageCount, s.SystemTokens, s.UserTokens,
				s.AssistantTokens, s.ToolTokens)
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
