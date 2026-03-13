package engine_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeExecutor struct {
	mu      sync.Mutex
	results map[string]string
	called  []string
}

func (f *fakeExecutor) Execute(_ context.Context, step *domain.Step) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
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

func TestEngine_Fanout(t *testing.T) {
	wf := &domain.Workflow{
		Name: "fanout",
		Steps: map[string]*domain.Step{
			"code": {Name: "code", Type: domain.StepTypeAgent, Results: []string{"success"}},
			"test": {Name: "test", Type: domain.StepTypeScript, Results: []string{"success"}},
			"lint": {Name: "lint", Type: domain.StepTypeScript, Results: []string{"success"}},
		},
		Wiring: []domain.Wire{
			{From: "code", Result: "success", To: "test"},
			{From: "code", Result: "success", To: "lint"},
			{From: "test", Result: "success", To: domain.StepDone},
			{From: "lint", Result: "success", To: domain.StepDone},
		},
		EntryStep: "code",
	}

	exec := &fakeExecutor{results: map[string]string{
		"code": "success", "test": "success", "lint": "success",
	}}
	eng := engine.New(exec)

	run, err := eng.Run(context.Background(), wf)
	require.NoError(t, err)
	assert.Equal(t, domain.RunStateSucceeded, run.State)
	// All three steps must have been called
	exec.mu.Lock()
	assert.Contains(t, exec.called, "code")
	assert.Contains(t, exec.called, "test")
	assert.Contains(t, exec.called, "lint")
	exec.mu.Unlock()
}

func TestEngine_CollectAll(t *testing.T) {
	wf := &domain.Workflow{
		Name: "collect-all",
		Steps: map[string]*domain.Step{
			"code":  {Name: "code", Type: domain.StepTypeAgent, Results: []string{"success"}},
			"test":  {Name: "test", Type: domain.StepTypeScript, Results: []string{"success"}},
			"lint":  {Name: "lint", Type: domain.StepTypeScript, Results: []string{"success"}},
			"merge": {Name: "merge", Type: domain.StepTypeScript, Results: []string{"success"}},
		},
		Wiring: []domain.Wire{
			{From: "code", Result: "success", To: "test"},
			{From: "code", Result: "success", To: "lint"},
			{From: "merge", Result: "success", To: domain.StepDone},
		},
		Collects: []domain.Collect{
			{
				Mode: domain.CollectAll,
				Conditions: []domain.WireCondition{
					{Step: "test", Result: "success"},
					{Step: "lint", Result: "success"},
				},
				To: "merge",
			},
		},
		EntryStep: "code",
	}

	exec := &fakeExecutor{results: map[string]string{
		"code": "success", "test": "success", "lint": "success", "merge": "success",
	}}
	eng := engine.New(exec)

	run, err := eng.Run(context.Background(), wf)
	require.NoError(t, err)
	assert.Equal(t, domain.RunStateSucceeded, run.State)
	exec.mu.Lock()
	assert.Contains(t, exec.called, "merge")
	exec.mu.Unlock()
}

func TestEngine_CollectAny(t *testing.T) {
	wf := &domain.Workflow{
		Name: "collect-any",
		Steps: map[string]*domain.Step{
			"code":  {Name: "code", Type: domain.StepTypeAgent, Results: []string{"success"}},
			"test":  {Name: "test", Type: domain.StepTypeScript, Results: []string{"success"}},
			"lint":  {Name: "lint", Type: domain.StepTypeScript, Results: []string{"success"}},
			"quick": {Name: "quick", Type: domain.StepTypeScript, Results: []string{"success"}},
		},
		Wiring: []domain.Wire{
			{From: "code", Result: "success", To: "test"},
			{From: "code", Result: "success", To: "lint"},
			{From: "test", Result: "success", To: domain.StepDone},
			{From: "lint", Result: "success", To: domain.StepDone},
			{From: "quick", Result: "success", To: domain.StepDone},
		},
		Collects: []domain.Collect{
			{
				Mode: domain.CollectAny,
				Conditions: []domain.WireCondition{
					{Step: "test", Result: "success"},
					{Step: "lint", Result: "success"},
				},
				To: "quick",
			},
		},
		EntryStep: "code",
	}

	exec := &fakeExecutor{results: map[string]string{
		"code": "success", "test": "success", "lint": "success", "quick": "success",
	}}
	eng := engine.New(exec)

	run, err := eng.Run(context.Background(), wf)
	require.NoError(t, err)
	assert.Equal(t, domain.RunStateSucceeded, run.State)
	exec.mu.Lock()
	assert.Contains(t, exec.called, "quick")
	exec.mu.Unlock()
}

func TestEngine_UndeclaredResultAborts(t *testing.T) {
	wf := &domain.Workflow{
		Name: "undeclared",
		Steps: map[string]*domain.Step{
			"code": {Name: "code", Type: domain.StepTypeAgent, Results: []string{"success", "fail"}},
		},
		Wiring: []domain.Wire{
			{From: "code", Result: "success", To: domain.StepDone},
			{From: "code", Result: "fail", To: domain.StepAbort},
		},
		EntryStep: "code",
	}

	exec := &fakeExecutor{results: map[string]string{"code": "unknown"}}
	eng := engine.New(exec)

	run, err := eng.Run(context.Background(), wf)
	require.Error(t, err)
	assert.Equal(t, domain.RunStateFailed, run.State)
	assert.Contains(t, err.Error(), "undeclared")
}

// slowExecutor blocks until the context is cancelled, simulating a hanging agent.
type slowExecutor struct {
	mu     sync.Mutex
	called []string
}

func (s *slowExecutor) Execute(ctx context.Context, step *domain.Step) (string, error) {
	s.mu.Lock()
	s.called = append(s.called, step.Name)
	s.mu.Unlock()
	<-ctx.Done()
	return "", fmt.Errorf("step %q timed out: %w", step.Name, ctx.Err())
}

func TestEngine_StepTimeout(t *testing.T) {
	wf := &domain.Workflow{
		Name: "timeout-test",
		Steps: map[string]*domain.Step{
			"hang": {
				Name:    "hang",
				Type:    domain.StepTypeAgent,
				Results: []string{"success", "fail"},
				Config:  map[string]string{"prompt": "do stuff", "timeout": "100ms"},
			},
		},
		Wiring: []domain.Wire{
			{From: "hang", Result: "success", To: domain.StepDone},
			{From: "hang", Result: "fail", To: domain.StepAbort},
		},
		EntryStep: "hang",
	}

	exec := &slowExecutor{}
	eng := engine.New(exec)

	start := time.Now()
	run, err := eng.Run(context.Background(), wf)
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.Equal(t, domain.RunStateFailed, run.State)
	assert.Contains(t, err.Error(), "timed out")
	assert.Less(t, elapsed, 5*time.Second, "timeout should fire quickly, not wait for default 30m")
}

func TestEngine_StepTimeoutOverridesDefault(t *testing.T) {
	wf := &domain.Workflow{
		Name: "timeout-override",
		Steps: map[string]*domain.Step{
			"hang": {
				Name:    "hang",
				Type:    domain.StepTypeAgent,
				Results: []string{"success", "fail"},
				Config:  map[string]string{"prompt": "do stuff"}, // no per-step timeout
			},
		},
		Wiring: []domain.Wire{
			{From: "hang", Result: "success", To: domain.StepDone},
			{From: "hang", Result: "fail", To: domain.StepAbort},
		},
		EntryStep: "hang",
	}

	exec := &slowExecutor{}
	eng := engine.New(exec)
	eng.SetDefaultTimeout(100 * time.Millisecond)

	start := time.Now()
	run, err := eng.Run(context.Background(), wf)
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.Equal(t, domain.RunStateFailed, run.State)
	assert.Less(t, elapsed, 5*time.Second, "default timeout should fire quickly")
}

func TestEngine_StepErrorIncludesStepName(t *testing.T) {
	wf := &domain.Workflow{
		Name: "error-info",
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

	// Executor that returns an error on the "test" step
	exec := engine.StepExecutorFunc(func(_ context.Context, step *domain.Step) (string, error) {
		if step.Name == "test" {
			return "", fmt.Errorf("tests failed: 3 failures")
		}
		return "success", nil
	})

	eng := engine.New(exec)
	run, err := eng.Run(context.Background(), wf)

	require.Error(t, err)
	assert.Equal(t, domain.RunStateFailed, run.State)
	assert.Contains(t, err.Error(), "test", "error should contain the failed step name")
	assert.Contains(t, err.Error(), "tests failed", "error should contain the original error message")

	// Verify the step execution was recorded with "error" result
	var testExec *domain.StepExecution
	for _, se := range run.StepExecutions {
		if se.StepName == "test" {
			testExec = se
			break
		}
	}
	require.NotNil(t, testExec, "test step should be in step executions")
	assert.Equal(t, "error", testExec.Result, "failed step should have 'error' result")
}
