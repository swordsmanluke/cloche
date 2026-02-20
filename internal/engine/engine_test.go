package engine_test

import (
	"context"
	"testing"

	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeExecutor struct {
	results map[string]string
	called  []string
}

func (f *fakeExecutor) Execute(_ context.Context, step *domain.Step) (string, error) {
	f.called = append(f.called, step.Name)
	return f.results[step.Name], nil
}

func TestEngine_LinearWorkflow(t *testing.T) {
	wf := &domain.Workflow{
		Name: "linear",
		Steps: map[string]*domain.Step{
			"build": {Name: "build", Type: domain.StepTypeScript, Results: []string{"success"}},
			"test":  {Name: "test", Type: domain.StepTypeScript, Results: []string{"pass"}},
		},
		Wiring: []domain.Wire{
			{From: "build", Result: "success", To: "test"},
			{From: "test", Result: "pass", To: domain.StepDone},
		},
		EntryStep: "build",
	}

	exec := &fakeExecutor{results: map[string]string{"build": "success", "test": "pass"}}
	eng := engine.New(exec)

	run, err := eng.Run(context.Background(), wf)
	require.NoError(t, err)
	assert.Equal(t, domain.RunStateSucceeded, run.State)
	assert.Equal(t, []string{"build", "test"}, exec.called)
}

func TestEngine_RetryLoop(t *testing.T) {
	wf := &domain.Workflow{
		Name: "retry",
		Steps: map[string]*domain.Step{
			"code":  {Name: "code", Type: domain.StepTypeAgent, Results: []string{"success", "fail"}},
			"check": {Name: "check", Type: domain.StepTypeScript, Results: []string{"pass", "fail"}},
		},
		Wiring: []domain.Wire{
			{From: "code", Result: "success", To: "check"},
			{From: "code", Result: "fail", To: domain.StepAbort},
			{From: "check", Result: "pass", To: domain.StepDone},
			{From: "check", Result: "fail", To: "code"},
		},
		EntryStep: "code",
	}

	callCount := 0
	dynamicExec := engine.StepExecutorFunc(func(_ context.Context, step *domain.Step) (string, error) {
		callCount++
		if step.Name == "check" && callCount <= 2 {
			return "fail", nil
		}
		if step.Name == "code" {
			return "success", nil
		}
		return "pass", nil
	})

	eng := engine.New(dynamicExec)
	run, err := eng.Run(context.Background(), wf)
	require.NoError(t, err)
	assert.Equal(t, domain.RunStateSucceeded, run.State)
}

func TestEngine_Abort(t *testing.T) {
	wf := &domain.Workflow{
		Name: "abort-test",
		Steps: map[string]*domain.Step{
			"code": {Name: "code", Type: domain.StepTypeAgent, Results: []string{"success", "fail"}},
		},
		Wiring: []domain.Wire{
			{From: "code", Result: "success", To: domain.StepDone},
			{From: "code", Result: "fail", To: domain.StepAbort},
		},
		EntryStep: "code",
	}

	exec := &fakeExecutor{results: map[string]string{"code": "fail"}}
	eng := engine.New(exec)

	run, err := eng.Run(context.Background(), wf)
	require.NoError(t, err)
	assert.Equal(t, domain.RunStateFailed, run.State)
}

func TestEngine_ContextCancellation(t *testing.T) {
	wf := &domain.Workflow{
		Name: "cancel-test",
		Steps: map[string]*domain.Step{
			"slow": {Name: "slow", Type: domain.StepTypeScript, Results: []string{"done"}},
		},
		Wiring: []domain.Wire{
			{From: "slow", Result: "done", To: domain.StepDone},
		},
		EntryStep: "slow",
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	exec := &fakeExecutor{results: map[string]string{"slow": "done"}}
	eng := engine.New(exec)

	run, err := eng.Run(ctx, wf)
	require.Error(t, err)
	assert.Equal(t, domain.RunStateCancelled, run.State)
}
