package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type writeInput struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// NeedsConfirmation is an error type that signals the agent should confirm with the user.
type NeedsConfirmation struct {
	Tool    string
	Path    string
	Preview string // diff or file preview text
	Execute func() (string, error)
}

func (e *NeedsConfirmation) Error() string {
	return fmt.Sprintf("%s requires confirmation for %s", e.Tool, e.Path)
}

func (r *Registry) writeTool(ctx context.Context, input json.RawMessage) (string, error) {
	var params writeInput
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if params.Path == "" {
		return "", fmt.Errorf("path is required")
	}
	if params.Content == "" {
		return "", fmt.Errorf("content is required")
	}

	absPath, err := ValidatePath(r.workDir, params.Path)
	if err != nil {
		return "", err
	}

	return "", &NeedsConfirmation{
		Tool:    "write",
		Path:    params.Path,
		Preview: params.Content,
		Execute: func() (string, error) {
			dir := filepath.Dir(absPath)
			if err := os.MkdirAll(dir, 0755); err != nil {
				return "", fmt.Errorf("create directory: %w", err)
			}

			if err := AtomicWrite(absPath, []byte(params.Content), 0644); err != nil {
				return "", fmt.Errorf("write file: %w", err)
			}

			return fmt.Sprintf("Successfully wrote %s (%d bytes)", params.Path, len(params.Content)), nil
		},
	}
}
