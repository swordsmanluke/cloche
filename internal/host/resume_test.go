package host

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildPreloadedResults_LinearWorkflow(t *testing.T) {
	wf := &domain.Workflow{
		Name: "test",
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

	run := &domain.Run{
		StepExecutions: []*domain.StepExecution{
			{StepName: "build", Result: "success"},
			{StepName: "test", Result: "fail"},
		},
	}

	// Resume from test: build should be preloaded
	preloaded := buildPreloadedResults(run, wf, "test")
	assert.Equal(t, map[string]string{"build": "success"}, preloaded)
}

func TestBuildPreloadedResults_ResumeFromFirst(t *testing.T) {
	wf := &domain.Workflow{
		Name: "test",
		Steps: map[string]*domain.Step{
			"build": {Name: "build", Type: domain.StepTypeScript, Results: []string{"success", "fail"}},
			"test":  {Name: "test", Type: domain.StepTypeScript, Results: []string{"pass"}},
		},
		Wiring: []domain.Wire{
			{From: "build", Result: "success", To: "test"},
			{From: "build", Result: "fail", To: domain.StepAbort},
			{From: "test", Result: "pass", To: domain.StepDone},
		},
		EntryStep: "build",
	}

	run := &domain.Run{
		StepExecutions: []*domain.StepExecution{
			{StepName: "build", Result: "fail"},
		},
	}

	// Resume from build (entry step): nothing should be preloaded
	preloaded := buildPreloadedResults(run, wf, "build")
	assert.Empty(t, preloaded)
}

func TestBuildPreloadedResults_MultipleCompleted(t *testing.T) {
	wf := &domain.Workflow{
		Name: "test",
		Steps: map[string]*domain.Step{
			"a": {Name: "a", Type: domain.StepTypeScript, Results: []string{"ok"}},
			"b": {Name: "b", Type: domain.StepTypeScript, Results: []string{"ok"}},
			"c": {Name: "c", Type: domain.StepTypeScript, Results: []string{"ok", "fail"}},
			"d": {Name: "d", Type: domain.StepTypeScript, Results: []string{"ok"}},
		},
		Wiring: []domain.Wire{
			{From: "a", Result: "ok", To: "b"},
			{From: "b", Result: "ok", To: "c"},
			{From: "c", Result: "ok", To: "d"},
			{From: "c", Result: "fail", To: domain.StepAbort},
			{From: "d", Result: "ok", To: domain.StepDone},
		},
		EntryStep: "a",
	}

	run := &domain.Run{
		StepExecutions: []*domain.StepExecution{
			{StepName: "a", Result: "ok"},
			{StepName: "b", Result: "ok"},
			{StepName: "c", Result: "fail"},
		},
	}

	// Resume from c: a and b should be preloaded
	preloaded := buildPreloadedResults(run, wf, "c")
	assert.Equal(t, map[string]string{"a": "ok", "b": "ok"}, preloaded)
}

func TestBuildPreloadedResults_SkipsErrorResults(t *testing.T) {
	wf := &domain.Workflow{
		Name: "test",
		Steps: map[string]*domain.Step{
			"build": {Name: "build", Type: domain.StepTypeScript, Results: []string{"success"}},
			"test":  {Name: "test", Type: domain.StepTypeScript, Results: []string{"pass", "fail"}},
		},
		Wiring: []domain.Wire{
			{From: "build", Result: "success", To: "test"},
			{From: "test", Result: "pass", To: domain.StepDone},
			{From: "test", Result: "fail", To: domain.StepAbort},
		},
		EntryStep: "build",
	}

	run := &domain.Run{
		StepExecutions: []*domain.StepExecution{
			{StepName: "build", Result: "error"}, // error results should be excluded
			{StepName: "test", Result: "fail"},
		},
	}

	// Resume from build: nothing should be preloaded because build had "error"
	preloaded := buildPreloadedResults(run, wf, "build")
	assert.Empty(t, preloaded)
}

func TestCopySuccessfulStepOutputs(t *testing.T) {
	oldDir := t.TempDir()
	newDir := t.TempDir()

	wf := &domain.Workflow{
		Name: "test",
		Steps: map[string]*domain.Step{
			"prep":  {Name: "prep", Type: domain.StepTypeScript, Results: []string{"success"}},
			"build": {Name: "build", Type: domain.StepTypeScript, Results: []string{"success", "fail"}},
			"test":  {Name: "test", Type: domain.StepTypeScript, Results: []string{"pass"}},
		},
		Wiring: []domain.Wire{
			{From: "prep", Result: "success", To: "build"},
			{From: "build", Result: "success", To: "test"},
			{From: "build", Result: "fail", To: domain.StepAbort},
			{From: "test", Result: "pass", To: domain.StepDone},
		},
		EntryStep: "prep",
	}

	run := &domain.Run{
		StepExecutions: []*domain.StepExecution{
			{StepName: "prep", Result: "success"},
			{StepName: "build", Result: "fail"},
		},
	}

	// Write output files for completed steps in the old dir.
	require.NoError(t, os.WriteFile(filepath.Join(oldDir, "prep.log"), []byte("prep-output"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(oldDir, "build.log"), []byte("build-output"), 0644))

	// Resume from "build": only "prep" should be copied (not "build").
	copySuccessfulStepOutputs(run, wf, "build", oldDir, newDir)

	prepData, err := os.ReadFile(filepath.Join(newDir, "prep.log"))
	require.NoError(t, err)
	assert.Equal(t, "prep-output", string(prepData))

	_, err = os.ReadFile(filepath.Join(newDir, "build.log"))
	assert.True(t, os.IsNotExist(err), "build.log should not be copied (it's the resume point)")
}

func TestResumeRunAsNewAttempt_CreatesNewRun(t *testing.T) {
	dir := t.TempDir()

	// Write a minimal host workflow using the correct DSL syntax.
	clocheDir := filepath.Join(dir, ".cloche")
	require.NoError(t, os.MkdirAll(clocheDir, 0755))
	workflowContent := `workflow "main" {
  host {}

  step build {
    run     = "echo done"
    results = [success, fail]
  }

  step test {
    run     = "echo done"
    results = [success]
  }

  build:success -> test
  build:fail    -> abort
  test:success  -> done
}`
	require.NoError(t, os.WriteFile(filepath.Join(clocheDir, "host.cloche"), []byte(workflowContent), 0644))

	store := &fakeStore{runs: make(map[string]*domain.Run)}

	oldAttemptID := "aa11"
	newAttemptID := "bb22"
	taskID := "user-aa11"

	// Create old attempt's output dir with step output for "build".
	oldOutputDir := filepath.Join(dir, ".cloche", "logs", taskID, oldAttemptID)
	require.NoError(t, os.MkdirAll(oldOutputDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(oldOutputDir, "build.log"), []byte("build ok"), 0644))

	oldRun := domain.NewRun(domain.GenerateRunID("main", oldAttemptID), "main")
	oldRun.ProjectDir = dir
	oldRun.TaskID = taskID
	oldRun.AttemptID = oldAttemptID
	oldRun.IsHost = true
	oldRun.State = domain.RunStateFailed
	oldRun.StartedAt = time.Now().Add(-time.Minute)
	oldRun.CompletedAt = time.Now()
	oldRun.StepExecutions = []*domain.StepExecution{
		{StepName: "build", Result: "success"},
		{StepName: "test", Result: "fail"},
	}
	require.NoError(t, store.CreateRun(context.Background(), oldRun))

	newRunID := domain.GenerateRunID("main", newAttemptID)

	runner := &Runner{
		Store:    store,
		TaskID:   taskID,
		AttemptID: newAttemptID,
	}

	result, err := runner.ResumeRunAsNewAttempt(context.Background(), oldRun, "test", newRunID)
	require.NoError(t, err)

	// Old run must remain failed.
	assert.Equal(t, domain.RunStateFailed, oldRun.State, "old run must stay failed")

	// New run must exist in the store.
	newRun, err := store.GetRun(context.Background(), newRunID)
	require.NoError(t, err)
	assert.Equal(t, newAttemptID, newRun.AttemptID)
	assert.Equal(t, taskID, newRun.TaskID)

	// Result should indicate the run completed (test script echos "done" = success).
	assert.Equal(t, domain.RunStateSucceeded, result.State)

	// New output dir must exist and contain build.log copied from old attempt.
	newOutputDir := filepath.Join(dir, ".cloche", "logs", taskID, newAttemptID)
	buildOut, err := os.ReadFile(filepath.Join(newOutputDir, "build.log"))
	require.NoError(t, err)
	assert.Equal(t, "build ok", string(buildOut), "build.log should be copied from previous attempt")
}

func TestFindFirstFailedStep(t *testing.T) {
	tests := []struct {
		name     string
		run      *domain.Run
		expected string
	}{
		{
			name: "finds first fail",
			run: &domain.Run{
				StepExecutions: []*domain.StepExecution{
					{StepName: "build", Result: "success"},
					{StepName: "test", Result: "fail"},
				},
			},
			expected: "test",
		},
		{
			name: "finds error result",
			run: &domain.Run{
				StepExecutions: []*domain.StepExecution{
					{StepName: "build", Result: "success"},
					{StepName: "deploy", Result: "error"},
				},
			},
			expected: "deploy",
		},
		{
			name: "no failures",
			run: &domain.Run{
				StepExecutions: []*domain.StepExecution{
					{StepName: "build", Result: "success"},
				},
			},
			expected: "",
		},
		{
			name:     "empty executions",
			run:      &domain.Run{},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.run.FindFirstFailedStep()
			assert.Equal(t, tt.expected, result)
		})
	}
}
