package tools

import (
	"context"
	"encoding/json"
	"fmt"
)

// ExploreFunc is the callback signature for running a sub-agent exploration.
// It receives a context and task description, returns the exploration summary.
type ExploreFunc func(ctx context.Context, task string) (string, error)

// SetExploreFunc injects the explore callback, breaking the circular dependency
// between the tools and agent packages.
func (r *Registry) SetExploreFunc(fn ExploreFunc) {
	r.exploreFunc = fn
}

type exploreInput struct {
	Task string `json:"task"`
}

func (r *Registry) exploreTool(ctx context.Context, input json.RawMessage) (string, error) {
	var params exploreInput
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if params.Task == "" {
		return "", fmt.Errorf("task is required")
	}
	if r.exploreFunc == nil {
		return "", fmt.Errorf("explore sub-agent not configured")
	}

	return r.exploreFunc(ctx, params.Task)
}

// NewReadOnlyRegistry creates a registry with only read-only tools (glob, grep, ls, read).
// Used by the explore sub-agent to prevent file modifications.
func NewReadOnlyRegistry(workDir string) *Registry {
	r := &Registry{workDir: workDir}
	r.registerReadOnlyBuiltins()
	return r
}

func (r *Registry) registerReadOnlyBuiltins() {
	r.register("glob",
		`Fast file pattern matching tool. Supports glob patterns like "**/*.go" or "src/**/*.ts". Returns matching file paths relative to working directory, sorted by modification time.`,
		json.RawMessage(`{
			"type": "object",
			"properties": {
				"pattern": {
					"type": "string",
					"description": "Glob pattern to match files (e.g., '**/*.go', 'src/**/*.ts')"
				}
			},
			"required": ["pattern"]
		}`),
		r.globTool,
	)

	r.register("grep",
		`Search file contents using RE2 regex. Returns matching lines with file paths and line numbers. Supports RE2 regex syntax. Filter files with the include parameter using glob patterns.`,
		json.RawMessage(`{
			"type": "object",
			"properties": {
				"pattern": {
					"type": "string",
					"description": "RE2 regular expression to search for"
				},
				"path": {
					"type": "string",
					"description": "Directory to search in (default: working directory)"
				},
				"include": {
					"type": "string",
					"description": "Glob pattern to filter filenames (e.g., '*.go', '*.{ts,tsx}')"
				}
			},
			"required": ["pattern"]
		}`),
		r.grepTool,
	)

	r.register("ls", "List directory contents with file/directory indicators and sizes.",
		json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {
					"type": "string",
					"description": "Directory path to list (default: working directory)"
				}
			}
		}`),
		r.lsTool,
	)

	r.register("read",
		`Read file contents with line numbers (cat -n format, 1-indexed). Use start_line/end_line for large files.`,
		json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {
					"type": "string",
					"description": "File path to read"
				},
				"start_line": {
					"type": "integer",
					"description": "First line to read (1-indexed, default: 1)"
				},
				"end_line": {
					"type": "integer",
					"description": "Last line to read (1-indexed, inclusive)"
				}
			},
			"required": ["path"]
		}`),
		r.readTool,
	)
}

