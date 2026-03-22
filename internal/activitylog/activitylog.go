// Package activitylog writes and reads project-owned activity log files that
// record task/attempt/step lifecycle events with timestamps and outcomes.
// Each project stores its activity log at .cloche/activity.log (JSONL format).
package activitylog

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
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

// Logger appends activity log entries to a project's .cloche/activity.log file.
// It is safe for concurrent use.
type Logger struct {
	path string
	mu   sync.Mutex
}

// NewLogger returns a Logger that writes to .cloche/activity.log inside projectDir.
func NewLogger(projectDir string) *Logger {
	return &Logger{
		path: filepath.Join(projectDir, ".cloche", "activity.log"),
	}
}

// Append writes entry to the log file, creating it (and its parent directory)
// if it does not exist. Timestamp is set to now if zero. Errors are non-fatal
// (the caller should log them but not fail the workflow).
func (l *Logger) Append(entry Entry) error {
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now()
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("activitylog: marshaling entry: %w", err)
	}
	data = append(data, '\n')

	l.mu.Lock()
	defer l.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(l.path), 0755); err != nil {
		return fmt.Errorf("activitylog: creating log dir: %w", err)
	}
	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("activitylog: opening log file: %w", err)
	}
	defer f.Close()
	_, err = f.Write(data)
	return err
}

// ReadOptions controls optional time-range filtering for Read.
type ReadOptions struct {
	// Since, when non-zero, excludes entries before this time.
	Since time.Time
	// Until, when non-zero, excludes entries after this time.
	Until time.Time
}

// Read returns activity log entries for projectDir, optionally filtered by the
// time range in opts. Returns an empty slice (not an error) when the log file
// does not exist yet. Malformed lines are silently skipped.
func Read(projectDir string, opts ReadOptions) ([]Entry, error) {
	path := filepath.Join(projectDir, ".cloche", "activity.log")
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("activitylog: opening log: %w", err)
	}
	defer f.Close()

	var entries []Entry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			continue // skip malformed lines
		}
		if !opts.Since.IsZero() && e.Timestamp.Before(opts.Since) {
			continue
		}
		if !opts.Until.IsZero() && e.Timestamp.After(opts.Until) {
			continue
		}
		entries = append(entries, e)
	}
	return entries, scanner.Err()
}
