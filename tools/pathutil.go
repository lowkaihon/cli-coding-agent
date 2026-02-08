package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ValidatePath ensures the resolved path is within the allowed working directory.
// Prevents path traversal attacks (e.g., "../../.ssh/id_rsa", "/etc/passwd").
func ValidatePath(workDir, requestedPath string) (string, error) {
	if filepath.IsAbs(requestedPath) {
		// Check if the absolute path is within workDir
		rel, err := filepath.Rel(workDir, requestedPath)
		if err != nil || strings.HasPrefix(rel, "..") {
			return "", fmt.Errorf("path %q is outside the working directory", requestedPath)
		}
		return filepath.Clean(requestedPath), nil
	}

	absPath := filepath.Join(workDir, requestedPath)
	absPath = filepath.Clean(absPath)

	rel, err := filepath.Rel(workDir, absPath)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("path %q is outside the working directory", requestedPath)
	}

	return absPath, nil
}

// AtomicWrite writes content to a file atomically using a temp file + rename.
// The temp file is created in the same directory as the target to ensure rename works.
func AtomicWrite(targetPath string, content []byte, perm os.FileMode) error {
	dir := filepath.Dir(targetPath)
	tmp, err := os.CreateTemp(dir, ".pilot-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()

	defer func() {
		if tmpPath != "" {
			os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Chmod(tmpPath, perm); err != nil {
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := os.Rename(tmpPath, targetPath); err != nil {
		return fmt.Errorf("rename temp file: %w", err)
	}
	tmpPath = "" // prevent deferred cleanup
	return nil
}
