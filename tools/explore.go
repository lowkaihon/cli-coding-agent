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
	r.registerReadOnlyTools()
	return r
}

