package agent

import (
	"fmt"
	"strings"
	"time"

	"github.com/lowkaihon/cli-coding-agent/tools"
)

// Task represents a tracked work item created by the LLM for planning.
type Task struct {
	ID         int       `json:"id"`
	Content    string    `json:"content"`     // imperative: "Add auth middleware"
	Status     string    `json:"status"`      // pending, in_progress, completed
	ActiveForm string    `json:"active_form"` // continuous: "Adding auth middleware"
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// WriteTasks replaces the entire task list with new tasks, auto-assigning IDs.
func (a *Agent) WriteTasks(inputs []tools.TaskInput) string {
	now := time.Now()
	a.tasks = make([]Task, len(inputs))
	for i, in := range inputs {
		a.tasks[i] = Task{
			ID:         i + 1,
			Content:    in.Content,
			Status:     "pending",
			ActiveForm: in.ActiveForm,
			CreatedAt:  now,
			UpdatedAt:  now,
		}
	}
	return a.TaskSummary()
}

// UpdateTask changes the status of a single task by ID.
func (a *Agent) UpdateTask(id int, status string) error {
	switch status {
	case "pending", "in_progress", "completed":
	default:
		return fmt.Errorf("invalid status %q (must be pending, in_progress, or completed)", status)
	}
	for i := range a.tasks {
		if a.tasks[i].ID == id {
			a.tasks[i].Status = status
			a.tasks[i].UpdatedAt = time.Now()
			return nil
		}
	}
	return fmt.Errorf("task %d not found", id)
}

// Tasks returns the current task list.
func (a *Agent) Tasks() []Task {
	return a.tasks
}

// TaskSummary returns a formatted text summary of all tasks.
func (a *Agent) TaskSummary() string {
	if len(a.tasks) == 0 {
		return "No tasks."
	}

	var sb strings.Builder
	pending, inProgress, completed := 0, 0, 0
	for _, t := range a.tasks {
		switch t.Status {
		case "pending":
			pending++
			fmt.Fprintf(&sb, "  [ ] %d. %s\n", t.ID, t.Content)
		case "in_progress":
			inProgress++
			fmt.Fprintf(&sb, "  [~] %d. %s\n", t.ID, t.Content)
		case "completed":
			completed++
			fmt.Fprintf(&sb, "  [x] %d. %s\n", t.ID, t.Content)
		}
	}
	fmt.Fprintf(&sb, "\n%d tasks (%d pending, %d in progress, %d completed)",
		len(a.tasks), pending, inProgress, completed)
	return sb.String()
}
