package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type lsInput struct {
	Path string `json:"path"`
}

func (r *Registry) lsTool(ctx context.Context, input json.RawMessage) (string, error) {
	var params lsInput
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	dir := r.workDir
	if params.Path != "" {
		var err error
		dir, err = ValidatePath(r.workDir, params.Path)
		if err != nil {
			return "", err
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("read directory: %w", err)
	}

	var result strings.Builder
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}

		if entry.IsDir() {
			result.WriteString(fmt.Sprintf("  %s/\n", entry.Name()))
		} else {
			result.WriteString(fmt.Sprintf("  %-40s %s\n", entry.Name(), formatSize(info.Size())))
		}
	}

	if result.Len() == 0 {
		return "Directory is empty.", nil
	}

	return result.String(), nil
}

func formatSize(bytes int64) string {
	switch {
	case bytes >= 1<<20:
		return fmt.Sprintf("%.1fMB", float64(bytes)/(1<<20))
	case bytes >= 1<<10:
		return fmt.Sprintf("%.1fKB", float64(bytes)/(1<<10))
	default:
		return fmt.Sprintf("%dB", bytes)
	}
}
