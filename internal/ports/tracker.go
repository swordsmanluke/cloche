package ports

import "context"

// TrackerTask represents a task from an external task tracker.
type TrackerTask struct {
	ID          string
	Title       string
	Description string
	Acceptance  string
	Labels      []string
	Priority    int
}

// TaskTracker provides access to an external task tracking system.
type TaskTracker interface {
	ListReady(ctx context.Context, project string) ([]TrackerTask, error)
	Claim(ctx context.Context, taskID string) error
	Complete(ctx context.Context, taskID string) error
	Fail(ctx context.Context, taskID string) error
}
