package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type editInput struct {
	Path   string `json:"path"`
	OldStr string `json:"old_str"`
	NewStr string `json:"new_str"`
}

func (r *Registry) editTool(ctx context.Context, input json.RawMessage) (string, error) {
	params, err := parseInput[editInput](input)
	if err != nil {
		return "", err
	}
	if params.Path == "" {
		return "", fmt.Errorf("path is required")
	}
	if params.OldStr == "" {
		return "", fmt.Errorf("old_str is required")
	}

	absPath, err := ValidatePath(r.workDir, params.Path)
	if err != nil {
		return "", err
	}

	contentBytes, err := os.ReadFile(absPath)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}
	content := string(contentBytes)

	count := strings.Count(content, params.OldStr)
	if count == 0 {
		return "", fmt.Errorf("no match found for old_str in %s. Check for exact whitespace and indentation", params.Path)
	}
	if count > 1 {
		// Find line numbers of each match to help the LLM provide more context
		lines := strings.Split(content, "\n")
		firstLine := strings.SplitN(params.OldStr, "\n", 2)[0]
		var locations []string
		for i, line := range lines {
			if strings.Contains(line, firstLine) {
				locations = append(locations, fmt.Sprintf("line %d", i+1))
			}
		}
		return "", fmt.Errorf("old_str matches %d times in %s (at %s). Include more surrounding context to make the match unique",
			count, params.Path, strings.Join(locations, ", "))
	}

	newContent := strings.Replace(content, params.OldStr, params.NewStr, 1)

	return "", &NeedsConfirmation{
		Tool:       "edit",
		Path:       params.Path,
		Preview:    content,
		NewContent: newContent,
		Execute: func() (string, error) {
			info, err := os.Stat(absPath)
			if err != nil {
				return "", fmt.Errorf("stat file: %w", err)
			}

			if err := AtomicWrite(absPath, []byte(newContent), info.Mode()); err != nil {
				return "", fmt.Errorf("write file: %w", err)
			}

			return fmt.Sprintf("Successfully edited %s", params.Path), nil
		},
	}
}
