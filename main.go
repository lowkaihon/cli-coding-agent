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

	"github.com/kaiho/pilot/agent"
	"github.com/kaiho/pilot/config"
	"github.com/kaiho/pilot/llm"
	"github.com/kaiho/pilot/tools"
	"github.com/kaiho/pilot/ui"
)

func main() {
	model := flag.String("model", "", "Model name (default: gpt-4o-mini)")
	flag.Parse()

	// Set up graceful shutdown
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		os.Exit(1)
	}

	if *model != "" {
		cfg.Model = *model
	}

	client := llm.NewOpenAIClient(cfg.APIKey, cfg.Model, cfg.MaxTokens, cfg.BaseURL)

	workDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting working directory: %s\n", err)
		os.Exit(1)
	}

	registry := tools.NewRegistry(workDir)
	ag := agent.New(client, registry, workDir)

	term := ui.NewTerminal()
	term.PrintBanner(cfg.Model, workDir)

	reader := bufio.NewReader(os.Stdin)

	for {
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

		if input == "exit" || input == "quit" {
			break
		}

		if err := ag.Run(ctx, input, term); err != nil {
			if ctx.Err() != nil {
				fmt.Println("\nInterrupted.")
				break
			}
			term.PrintError(err)
		}
	}
}
