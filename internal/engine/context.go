package engine

import "context"

type contextKey int

const stepTriggerKey contextKey = iota

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
