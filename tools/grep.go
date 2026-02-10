package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type grepInput struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path"`
	Include string `json:"include"`
}

func (r *Registry) grepTool(ctx context.Context, input json.RawMessage) (string, error) {
	var params grepInput
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if params.Pattern == "" {
		return "", fmt.Errorf("pattern is required")
	}

	re, err := regexp.Compile(params.Pattern)
	if err != nil {
		return "", fmt.Errorf("invalid regex (RE2 syntax): %w", err)
	}

	searchDir := r.workDir
	if params.Path != "" {
		searchDir, err = ValidatePath(r.workDir, params.Path)
		if err != nil {
			return "", err
		}
	}

	const maxResults = 50
	var results []string
	totalMatches := 0

	err = filepath.WalkDir(searchDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if d.IsDir() {
			if shouldSkipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}

		// Apply include filter
		if params.Include != "" {
			matched, _ := filepath.Match(params.Include, d.Name())
			if !matched {
				return nil
			}
		}

		// Skip binary files (check first 512 bytes)
		if isBinaryFile(path) {
			return nil
		}

		file, err := os.Open(path)
		if err != nil {
			return nil
		}

		rel, _ := filepath.Rel(r.workDir, path)
		rel = filepath.ToSlash(rel)

		scanner := bufio.NewScanner(file)
		lineNum := 0
		for scanner.Scan() {
			lineNum++
			line := scanner.Text()
			if re.MatchString(line) {
				totalMatches++
				if len(results) < maxResults {
					results = append(results, fmt.Sprintf("%s:%d: %s", rel, lineNum, truncateLine(line, 200)))
				}
			}
		}
		file.Close()
		return nil
	})

	if err != nil {
		return "", err
	}

	if len(results) == 0 {
		return "No matches found.", nil
	}

	var out strings.Builder
	for _, r := range results {
		out.WriteString(r)
		out.WriteByte('\n')
	}

	if totalMatches > maxResults {
		out.WriteString(fmt.Sprintf("\n... and %d more matches", totalMatches-maxResults))
	}

	return out.String(), nil
}

func truncateLine(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

func isBinaryFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return true
	}
	defer f.Close()

	buf := make([]byte, 512)
	n, err := f.Read(buf)
	if err != nil {
		return true
	}

	for _, b := range buf[:n] {
		if b == 0 {
			return true
		}
	}
	return false
}
