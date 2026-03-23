// Package activitylog records task/attempt/step lifecycle events with
// timestamps and outcomes. Entries are persisted via an ActivityStore
// (backed by the daemon's SQLite database) rather than per-project files.
package activitylog

import (
	"context"
	"time"
)

// EventKind describes the type of event recorded in the activity log.
type EventKind string

const (
	// KindAttemptStarted is written when the orchestration loop begins a new attempt.
	KindAttemptStarted EventKind = "attempt_started"
	// KindAttemptEnded is written when an attempt reaches a terminal state.
	KindAttemptEnded EventKind = "attempt_ended"
	// KindStepStarted is written when a workflow step begins executing.
	KindStepStarted EventKind = "step_started"
	// KindStepCompleted is written when a workflow step produces a result.
	KindStepCompleted EventKind = "step_completed"
)

// Entry is one record in the activity log. Fields are omitted when empty.
type Entry struct {
	Timestamp    time.Time `json:"ts"`
	Kind         EventKind `json:"kind"`
	TaskID       string    `json:"task_id,omitempty"`
	AttemptID    string    `json:"attempt_id,omitempty"`
	WorkflowName string    `json:"workflow,omitempty"`
	StepName     string    `json:"step,omitempty"`
	// Result is set for KindStepCompleted entries (e.g. "success", "fail").
	Result string `json:"result,omitempty"`
	// State is set for KindAttemptEnded entries (e.g. "succeeded", "failed").
	State string `json:"state,omitempty"`
}

// ReadOptions controls optional time-range filtering for activity log reads.
type ReadOptions struct {
	// Since, when non-zero, excludes entries before this time.
	Since time.Time
	// Until, when non-zero, excludes entries after this time.
	Until time.Time
}

// Appender is the interface used by Logger to persist entries.
// The sqlite store implements this interface.
type Appender interface {
	AppendActivityEntry(ctx context.Context, projectDir string, entry Entry) error
}

// Logger appends activity log entries for a specific project directory.
// It is safe for concurrent use (delegating to the underlying store).
type Logger struct {
	projectDir string
	appender   Appender
}

// NewLogger returns a Logger that writes to appender for projectDir.
func NewLogger(projectDir string, appender Appender) *Logger {
	return &Logger{projectDir: projectDir, appender: appender}
}

// Append writes entry to the store. Timestamp is set to now if zero.
// Errors are non-fatal; the caller should log them but not fail the workflow.
func (l *Logger) Append(entry Entry) error {
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now()
	}
	return l.appender.AppendActivityEntry(context.Background(), l.projectDir, entry)
}
