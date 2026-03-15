package engine_test

import (
	"context"
	"testing"

	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEngine_ResumeFromStep(t *testing.T) {
	// Workflow: build -> test -> deploy
	// build succeeded, test failed. Resume from test.
	wf := &domain.Workflow{
		Name: "resume-linear",
		Steps: map[string]*domain.Step{
			"build":  {Name: "build", Type: domain.StepTypeScript, Results: []string{"success"}},
			"test":   {Name: "test", Type: domain.StepTypeScript, Results: []string{"pass", "fail"}},
			"deploy": {Name: "deploy", Type: domain.StepTypeScript, Results: []string{"success"}},
		},
		Wiring: []domain.Wire{
			{From: "build", Result: "success", To: "test"},
			{From: "test", Result: "pass", To: "deploy"},
			{From: "test", Result: "fail", To: domain.StepAbort},
			{From: "deploy", Result: "success", To: domain.StepDone},
		},
		EntryStep: "build",
	}

	exec := &fakeExecutor{results: map[string]string{
		"build": "success", "test": "pass", "deploy": "success",
	}}
	eng := engine.New(exec)

	// Preload build as completed — test and deploy should actually execute
	eng.SetPreloadedResults(map[string]string{
		"build": "success",
	})

	run, err := eng.Run(context.Background(), wf)
	require.NoError(t, err)
	assert.Equal(t, domain.RunStateSucceeded, run.State)

	// build should NOT have been called (preloaded)
	exec.mu.Lock()
	assert.NotContains(t, exec.called, "build")
	assert.Contains(t, exec.called, "test")
	assert.Contains(t, exec.called, "deploy")
	exec.mu.Unlock()
}

func TestEngine_ResumeSkipsMultipleSteps(t *testing.T) {
	// Workflow: a -> b -> c -> d
	// a and b succeeded. Resume from c.
	wf := &domain.Workflow{
		Name: "resume-multi",
		Steps: map[string]*domain.Step{
			"a": {Name: "a", Type: domain.StepTypeScript, Results: []string{"ok"}},
			"b": {Name: "b", Type: domain.StepTypeScript, Results: []string{"ok"}},
			"c": {Name: "c", Type: domain.StepTypeScript, Results: []string{"ok"}},
			"d": {Name: "d", Type: domain.StepTypeScript, Results: []string{"ok"}},
		},
		Wiring: []domain.Wire{
			{From: "a", Result: "ok", To: "b"},
			{From: "b", Result: "ok", To: "c"},
			{From: "c", Result: "ok", To: "d"},
			{From: "d", Result: "ok", To: domain.StepDone},
		},
		EntryStep: "a",
	}

	exec := &fakeExecutor{results: map[string]string{
		"a": "ok", "b": "ok", "c": "ok", "d": "ok",
	}}
	eng := engine.New(exec)
	eng.SetPreloadedResults(map[string]string{
		"a": "ok",
		"b": "ok",
	})

	run, err := eng.Run(context.Background(), wf)
	require.NoError(t, err)
	assert.Equal(t, domain.RunStateSucceeded, run.State)

	exec.mu.Lock()
	assert.NotContains(t, exec.called, "a")
	assert.NotContains(t, exec.called, "b")
	assert.Contains(t, exec.called, "c")
	assert.Contains(t, exec.called, "d")
	exec.mu.Unlock()
}

func TestEngine_ResumeWithFanout(t *testing.T) {
	// Workflow: code -> test, code -> lint
	// code succeeded (preloaded). Both test and lint should execute.
	wf := &domain.Workflow{
		Name: "resume-fanout",
		Steps: map[string]*domain.Step{
			"code": {Name: "code", Type: domain.StepTypeAgent, Results: []string{"success"}},
			"test": {Name: "test", Type: domain.StepTypeScript, Results: []string{"pass"}},
			"lint": {Name: "lint", Type: domain.StepTypeScript, Results: []string{"pass"}},
		},
		Wiring: []domain.Wire{
			{From: "code", Result: "success", To: "test"},
			{From: "code", Result: "success", To: "lint"},
			{From: "test", Result: "pass", To: domain.StepDone},
			{From: "lint", Result: "pass", To: domain.StepDone},
		},
		EntryStep: "code",
	}

	exec := &fakeExecutor{results: map[string]string{
		"code": "success", "test": "pass", "lint": "pass",
	}}
	eng := engine.New(exec)
	eng.SetPreloadedResults(map[string]string{
		"code": "success",
	})

	run, err := eng.Run(context.Background(), wf)
	require.NoError(t, err)
	assert.Equal(t, domain.RunStateSucceeded, run.State)

	exec.mu.Lock()
	assert.NotContains(t, exec.called, "code")
	assert.Contains(t, exec.called, "test")
	assert.Contains(t, exec.called, "lint")
	exec.mu.Unlock()
}

func TestEngine_ResumePreloadedStepsRecordedInRun(t *testing.T) {
	// Verify that preloaded steps still appear in the run's StepExecutions
	wf := &domain.Workflow{
		Name: "resume-recorded",
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

	exec := &fakeExecutor{results: map[string]string{
		"build": "success", "test": "pass",
	}}
	eng := engine.New(exec)
	eng.SetPreloadedResults(map[string]string{
		"build": "success",
	})

	run, err := eng.Run(context.Background(), wf)
	require.NoError(t, err)
	assert.Equal(t, domain.RunStateSucceeded, run.State)

	// Both steps should appear in step executions
	var buildExec, testExec *domain.StepExecution
	for _, se := range run.StepExecutions {
		switch se.StepName {
		case "build":
			buildExec = se
		case "test":
			testExec = se
		}
	}
	require.NotNil(t, buildExec, "build step should be recorded")
	assert.Equal(t, "success", buildExec.Result)
	require.NotNil(t, testExec, "test step should be recorded")
	assert.Equal(t, "pass", testExec.Result)
}

func TestEngine_ResumeEmptyPreloadedRunsNormally(t *testing.T) {
	// Setting empty preloaded results should not change behavior
	wf := &domain.Workflow{
		Name: "resume-empty",
		Steps: map[string]*domain.Step{
			"build": {Name: "build", Type: domain.StepTypeScript, Results: []string{"success"}},
		},
		Wiring: []domain.Wire{
			{From: "build", Result: "success", To: domain.StepDone},
		},
		EntryStep: "build",
	}

	exec := &fakeExecutor{results: map[string]string{"build": "success"}}
	eng := engine.New(exec)
	eng.SetPreloadedResults(map[string]string{})

	run, err := eng.Run(context.Background(), wf)
	require.NoError(t, err)
	assert.Equal(t, domain.RunStateSucceeded, run.State)

	exec.mu.Lock()
	assert.Contains(t, exec.called, "build")
	exec.mu.Unlock()
}
