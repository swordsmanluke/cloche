package host

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// TaskStatus represents the state of a task from a task tracker.
type TaskStatus string

const (
	TaskStatusOpen       TaskStatus = "open"
	TaskStatusClosed     TaskStatus = "closed"
	TaskStatusInProgress TaskStatus = "in-progress"
)

// Task represents a work item from an external task tracker.
type Task struct {
	ID          string            `json:"id"`
	Status      string            `json:"status"`
	Title       string            `json:"title"`
	Description string            `json:"description"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// IsOpen returns true if the task status is "open" or empty (for backward compatibility).
func (t Task) IsOpen() bool {
	return t.Status == "" || TaskStatus(t.Status) == TaskStatusOpen
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

// ParseTasksJSONL parses JSONL-formatted task output (one JSON object per line).
// Each line should have at least an "id" field. Lines that are empty or fail to
// parse are skipped.
func ParseTasksJSONL(data string) ([]Task, error) {
	var tasks []Task
	scanner := bufio.NewScanner(strings.NewReader(data))
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var task Task
		if err := json.Unmarshal([]byte(line), &task); err != nil {
			return nil, fmt.Errorf("parsing JSONL line %d: %w", lineNum, err)
		}
		tasks = append(tasks, task)
	}
	return tasks, nil
}
