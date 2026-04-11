package domain

import "time"

// TaskStatus represents the status of a task, derived from its latest attempt.
type TaskStatus string

const (
	TaskStatusPending   TaskStatus = "pending"
	TaskStatusRunning   TaskStatus = "running"
	TaskStatusWaiting   TaskStatus = "waiting" // blocked at a human step
	TaskStatusSucceeded TaskStatus = "succeeded"
	TaskStatusFailed    TaskStatus = "failed"
	TaskStatusCancelled TaskStatus = "cancelled"
)

// TaskSource indicates how a task was created.
type TaskSource string

const (
	TaskSourceExternal      TaskSource = "external"
	TaskSourceUserInitiated TaskSource = "user-initiated"
)

// Task is the top-level work unit. It may come from an external task tracker
// or be created by a manual `cloche run` invocation.
type Task struct {
	ID         string
	Title      string
	Status     TaskStatus // derived from latest attempt
	Source     TaskSource
	ProjectDir string
	CreatedAt  time.Time
	Attempts   []*Attempt
}

// DeriveStatus computes the task status from the latest attempt's result.
// If there are no attempts, the status is pending.
func (t *Task) DeriveStatus() TaskStatus {
	if len(t.Attempts) == 0 {
		return TaskStatusPending
	}
	latest := t.Attempts[len(t.Attempts)-1]
	return TaskStatusFromAttemptResult(latest.Result)
}

// LatestAttempt returns the most recent attempt, or nil if there are none.
func (t *Task) LatestAttempt() *Attempt {
	if len(t.Attempts) == 0 {
		return nil
	}
	return t.Attempts[len(t.Attempts)-1]
}

// TaskStatusFromAttemptResult maps an AttemptResult to a TaskStatus.
func TaskStatusFromAttemptResult(r AttemptResult) TaskStatus {
	switch r {
	case AttemptResultRunning:
		return TaskStatusRunning
	case AttemptResultSucceeded:
		return TaskStatusSucceeded
	case AttemptResultFailed:
		return TaskStatusFailed
	case AttemptResultCancelled:
		return TaskStatusCancelled
	default:
		return TaskStatusPending
	}
}
