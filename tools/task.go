package tools

import (
	"context"
	"encoding/json"
	"fmt"
)

// TaskInput is the per-task shape for write_tasks (no ID or timestamps).
type TaskInput struct {
	Content    string `json:"content"`
	ActiveForm string `json:"active_form"`
}

// TaskCallbacks breaks the circular dependency between tools and agent
// for task operations, following the same pattern as ExploreFunc.
type TaskCallbacks struct {
	WriteTasks func(tasks []TaskInput) string
	UpdateTask func(id int, status string) error
	ReadTasks  func() string
}

// SetTaskCallbacks injects the task callbacks into the registry.
func (r *Registry) SetTaskCallbacks(cb TaskCallbacks) {
	r.taskCallbacks = cb
}

type writeTasksInput struct {
	Tasks []TaskInput `json:"tasks"`
}

func (r *Registry) writeTasksTool(_ context.Context, input json.RawMessage) (string, error) {
	params, err := parseInput[writeTasksInput](input)
	if err != nil {
		return "", err
	}
	if len(params.Tasks) == 0 {
		return "", fmt.Errorf("tasks array is required and must not be empty")
	}
	for i, t := range params.Tasks {
		if t.Content == "" {
			return "", fmt.Errorf("task %d: content is required", i+1)
		}
	}
	if r.taskCallbacks.WriteTasks == nil {
		return "", fmt.Errorf("task callbacks not configured")
	}
	return r.taskCallbacks.WriteTasks(params.Tasks), nil
}

type updateTaskInput struct {
	ID     int    `json:"id"`
	Status string `json:"status"`
}

func (r *Registry) updateTaskTool(_ context.Context, input json.RawMessage) (string, error) {
	params, err := parseInput[updateTaskInput](input)
	if err != nil {
		return "", err
	}
	if params.ID == 0 {
		return "", fmt.Errorf("id is required")
	}
	if params.Status == "" {
		return "", fmt.Errorf("status is required")
	}
	if r.taskCallbacks.UpdateTask == nil {
		return "", fmt.Errorf("task callbacks not configured")
	}
	if err := r.taskCallbacks.UpdateTask(params.ID, params.Status); err != nil {
		return "", err
	}
	return r.taskCallbacks.ReadTasks(), nil
}

func (r *Registry) readTasksTool(_ context.Context, _ json.RawMessage) (string, error) {
	if r.taskCallbacks.ReadTasks == nil {
		return "", fmt.Errorf("task callbacks not configured")
	}
	return r.taskCallbacks.ReadTasks(), nil
}
