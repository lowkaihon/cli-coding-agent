package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

// projectHash returns a deterministic 16-char hex hash of the absolute workDir path.
// Used to isolate sessions per project under the global ~/.pilot/ directory.
func projectHash(workDir string) string {
	absPath, err := filepath.Abs(workDir)
	if err != nil {
		absPath = workDir
	}
	h := sha256.Sum256([]byte(filepath.Clean(absPath)))
	return hex.EncodeToString(h[:])[:16]
}

// GlobalSessionsDir returns the path to the sessions directory for a given project
// under the user's home directory: ~/.pilot/projects/<hash>/sessions
func GlobalSessionsDir(workDir string) (string, error) {
	return globalSessionsDir(workDir)
}

func globalSessionsDir(workDir string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".pilot", "projects", projectHash(workDir), "sessions"), nil
}
