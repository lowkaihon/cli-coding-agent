package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type globInput struct {
	Pattern string `json:"pattern"`
}

func (r *Registry) globTool(ctx context.Context, input json.RawMessage) (string, error) {
	params, err := parseInput[globInput](input)
	if err != nil {
		return "", err
	}
	if params.Pattern == "" {
		return "", fmt.Errorf("pattern is required")
	}

	const maxResults = 100
	var matches []string

	err = filepath.WalkDir(r.workDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip errors
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// Skip hidden directories and common ignores
		if d.IsDir() {
			if shouldSkipDir(d.Name()) {
				return filepath.SkipDir
			}
			// Skip symlinks that point to directories
			if d.Type()&os.ModeSymlink != 0 {
				return filepath.SkipDir
			}
			return nil
		}

		rel, err := filepath.Rel(r.workDir, path)
		if err != nil {
			return nil
		}
		// Normalize to forward slashes for pattern matching
		rel = filepath.ToSlash(rel)

		matched, err := matchGlob(params.Pattern, rel)
		if err != nil {
			return fmt.Errorf("invalid glob pattern: %w", err)
		}

		if matched {
			matches = append(matches, rel)
		}
		return nil
	})

	if err != nil {
		return "", err
	}

	if len(matches) == 0 {
		return "No files matched the pattern.", nil
	}

	var result strings.Builder
	limit := len(matches)
	truncated := false
	if limit > maxResults {
		limit = maxResults
		truncated = true
	}

	for _, m := range matches[:limit] {
		result.WriteString(m)
		result.WriteByte('\n')
	}

	if truncated {
		result.WriteString(fmt.Sprintf("\n... and %d more matches", len(matches)-maxResults))
	}

	return result.String(), nil
}

// matchGlob performs glob matching supporting ** for recursive directory matching.
func matchGlob(pattern, name string) (bool, error) {
	// Handle ** pattern: split and match segments
	if strings.Contains(pattern, "**") {
		return matchDoublestar(pattern, name)
	}
	return filepath.Match(pattern, name)
}

// matchDoublestar handles ** glob patterns.
func matchDoublestar(pattern, name string) (bool, error) {
	// Simple ** implementation: ** matches any number of path segments
	parts := strings.Split(pattern, "**")

	if len(parts) == 2 {
		prefix := strings.TrimSuffix(parts[0], "/")
		suffix := strings.TrimPrefix(parts[1], "/")

		if prefix == "" && suffix == "" {
			return true, nil
		}

		if prefix == "" {
			// Pattern like "**/*.go" — match suffix against name and all sub-paths
			segments := strings.Split(name, "/")
			for i := range segments {
				subpath := strings.Join(segments[i:], "/")
				if matched, _ := filepath.Match(suffix, subpath); matched {
					return true, nil
				}
				// Also try matching just the filename
				if matched, _ := filepath.Match(suffix, segments[len(segments)-1]); matched {
					return true, nil
				}
			}
			return false, nil
		}

		if suffix == "" {
			// Pattern like "src/**" — match if name starts with prefix
			return strings.HasPrefix(name, prefix+"/") || name == prefix, nil
		}

		// Pattern like "src/**/*.go"
		if !strings.HasPrefix(name, prefix+"/") && name != prefix {
			return false, nil
		}
		rest := strings.TrimPrefix(name, prefix+"/")
		return matchDoublestar("**/"+suffix, rest)
	}

	// Fallback for complex patterns
	return filepath.Match(pattern, name)
}
