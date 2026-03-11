package host

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// Task represents a work item from an external task tracker.
type Task struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

// TaskAssigner lists available tasks for the daemon to assign to workflow runs.
type TaskAssigner interface {
	ListTasks(ctx context.Context, projectDir string) ([]Task, error)
}

// ScriptTaskAssigner runs a shell command to list tasks and parses the JSON output.
// The command should output a JSON array of objects with at least an "id" field.
type ScriptTaskAssigner struct {
	Command string // shell command to run (e.g., "bash .cloche/scripts/ready-tasks.sh")
}

// ListTasks executes the configured command from the project directory and
// parses its stdout as a JSON array of Task objects.
func (s *ScriptTaskAssigner) ListTasks(ctx context.Context, projectDir string) ([]Task, error) {
	cmd := exec.CommandContext(ctx, "sh", "-c", s.Command)
	cmd.Dir = MainWorktreeDir(projectDir)

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("running list-tasks command: %w", err)
	}

	output := strings.TrimSpace(string(out))
	if output == "" || output == "[]" {
		return nil, nil
	}

	var tasks []Task
	if err := json.Unmarshal([]byte(output), &tasks); err != nil {
		return nil, fmt.Errorf("parsing list-tasks output: %w", err)
	}
	return tasks, nil
}
