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

type StepExecution struct {
	StepName    string
	Result      string
	StartedAt   time.Time
	CompletedAt time.Time
	Logs          string
	GitRef        string // output state
	PromptText    string
	AgentOutput   string
	AttemptNumber int
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
