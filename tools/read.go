package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type readInput struct {
	Path      string `json:"path"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}

func (r *Registry) readTool(ctx context.Context, input json.RawMessage) (string, error) {
	params, err := parseInput[readInput](input)
	if err != nil {
		return "", err
	}
	if params.Path == "" {
		return "", fmt.Errorf("path is required")
	}

	absPath, err := ValidatePath(r.workDir, params.Path)
	if err != nil {
		return "", err
	}

	file, err := os.Open(absPath)
	if err != nil {
		return "", fmt.Errorf("open file: %w", err)
	}
	defer file.Close()

	// Default: 1-indexed, start from line 1
	startLine := params.StartLine
	if startLine <= 0 {
		startLine = 1
	}
	endLine := params.EndLine

	const maxLines = 500

	var result strings.Builder
	scanner := bufio.NewScanner(file)
	// Increase buffer for long lines
	scanner.Buffer(make([]byte, 0, 256*1024), 256*1024)

	lineNum := 0
	linesRead := 0
	totalLines := 0

	for scanner.Scan() {
		lineNum++
		totalLines = lineNum

		if lineNum < startLine {
			continue
		}
		if endLine > 0 && lineNum > endLine {
			continue // keep counting total lines
		}

		linesRead++
		if endLine <= 0 && linesRead > maxLines {
			// Count remaining lines
			for scanner.Scan() {
				lineNum++
				totalLines = lineNum
			}
			result.WriteString(fmt.Sprintf("\n... (file has %d total lines, showing lines %d-%d. Use start_line/end_line to read more.)",
				totalLines, startLine, startLine+maxLines-1))
			break
		}

		result.WriteString(fmt.Sprintf("%4d │ %s\n", lineNum, scanner.Text()))
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}

	if result.Len() == 0 {
		return "File is empty.", nil
	}

	return result.String(), nil
}
