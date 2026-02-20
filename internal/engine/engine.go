package engine

import (
	"context"
	"fmt"

	"github.com/cloche-dev/cloche/internal/domain"
)

// StepExecutor executes a single step and returns the result name.
type StepExecutor interface {
	Execute(ctx context.Context, step *domain.Step) (string, error)
}

// StepExecutorFunc adapts a function to the StepExecutor interface.
type StepExecutorFunc func(ctx context.Context, step *domain.Step) (string, error)

func (f StepExecutorFunc) Execute(ctx context.Context, step *domain.Step) (string, error) {
	return f(ctx, step)
}

// StatusHandler receives notifications about workflow execution progress.
type StatusHandler interface {
	OnStepStart(run *domain.Run, step *domain.Step)
	OnStepComplete(run *domain.Run, step *domain.Step, result string)
	OnRunComplete(run *domain.Run)
}

type noopStatus struct{}

func (noopStatus) OnStepStart(*domain.Run, *domain.Step)            {}
func (noopStatus) OnStepComplete(*domain.Run, *domain.Step, string) {}
func (noopStatus) OnRunComplete(*domain.Run)                        {}

type Engine struct {
	executor StepExecutor
	status   StatusHandler
	maxSteps int
}

func New(executor StepExecutor) *Engine {
	return &Engine{
		executor: executor,
		status:   noopStatus{},
		maxSteps: 1000,
	}
}

func (e *Engine) SetStatusHandler(h StatusHandler) {
	e.status = h
}

func (e *Engine) SetMaxSteps(n int) {
	e.maxSteps = n
}

func (e *Engine) Run(ctx context.Context, wf *domain.Workflow) (*domain.Run, error) {
	if err := wf.Validate(); err != nil {
		return nil, fmt.Errorf("invalid workflow: %w", err)
	}

	run := domain.NewRun(generateRunID(), wf.Name)
	run.Start()

	currentStepName := wf.EntryStep
	stepCount := 0

	for currentStepName != domain.StepDone && currentStepName != domain.StepAbort {
		if err := ctx.Err(); err != nil {
			run.Complete(domain.RunStateCancelled)
			return run, fmt.Errorf("workflow cancelled: %w", err)
		}

		stepCount++
		if stepCount > e.maxSteps {
			run.Complete(domain.RunStateFailed)
			return run, fmt.Errorf("workflow exceeded maximum step count (%d)", e.maxSteps)
		}

		step, ok := wf.Steps[currentStepName]
		if !ok {
			run.Complete(domain.RunStateFailed)
			return run, fmt.Errorf("step %q not found in workflow", currentStepName)
		}

		run.RecordStepStart(step.Name)
		e.status.OnStepStart(run, step)

		result, err := e.executor.Execute(ctx, step)
		if err != nil {
			run.RecordStepComplete(step.Name, "error")
			run.Complete(domain.RunStateFailed)
			return run, fmt.Errorf("step %q execution failed: %w", step.Name, err)
		}

		run.RecordStepComplete(step.Name, result)
		e.status.OnStepComplete(run, step, result)

		nextStep, err := wf.NextStep(currentStepName, result)
		if err != nil {
			run.Complete(domain.RunStateFailed)
			return run, err
		}

		currentStepName = nextStep
	}

	if currentStepName == domain.StepDone {
		run.Complete(domain.RunStateSucceeded)
	} else {
		run.Complete(domain.RunStateFailed)
	}

	e.status.OnRunComplete(run)
	return run, nil
}

var runCounter int

func generateRunID() string {
	runCounter++
	return fmt.Sprintf("run-%d", runCounter)
}
