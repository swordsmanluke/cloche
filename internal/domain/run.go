package domain

import (
	"strings"
	"time"
)

type RunState string

const (
	RunStatePending   RunState = "pending"
	RunStateRunning   RunState = "running"
	RunStateSucceeded RunState = "succeeded"
	RunStateFailed    RunState = "failed"
	RunStateCancelled RunState = "cancelled"
)

// RunListFilter holds optional filters for listing runs.
type RunListFilter struct {
	ProjectDir string
	State      RunState
	TaskID     string
	AttemptID  string
	Limit      int
	Since      time.Time
}

type StepExecution struct {
	StepName    string
	Result      string
	StartedAt   time.Time
	CompletedAt time.Time
	Logs        string
	GitRef      string // output state
}

func (e *StepExecution) Duration() time.Duration {
	return e.CompletedAt.Sub(e.StartedAt)
}

type Run struct {
	ID             string
	WorkflowName   string
	State          RunState
	ActiveSteps    []string
	StepExecutions []*StepExecution
	StartedAt      time.Time
	CompletedAt    time.Time
	ProjectDir     string
	ErrorMessage   string
	ContainerID    string
	BaseSHA        string // Git HEAD at run start, for result branch creation
	ContainerKept  bool   // true when the container was retained after run completion
	Title          string // One-line summary of the work being done
	IsHost         bool   // true for host workflow runs (vs container runs)
	ParentRunID    string // ID of the parent (host) run, empty for top-level runs
	TaskID         string // optional task ID this run is associated with
	TaskTitle      string // title from the task tracker, for display after the task leaves the active snapshot
	AttemptID      string // ID of the attempt this run belongs to (v2)
}

func NewRun(id, workflowName string) *Run {
	return &Run{
		ID:           id,
		WorkflowName: workflowName,
		State:        RunStatePending,
	}
}

func (r *Run) Start() {
	r.State = RunStateRunning
	r.StartedAt = time.Now()
}

func (r *Run) RecordStepStart(stepName string) {
	r.ActiveSteps = append(r.ActiveSteps, stepName)
	r.StepExecutions = append(r.StepExecutions, &StepExecution{
		StepName:  stepName,
		StartedAt: time.Now(),
	})
}

func (r *Run) RecordStepComplete(stepName, result string) {
	// Remove from active steps
	for i, name := range r.ActiveSteps {
		if name == stepName {
			r.ActiveSteps = append(r.ActiveSteps[:i], r.ActiveSteps[i+1:]...)
			break
		}
	}
	// Mark execution complete
	for i := len(r.StepExecutions) - 1; i >= 0; i-- {
		if r.StepExecutions[i].StepName == stepName && r.StepExecutions[i].CompletedAt.IsZero() {
			r.StepExecutions[i].Result = result
			r.StepExecutions[i].CompletedAt = time.Now()
			return
		}
	}
}

// ActiveStepsString returns a comma-separated representation of active steps
// for backward-compatible serialization.
func (r *Run) ActiveStepsString() string {
	return strings.Join(r.ActiveSteps, ",")
}

// SetActiveStepsFromString parses a comma-separated string into ActiveSteps.
func (r *Run) SetActiveStepsFromString(s string) {
	if s == "" {
		r.ActiveSteps = nil
		return
	}
	r.ActiveSteps = strings.Split(s, ",")
}

func (r *Run) Complete(state RunState) {
	r.State = state
	r.CompletedAt = time.Now()
}

func (r *Run) Fail(msg string) {
	r.State = RunStateFailed
	r.CompletedAt = time.Now()
	r.ErrorMessage = msg
}

// FindFirstFailedStep returns the name of the first step that produced a
// failure result (fail/error) in the run's step executions.
// Returns empty string if no failed step is found.
func (r *Run) FindFirstFailedStep() string {
	for _, exec := range r.StepExecutions {
		if exec.Result == "fail" || exec.Result == "error" {
			return exec.StepName
		}
	}
	return ""
}

// stateSeverity returns a severity score for terminal RunStates.
// Higher values indicate worse outcomes.
func stateSeverity(s RunState) int {
	switch s {
	case RunStateSucceeded:
		return 0
	case RunStateCancelled:
		return 1
	case RunStateFailed:
		return 2
	default:
		return -1
	}
}

// WorseState returns the more severe of two terminal RunStates.
// Failed is worse than cancelled, which is worse than succeeded.
func WorseState(a, b RunState) RunState {
	if stateSeverity(b) > stateSeverity(a) {
		return b
	}
	return a
}

// AttemptAggregateStatus computes the aggregate status for runs within a single
// attempt. Active statuses (running, pending) outweigh terminal ones. Among
// terminal runs, the worst outcome wins: failed > cancelled > succeeded. This
// ensures that if any run in an attempt fails, the attempt is marked failed.
func AttemptAggregateStatus(runs []*Run) RunState {
	if len(runs) == 0 {
		return RunStatePending
	}

	hasRunning := false
	hasPending := false
	for _, r := range runs {
		switch r.State {
		case RunStateRunning:
			hasRunning = true
		case RunStatePending:
			hasPending = true
		}
	}
	if hasRunning {
		return RunStateRunning
	}
	if hasPending {
		return RunStatePending
	}

	// All terminal: use worst state (failed > cancelled > succeeded).
	result := RunStateSucceeded
	for _, r := range runs {
		result = WorseState(result, r.State)
	}
	return result
}

// TaskAggregateStatus computes the aggregate status for a group of runs
// representing attempts at a task. Active statuses (running, pending) outweigh
// terminal ones. Among terminal runs, host runs (which represent the full
// attempt including finalize) take precedence over child container runs.
// If no host runs exist, the most recently started run determines the result.
func TaskAggregateStatus(runs []*Run) RunState {
	if len(runs) == 0 {
		return RunStatePending
	}

	// Active statuses outweigh terminal ones. Prefer running over pending.
	hasRunning := false
	hasPending := false
	for _, r := range runs {
		switch r.State {
		case RunStateRunning:
			hasRunning = true
		case RunStatePending:
			hasPending = true
		}
	}
	if hasRunning {
		return RunStateRunning
	}
	if hasPending {
		return RunStatePending
	}

	// All runs are terminal. Prefer the most recently started host run,
	// since it reflects the full attempt outcome (including finalize).
	// Child container runs start after their parent, so naive most-recent
	// selection would incorrectly pick a succeeded child over a failed host.
	var latestHost *Run
	var latest *Run
	for _, r := range runs {
		if r.IsHost && (latestHost == nil || r.StartedAt.After(latestHost.StartedAt)) {
			latestHost = r
		}
		if latest == nil || r.StartedAt.After(latest.StartedAt) {
			latest = r
		}
	}
	if latestHost != nil {
		return latestHost.State
	}
	return latest.State
}
