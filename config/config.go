package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	APIKey    string
	Model     string
	MaxTokens int
	BaseURL   string
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

func Load() (*Config, error) {
	// Load .env file in cwd if present
	loadEnvFile(".env")

	// Load credentials from XDG config dir
	if configDir, err := ConfigDir(); err == nil {
		loadEnvFile(filepath.Join(configDir, "credentials"))
	}

	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		var err error
		apiKey, err = promptAPIKey()
		if err != nil {
			return nil, err
		}
	}

	cfg := &Config{
		APIKey:    apiKey,
		Model:     "gpt-4o-mini",
		MaxTokens: 4096,
		BaseURL:   "https://api.openai.com/v1",
	}

	return cfg, nil
}

// promptAPIKey asks the user for their API key and saves it to the credentials file.
func promptAPIKey() (string, error) {
	fmt.Print("Enter your OpenAI API key: ")
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
		// Can't save but can still use the key this session
		return key, nil
	}

	if err := os.MkdirAll(configDir, 0755); err != nil {
		return key, nil
	}

	credPath := filepath.Join(configDir, "credentials")
	content := []byte("OPENAI_API_KEY=" + key + "\n")
	if err := os.WriteFile(credPath, content, 0600); err != nil {
		return key, nil
	}

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
