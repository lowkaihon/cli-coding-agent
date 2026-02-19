// Package tools provides the tool registry and implementations for file operations,
// shell execution, and codebase exploration, with path sandboxing for security.
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
	tools         []toolEntry
	workDir       string
	exploreFunc   ExploreFunc
	taskCallbacks TaskCallbacks
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
	case "glob", "grep", "ls", "read", "explore", "update_task", "read_tasks":
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

// registerReadOnlyTools registers the read-only tools (glob, grep, ls, read).
// Shared by both the full registry and the read-only registry used by the explore sub-agent.
func (r *Registry) registerReadOnlyTools() {
	r.register("glob",
		`Fast file pattern matching tool. Supports glob patterns like "**/*.go" or "src/**/*.ts". Returns matching file paths relative to working directory, sorted by modification time. Use this tool when you need to find files by name patterns. Prefer this over bash find or ls commands.`,
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
		`Search file contents using RE2 regex. Returns matching lines with file paths and line numbers. ALWAYS use this tool for content search — never use bash grep or rg. Supports RE2 regex syntax (e.g., "log.*Error", "func\\s+\\w+"). Note: RE2 does not support lookaheads or lookbehinds. Literal braces need escaping (use "interface\\{\\}" to find "interface{}" in Go code). Filter files with the include parameter using glob patterns (e.g., "*.go", "*.{ts,tsx}").`,
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

	r.register("ls", "List directory contents with file/directory indicators and sizes. Can only list directories, not files. Use glob to find files by pattern.",
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
		`Read file contents with line numbers (cat -n format, 1-indexed). Use start_line/end_line for large files to read specific sections. Can only read files, not directories — use ls for directories. Read multiple files in parallel when you need to understand several files at once. Always use this tool instead of bash cat, head, or tail.`,
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

func (r *Registry) registerTaskTools() {
	r.register("write_tasks",
		`Create or replace the task list for planning multi-step work. User confirmation required.
Each task has:
- content: short imperative title (e.g. "Add auth middleware")
- description: detailed implementation plan with files to create/modify, code patterns to follow, and what "done" looks like
- active_form: (optional) continuous form for status display

After the user approves the plan, immediately mark task 1 as in_progress and begin implementation.`,
		json.RawMessage(`{
			"type": "object",
			"properties": {
				"tasks": {
					"type": "array",
					"items": {
						"type": "object",
						"properties": {
							"content": {
								"type": "string",
								"description": "Short imperative title (e.g. 'Add auth middleware')"
							},
							"description": {
								"type": "string",
								"description": "Detailed description of what needs to be done. Include enough detail for another agent to understand and complete the task: specific files to create/modify, functions to change, code patterns to follow, and acceptance criteria."
							},
							"active_form": {
								"type": "string",
								"description": "Task description in continuous form (e.g. 'Adding auth middleware')"
							}
						},
						"required": ["content", "description"]
					},
					"description": "Array of tasks to create"
				}
			},
			"required": ["tasks"]
		}`),
		r.writeTasksTool,
	)

	r.register("update_task",
		`Update the status of a task by ID. Valid statuses: pending, in_progress, completed. Mark tasks in_progress when you start working on them and completed when done. Returns the updated task list.`,
		json.RawMessage(`{
			"type": "object",
			"properties": {
				"id": {
					"type": "integer",
					"description": "Task ID to update"
				},
				"status": {
					"type": "string",
					"enum": ["pending", "in_progress", "completed"],
					"description": "New status for the task"
				}
			},
			"required": ["id", "status"]
		}`),
		r.updateTaskTool,
	)

	r.register("read_tasks",
		`Read the current task list. Task state is already in your system prompt at the start of each turn — you rarely need this tool. Only useful after many turns of work when context may have been compacted.`,
		json.RawMessage(`{
			"type": "object",
			"properties": {}
		}`),
		r.readTasksTool,
	)
}

func (r *Registry) registerBuiltins() {
	r.registerReadOnlyTools()
	r.registerTaskTools()

	r.register("write",
		`Create or overwrite a file with the given content. Creates parent directories if needed. User confirmation required. ALWAYS prefer editing existing files over writing new ones — use the edit tool to modify existing files. Never proactively create documentation files (*.md) or README files unless explicitly requested.`,
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

	r.register("edit",
		`Edit a file by replacing an exact string match. The old_str must appear exactly once in the file. When editing text from read tool output, preserve the exact indentation (tabs/spaces) as shown in the file content — do not include line numbers from the read output. If the edit fails because old_str is not unique, include more surrounding context lines to make it unique. Always prefer editing existing files over creating new ones.`,
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

	r.register("bash",
		`Execute a shell command in the working directory. Use for terminal operations like git, builds, tests, and other system commands. Do NOT use bash for file operations (reading, writing, editing, searching) — use the dedicated tools instead. Specifically, do not use cat, head, tail, sed, awk, find, grep, or echo when a dedicated tool exists.

Before executing commands that create new directories or files, first verify the parent directory exists using ls. Always quote file paths containing spaces. Use && to chain sequential dependent commands. Prefer absolute paths and avoid cd when possible.

All commands require user confirmation. Default timeout: 30s, max: 120s. Output is truncated at 10,000 characters.

Git safety: Never force-push, reset --hard, use --no-verify, or amend unless the user explicitly asks. Never use interactive flags (-i). Prefer staging specific files over "git add -A". Only commit when explicitly requested by the user.`,
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

	r.register("explore",
		`Explore the codebase to answer broad questions by delegating to a focused sub-agent. The sub-agent has its own context and read-only tools (glob, grep, ls, read). Use this for questions like "how does authentication work?", "what's the project structure?", or "find all API endpoints". Do NOT use this for direct tasks like editing files or running commands — only for research and exploration.`,
		json.RawMessage(`{
			"type": "object",
			"properties": {
				"task": {
					"type": "string",
					"description": "What to explore or research in the codebase"
				}
			},
			"required": ["task"]
		}`),
		r.exploreTool,
	)

}
