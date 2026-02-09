package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadEnvFile(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")

	content := `# This is a comment
OPENAI_API_KEY=sk-test123

SOME_VAR="quoted_value"
SINGLE_QUOTED='single'
EMPTY=
`
	os.WriteFile(envPath, []byte(content), 0644)

	// Clear env vars first
	os.Unsetenv("OPENAI_API_KEY")
	os.Unsetenv("SOME_VAR")
	os.Unsetenv("SINGLE_QUOTED")
	os.Unsetenv("EMPTY")

	loadEnvFile(envPath)

	tests := []struct {
		key  string
		want string
	}{
		{"OPENAI_API_KEY", "sk-test123"},
		{"SOME_VAR", "quoted_value"},
		{"SINGLE_QUOTED", "single"},
		{"EMPTY", ""},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			got := os.Getenv(tt.key)
			if got != tt.want {
				t.Errorf("expected %q=%q, got %q", tt.key, tt.want, got)
			}
		})
	}

	// Clean up
	for _, tt := range tests {
		os.Unsetenv(tt.key)
	}
}

func TestLoadEnvFileDoesNotOverride(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")

	os.WriteFile(envPath, []byte("MY_VAR=from_file\n"), 0644)
	os.Setenv("MY_VAR", "from_env")
	defer os.Unsetenv("MY_VAR")

	loadEnvFile(envPath)

	if got := os.Getenv("MY_VAR"); got != "from_env" {
		t.Errorf("expected from_env, got %s", got)
	}
}

func TestLoadEnvFileMissing(t *testing.T) {
	// Should not panic on missing file
	loadEnvFile("/nonexistent/path/.env")
}

func TestConfigDir(t *testing.T) {
	// Test with XDG_CONFIG_HOME set
	original := os.Getenv("XDG_CONFIG_HOME")
	defer os.Setenv("XDG_CONFIG_HOME", original)

	dir := t.TempDir()
	os.Setenv("XDG_CONFIG_HOME", dir)

	configDir, err := ConfigDir()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := filepath.Join(dir, "pilot")
	if configDir != expected {
		t.Errorf("expected %s, got %s", expected, configDir)
	}
}

func TestConfigDirDefault(t *testing.T) {
	original := os.Getenv("XDG_CONFIG_HOME")
	os.Unsetenv("XDG_CONFIG_HOME")
	defer os.Setenv("XDG_CONFIG_HOME", original)

	configDir, err := ConfigDir()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	home, _ := os.UserHomeDir()
	expected := filepath.Join(home, ".config", "pilot")
	if configDir != expected {
		t.Errorf("expected %s, got %s", expected, configDir)
	}
}
