package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/lowkaihon/cli-coding-agent/llm"
)

// ToolFunc is the signature for tool implementations.
type ToolFunc func(ctx context.Context, input json.RawMessage) (string, error)

type toolEntry struct {
	name string
	fn   ToolFunc
	def  llm.ToolDef
}

// Registry holds all available tools and dispatches execution.
type Registry struct {
	tools   []toolEntry
	workDir string
}

// NewRegistry creates a registry and registers all built-in tools.
func NewRegistry(workDir string) *Registry {
	r := &Registry{workDir: workDir}
	r.registerBuiltins()
	return r
}

func (r *Registry) register(name, description string, schema json.RawMessage, fn ToolFunc) {
	r.tools = append(r.tools, toolEntry{
		name: name,
		fn:   fn,
		def: llm.ToolDef{
			Type: "function",
			Function: llm.FunctionDef{
				Name:        name,
				Description: description,
				Parameters:  schema,
			},
		},
	})
}

// Execute runs a tool by name with the given input.
func (r *Registry) Execute(ctx context.Context, name string, input json.RawMessage) (string, error) {
	for _, t := range r.tools {
		if t.name == name {
			return t.fn(ctx, input)
		}
	}
	return "", fmt.Errorf("unknown tool: %s", name)
}

// IsReadOnly returns true for tools that don't modify the filesystem.
func (r *Registry) IsReadOnly(name string) bool {
	switch name {
	case "glob", "grep", "ls", "read":
		return true
	default:
		return false
	}
}

// Definitions returns tool definitions in stable registration order.
func (r *Registry) Definitions() []llm.ToolDef {
	defs := make([]llm.ToolDef, len(r.tools))
	for i, t := range r.tools {
		defs[i] = t.def
	}
	return defs
}

func (r *Registry) registerBuiltins() {
	r.register("glob", "Find files matching a glob pattern (supports ** for recursive matching). Returns file paths relative to working directory.",
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

	r.register("grep", "Search file contents using RE2 regex. Returns matching lines with file paths and line numbers.",
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

	r.register("read", "Read file contents with line numbers. Supports optional line range to read specific sections.",
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

	r.register("write", "Create or overwrite a file with the given content. Creates parent directories if needed.",
		json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {
					"type": "string",
					"description": "File path to write"
				},
				"content": {
					"type": "string",
					"description": "Content to write to the file"
				}
			},
			"required": ["path", "content"]
		}`),
		r.writeTool,
	)

	r.register("edit", "Edit a file by replacing an exact string match. The old_str must appear exactly once in the file.",
		json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {
					"type": "string",
					"description": "File path to edit"
				},
				"old_str": {
					"type": "string",
					"description": "Exact string to find (must appear exactly once)"
				},
				"new_str": {
					"type": "string",
					"description": "Replacement string"
				}
			},
			"required": ["path", "old_str", "new_str"]
		}`),
		r.editTool,
	)

	r.register("bash", "Execute a shell command. Runs in the working directory. Use for builds, tests, git, etc. All commands require user confirmation.",
		json.RawMessage(`{
			"type": "object",
			"properties": {
				"command": {
					"type": "string",
					"description": "Shell command to execute"
				},
				"timeout": {
					"type": "integer",
					"description": "Timeout in seconds (default: 30, max: 120)"
				}
			},
			"required": ["command"]
		}`),
		r.bashTool,
	)

}
