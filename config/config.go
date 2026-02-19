// Package config handles LLM provider configuration, .env file loading,
// API key management, and XDG-compliant credential storage.
package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Config holds the resolved LLM provider configuration including API credentials,
// model selection, and context window limits.
type Config struct {
	Provider      string
	APIKey        string
	Model         string
	MaxTokens     int
	BaseURL       string
	ContextWindow int
}

// Load resolves LLM configuration by reading .env files, XDG credentials,
// and prompting for missing API keys. An empty provider defaults to "openai".
func Load(provider string) (*Config, error) {
	// Load .env file in cwd if present
	loadEnvFile(".env")

	// Load credentials from XDG config dir
	if configDir, err := ConfigDir(); err == nil {
		loadEnvFile(filepath.Join(configDir, "credentials"))
	}

	if provider == "" {
		provider = "openai"
	}

	var cfg *Config
	switch provider {
	case "anthropic":
		apiKey := os.Getenv("ANTHROPIC_API_KEY")
		if apiKey == "" {
			var err error
			apiKey, err = promptAPIKeyFor("Anthropic", "ANTHROPIC_API_KEY")
			if err != nil {
				return nil, err
			}
		}
		cfg = &Config{
			Provider:      "anthropic",
			APIKey:        apiKey,
			Model:         "claude-sonnet-4-5-20250929",
			MaxTokens:     16384,
			BaseURL:       "https://api.anthropic.com/v1",
			ContextWindow: 200000,
		}
	default:
		apiKey := os.Getenv("OPENAI_API_KEY")
		if apiKey == "" {
			var err error
			apiKey, err = promptAPIKeyFor("OpenAI", "OPENAI_API_KEY")
			if err != nil {
				return nil, err
			}
		}
		cfg = &Config{
			Provider:      "openai",
			APIKey:        apiKey,
			Model:         "gpt-4o-mini",
			MaxTokens:     16384,
			BaseURL:       "https://api.openai.com/v1",
			ContextWindow: 128000,
		}
	}

	return cfg, nil
}

// KnownModel represents a curated model option.
type KnownModel struct {
	Provider string
	Model    string
	Label    string
}

// KnownModels returns the list of curated models for the /model menu.
func KnownModels() []KnownModel {
	return []KnownModel{
		{"openai", "gpt-4o-mini", "GPT-4o Mini (OpenAI)"},
		{"openai", "gpt-5.1-codex-mini", "GPT-5.1 Codex Mini (OpenAI)"},
		{"openai", "gpt-5.2-codex", "GPT-5.2 Codex (OpenAI)"},
		{"anthropic", "claude-opus-4-6", "Claude Opus 4.6 (Anthropic)"},
		{"anthropic", "claude-sonnet-4-5-20250929", "Claude Sonnet 4.5 (Anthropic)"},
		{"anthropic", "claude-haiku-4-5-20251001", "Claude Haiku 4.5 (Anthropic)"},
	}
}

// ProviderDefaults returns the base URL, max tokens, and context window for a provider and model.
func ProviderDefaults(provider, model string) (baseURL string, maxTokens int, contextWindow int) {
	switch provider {
	case "anthropic":
		return "https://api.anthropic.com/v1", 16384, 200000
	default:
		return "https://api.openai.com/v1", 16384, openAIContextWindow(model)
	}
}

// openAIContextWindow returns the context window size for an OpenAI model
// based on its name prefix.
func openAIContextWindow(model string) int {
	switch {
	case strings.HasPrefix(model, "gpt-5"):
		return 400000
	case strings.HasPrefix(model, "o3") || strings.HasPrefix(model, "o4"):
		return 200000
	case strings.HasPrefix(model, "gpt-3.5"):
		return 16000
	default:
		return 128000
	}
}

// APIKeyForProvider returns the API key for the given provider from env/credentials.
// Returns empty string if not found.
func APIKeyForProvider(provider string) string {
	switch provider {
	case "anthropic":
		return os.Getenv("ANTHROPIC_API_KEY")
	default:
		return os.Getenv("OPENAI_API_KEY")
	}
}

// ConfigDir returns the XDG-compliant config directory for Pilot.
// Uses $XDG_CONFIG_HOME/pilot if set, otherwise ~/.config/pilot.
func ConfigDir() (string, error) {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" && filepath.IsAbs(dir) {
		return filepath.Join(dir, "pilot"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".config", "pilot"), nil
}

// promptAPIKeyFor asks the user for an API key and saves it to the credentials file.
func promptAPIKeyFor(providerName, envVar string) (string, error) {
	fmt.Printf("Enter your %s API key: ", providerName)
	reader := bufio.NewReader(os.Stdin)
	key, err := reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("failed to read API key: %w", err)
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return "", fmt.Errorf("API key cannot be empty")
	}

	// Save to credentials file
	configDir, err := ConfigDir()
	if err != nil {
		return key, nil
	}

	if err := os.MkdirAll(configDir, 0755); err != nil {
		return key, nil
	}

	credPath := filepath.Join(configDir, "credentials")
	// Append to existing credentials rather than overwrite
	f, err := os.OpenFile(credPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return key, nil
	}
	defer f.Close()

	fmt.Fprintf(f, "%s=%s\n", envVar, key)
	fmt.Printf("API key saved to %s\n", credPath)
	return key, nil
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
