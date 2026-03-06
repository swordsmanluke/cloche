package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/ports"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockRunWaiter struct {
	state domain.RunState
	err   error
	calls int
}

func (m *mockRunWaiter) WaitRun(_ context.Context, runID string) (domain.RunState, error) {
	m.calls++
	return m.state, m.err
}

func TestHostRunner_ScriptStep(t *testing.T) {
	tmpDir := t.TempDir()

	wf := &domain.Workflow{
		Name:      "orchestrate",
		EntryStep: "greet",
		Steps: map[string]*domain.Step{
			"greet": {
				Name:    "greet",
				Type:    domain.StepTypeScript,
				Results: []string{"success", "fail"},
				Config:  map[string]string{"run": "echo hello"},
			},
		},
		Wiring: []domain.Wire{
			{From: "greet", Result: "success", To: domain.StepDone},
			{From: "greet", Result: "fail", To: domain.StepAbort},
		},
	}

	hr := &HostRunner{
		ProjectDir: tmpDir,
	}

	task := ports.TrackerTask{ID: "t1", Title: "Test", Description: "body"}

	result, err := hr.RunWorkflow(context.Background(), wf, task, "orch-test-run")
	require.NoError(t, err)
	assert.Equal(t, domain.StepDone, result)

	// Check output file was created
	outFile := filepath.Join(tmpDir, ".cloche", "orch-test-run", "orchestrate", "greet.out")
	data, err := os.ReadFile(outFile)
	require.NoError(t, err)
	assert.Contains(t, string(data), "hello")
}

func TestHostRunner_ScriptStepFail(t *testing.T) {
	tmpDir := t.TempDir()

	wf := &domain.Workflow{
		Name:      "orchestrate",
		EntryStep: "bad",
		Steps: map[string]*domain.Step{
			"bad": {
				Name:    "bad",
				Type:    domain.StepTypeScript,
				Results: []string{"success", "fail"},
				Config:  map[string]string{"run": "exit 1"},
			},
		},
		Wiring: []domain.Wire{
			{From: "bad", Result: "success", To: domain.StepDone},
			{From: "bad", Result: "fail", To: domain.StepAbort},
		},
	}

	hr := &HostRunner{ProjectDir: tmpDir}
	task := ports.TrackerTask{ID: "t1", Title: "Test"}

	result, err := hr.RunWorkflow(context.Background(), wf, task, "orch-fail-run")
	require.NoError(t, err)
	assert.Equal(t, domain.StepAbort, result)
}

func TestHostRunner_WorkflowStep(t *testing.T) {
	tmpDir := t.TempDir()
	orchRunID := "orch-wf-run"

	var capturedPrompt string
	dispatch := func(_ context.Context, workflowName, projectDir, prompt string) (string, error) {
		capturedPrompt = prompt
		return "run-123", nil
	}

	waiter := &mockRunWaiter{state: domain.RunStateSucceeded}

	wf := &domain.Workflow{
		Name:      "orchestrate",
		EntryStep: "prep",
		Steps: map[string]*domain.Step{
			"prep": {
				Name:    "prep",
				Type:    domain.StepTypeScript,
				Results: []string{"success", "fail"},
				Config:  map[string]string{"run": "echo the-prompt-text"},
			},
			"dev": {
				Name:    "dev",
				Type:    domain.StepTypeWorkflow,
				Results: []string{"success", "fail"},
				Config:  map[string]string{"workflow_name": "develop", "prompt_step": "prep"},
			},
		},
		Wiring: []domain.Wire{
			{From: "prep", Result: "success", To: "dev"},
			{From: "prep", Result: "fail", To: domain.StepAbort},
			{From: "dev", Result: "success", To: domain.StepDone},
			{From: "dev", Result: "fail", To: domain.StepAbort},
		},
	}

	hr := &HostRunner{
		Dispatch:   dispatch,
		WaitRun:    waiter,
		ProjectDir: tmpDir,
	}

	task := ports.TrackerTask{ID: "t1", Title: "Test"}

	result, err := hr.RunWorkflow(context.Background(), wf, task, orchRunID)
	require.NoError(t, err)
	assert.Equal(t, domain.StepDone, result)
	assert.Equal(t, "the-prompt-text\n", capturedPrompt)
	assert.Equal(t, 1, waiter.calls)
}

func TestHostRunner_WorkflowStepFailed(t *testing.T) {
	tmpDir := t.TempDir()
	orchRunID := "orch-wf-fail"

	dispatch := func(_ context.Context, workflowName, projectDir, prompt string) (string, error) {
		return "run-456", nil
	}

	waiter := &mockRunWaiter{state: domain.RunStateFailed}

	wf := &domain.Workflow{
		Name:      "orchestrate",
		EntryStep: "prep",
		Steps: map[string]*domain.Step{
			"prep": {
				Name:    "prep",
				Type:    domain.StepTypeScript,
				Results: []string{"success", "fail"},
				Config:  map[string]string{"run": "echo ok"},
			},
			"dev": {
				Name:    "dev",
				Type:    domain.StepTypeWorkflow,
				Results: []string{"success", "fail"},
				Config:  map[string]string{"workflow_name": "develop", "prompt_step": "prep"},
			},
		},
		Wiring: []domain.Wire{
			{From: "prep", Result: "success", To: "dev"},
			{From: "prep", Result: "fail", To: domain.StepAbort},
			{From: "dev", Result: "success", To: domain.StepDone},
			{From: "dev", Result: "fail", To: domain.StepAbort},
		},
	}

	hr := &HostRunner{
		Dispatch:   dispatch,
		WaitRun:    waiter,
		ProjectDir: tmpDir,
	}

	task := ports.TrackerTask{ID: "t1", Title: "Test"}

	result, err := hr.RunWorkflow(context.Background(), wf, task, orchRunID)
	require.NoError(t, err)
	assert.Equal(t, domain.StepAbort, result)
}

func TestHostRunner_AbortPath(t *testing.T) {
	tmpDir := t.TempDir()

	wf := &domain.Workflow{
		Name:      "orchestrate",
		EntryStep: "fail-step",
		Steps: map[string]*domain.Step{
			"fail-step": {
				Name:    "fail-step",
				Type:    domain.StepTypeScript,
				Results: []string{"success", "fail"},
				Config:  map[string]string{"run": "exit 1"},
			},
			"next": {
				Name:    "next",
				Type:    domain.StepTypeScript,
				Results: []string{"success"},
				Config:  map[string]string{"run": "echo should-not-reach"},
			},
		},
		Wiring: []domain.Wire{
			{From: "fail-step", Result: "success", To: "next"},
			{From: "fail-step", Result: "fail", To: domain.StepAbort},
			{From: "next", Result: "success", To: domain.StepDone},
		},
	}

	hr := &HostRunner{ProjectDir: tmpDir}
	task := ports.TrackerTask{ID: "t1", Title: "Test"}

	result, err := hr.RunWorkflow(context.Background(), wf, task, "orch-abort-run")
	require.NoError(t, err)
	assert.Equal(t, domain.StepAbort, result)
}
