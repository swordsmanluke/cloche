package domain

import "time"

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
	CurrentStep    string
	StepExecutions []*StepExecution
	StartedAt      time.Time
	CompletedAt    time.Time
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
	r.CurrentStep = stepName
	r.StepExecutions = append(r.StepExecutions, &StepExecution{
		StepName:  stepName,
		StartedAt: time.Now(),
	})
}

func (r *Run) RecordStepComplete(stepName, result string) {
	for i := len(r.StepExecutions) - 1; i >= 0; i-- {
		if r.StepExecutions[i].StepName == stepName && r.StepExecutions[i].CompletedAt.IsZero() {
			r.StepExecutions[i].Result = result
			r.StepExecutions[i].CompletedAt = time.Now()
			return
		}
	}
}

func (r *Run) Complete(state RunState) {
	r.State = state
	r.CompletedAt = time.Now()
}
