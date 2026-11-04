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

// ReposEnvVar carries the repos present in a container to the agent inside it,
// comma-separated. It is set only when the run selected specific repos.
const ReposEnvVar = "CLOCHE_REPOS"

// RepoErrorKey is the KV key holding why a run's repo list couldn't be resolved,
// which the host run reports as its error.
const RepoErrorKey = "repo_error"

// Attempt represents one try at completing a task. Each attempt has a short
// generated ID (4 alphanumeric characters) that is unique within its parent task.
type Attempt struct {
	ID                string
	TaskID            string
	StartedAt         time.Time
	EndedAt           time.Time
	Result            AttemptResult
	PreviousAttemptID string // lineage: ID of the attempt this one was resumed from
	// Repos names the [[repositories]] this attempt's containers need. Empty
	// means every configured repo. Fixed when the attempt is created.
	Repos []string
}

// NewAttempt creates a new running attempt for the given task with a random ID.
// Callers persisting the attempt replace it with ports.AllocateAttemptID.
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

// AttemptResultFromRunState maps a RunState to the corresponding AttemptResult.
func AttemptResultFromRunState(state RunState) AttemptResult {
	switch state {
	case RunStateSucceeded:
		return AttemptResultSucceeded
	case RunStateCancelled:
		return AttemptResultCancelled
	default:
		return AttemptResultFailed
	}
}

// attemptIDAlphabet is the set of characters used for attempt IDs.
const attemptIDAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

// attemptIDLength is the length of an attempt ID. IDs get typed at the CLI, so
// they stay short.
const attemptIDLength = 4

// AttemptIDSpace is the number of distinct attempt IDs: 36^attemptIDLength.
const AttemptIDSpace uint64 = 36 * 36 * 36 * 36

// attemptIDMultiplier scatters consecutive sequence numbers across the ID
// space so neighbouring attempts don't get look-alike IDs. It is coprime with
// AttemptIDSpace (divisible by neither 2 nor 3), which makes multiplying by it
// modulo the space one-to-one. Changing it would reissue IDs already handed out.
const attemptIDMultiplier uint64 = 1038073

// AttemptIDFromSequence returns the attempt ID for a sequence number. Sequence
// numbers below AttemptIDSpace always map to different IDs, so attempts numbered
// from a persistent counter cannot collide.
func AttemptIDFromSequence(seq uint64) string {
	v := seq % AttemptIDSpace * attemptIDMultiplier % AttemptIDSpace
	b := make([]byte, attemptIDLength)
	for i := len(b) - 1; i >= 0; i-- {
		b[i] = attemptIDAlphabet[v%uint64(len(attemptIDAlphabet))]
		v /= uint64(len(attemptIDAlphabet))
	}
	return string(b)
}

// GenerateAttemptID produces a random ID in the attempt ID format. Random IDs of
// this length collide within a few thousand draws, so use it only for short-lived
// names. Attempts get their IDs from ports.AllocateAttemptID.
func GenerateAttemptID() string {
	b := make([]byte, attemptIDLength)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("crypto/rand failed: %v", err))
	}
	for i := range b {
		b[i] = attemptIDAlphabet[int(b[i])%len(attemptIDAlphabet)]
	}
	return string(b)
}
