package engine

import (
	"context"

	"github.com/cloche-dev/cloche/internal/domain"
)

type contextKey int

const (
	stepTriggerKey contextKey = iota
	workflowKey
)

// StepTrigger carries information about the step and result that triggered
// the current step to run.
type StepTrigger struct {
	PrevStep   string
	PrevResult string
}

// WithStepTrigger returns a new context with the given StepTrigger attached.
func WithStepTrigger(ctx context.Context, t StepTrigger) context.Context {
	return context.WithValue(ctx, stepTriggerKey, t)
}

// GetStepTrigger retrieves the StepTrigger from the context, if present.
func GetStepTrigger(ctx context.Context) (StepTrigger, bool) {
	t, ok := ctx.Value(stepTriggerKey).(StepTrigger)
	return t, ok
}

// WithWorkflow returns a new context with the given Workflow attached.
// The engine sets this before calling StepExecutor.Execute so that executors
// can inspect the workflow location and configuration.
func WithWorkflow(ctx context.Context, wf *domain.Workflow) context.Context {
	return context.WithValue(ctx, workflowKey, wf)
}

// WorkflowFromContext retrieves the Workflow from the context, if present.
func WorkflowFromContext(ctx context.Context) (*domain.Workflow, bool) {
	wf, ok := ctx.Value(workflowKey).(*domain.Workflow)
	return wf, ok
}
