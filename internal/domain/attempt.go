package domain

import (
	"crypto/rand"
	"fmt"
	"time"
)

// AttemptResult describes the outcome of an attempt.
type AttemptResult string

const (
	AttemptResultRunning   AttemptResult = "running"
	AttemptResultSucceeded AttemptResult = "succeeded"
	AttemptResultFailed    AttemptResult = "failed"
	AttemptResultCancelled AttemptResult = "cancelled"
)

// Attempt represents one try at completing a task. Each attempt has a short
// generated ID (4 alphanumeric characters) that is unique within its parent task.
type Attempt struct {
	ID        string
	TaskID    string
	StartedAt time.Time
	EndedAt   time.Time
	Result    AttemptResult
}

// NewAttempt creates a new running attempt for the given task with a random ID.
func NewAttempt(taskID string) *Attempt {
	return &Attempt{
		ID:        GenerateAttemptID(),
		TaskID:    taskID,
		StartedAt: time.Now(),
		Result:    AttemptResultRunning,
	}
}

// Complete marks the attempt as finished with the given result.
func (a *Attempt) Complete(result AttemptResult) {
	a.Result = result
	a.EndedAt = time.Now()
}

// IsTerminal returns true if the attempt has reached a final state.
func (a *Attempt) IsTerminal() bool {
	switch a.Result {
	case AttemptResultSucceeded, AttemptResultFailed, AttemptResultCancelled:
		return true
	default:
		return false
	}
}

// Duration returns the elapsed time. If the attempt is still running,
// it returns the time since start.
func (a *Attempt) Duration() time.Duration {
	if a.EndedAt.IsZero() {
		return time.Since(a.StartedAt)
	}
	return a.EndedAt.Sub(a.StartedAt)
}

// attemptIDAlphabet is the set of characters used for attempt IDs.
const attemptIDAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

// GenerateAttemptID produces a random 4-character alphanumeric ID.
func GenerateAttemptID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("crypto/rand failed: %v", err))
	}
	for i := range b {
		b[i] = attemptIDAlphabet[int(b[i])%len(attemptIDAlphabet)]
	}
	return string(b)
}
