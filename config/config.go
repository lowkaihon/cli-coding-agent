package config

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

type Config struct {
	APIKey    string
	Model     string
	MaxTokens int
	BaseURL   string
}

func Load() (*Config, error) {
	// Load .env file if present (before reading env vars)
	loadEnvFile(".env")

	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("OPENAI_API_KEY environment variable is not set.\nSet it via: export OPENAI_API_KEY=\"sk-...\" or add it to a .env file")
	}

	cfg := &Config{
		APIKey:    apiKey,
		Model:     "gpt-4o-mini",
		MaxTokens: 4096,
		BaseURL:   "https://api.openai.com/v1",
	}

	return cfg, nil
}

// loadEnvFile reads a .env file and sets environment variables.
// Lines are KEY=VALUE format. Ignores comments (#) and blank lines.
// Does not override variables already set in the environment.
func loadEnvFile(path string) {
	f, err := os.Open(path)
	if err != nil {
		return // file not found is fine
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		// Strip surrounding quotes
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		}
		// Don't override existing env vars
		if os.Getenv(key) == "" {
			os.Setenv(key, value)
		}
	}
}
