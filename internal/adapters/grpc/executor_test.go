package grpc

import (
	"context"
	"fmt"
	"io"
	"testing"

	"github.com/cloche-dev/cloche/internal/adapters/docker"
	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/engine"
	"github.com/cloche-dev/cloche/internal/host"
	"github.com/cloche-dev/cloche/internal/ports"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// buildHostWFForTest creates a minimal host workflow with one script step.
func buildHostWFForTest(name string) *domain.Workflow {
	return &domain.Workflow{
		Name:     name,
		Location: domain.LocationHost,
		Steps: map[string]*domain.Step{
			"step1": {
				Name:    "step1",
				Type:    domain.StepTypeScript,
				Results: []string{"success"},
				Config:  map[string]string{"run": "echo ok"},
			},
		},
		Wiring:    []domain.Wire{{From: "step1", Result: "success", To: domain.StepDone}},
		EntryStep: "step1",
	}
}

// buildContainerWFForTest creates a minimal container workflow.
func buildContainerWFForTest(name string) *domain.Workflow {
	return &domain.Workflow{
		Name:     name,
		Location: domain.LocationContainer,
		Steps: map[string]*domain.Step{
			"step1": {
				Name:    "step1",
				Type:    domain.StepTypeAgent,
				Results: []string{"success"},
				Config:  map[string]string{},
			},
		},
		Wiring:    []domain.Wire{{From: "step1", Result: "success", To: domain.StepDone}},
		EntryStep: "step1",
	}
}

// TestDaemonExecutor_ErrorWhenNoWorkflowInContext verifies that Execute returns
// an error for non-workflow steps when no workflow is in the context.
func TestDaemonExecutor_ErrorWhenNoWorkflowInContext(t *testing.T) {
	de := NewDaemonExecutor(DaemonExecutorConfig{
		ProjectDir: t.TempDir(),
		AttemptID:  "att1",
		AllWFs:     map[string]*domain.Workflow{},
	})

	step := &domain.Step{
		Name:    "step1",
		Type:    domain.StepTypeScript,
		Results: []string{"success"},
		Config:  map[string]string{},
	}

	_, err := de.Execute(context.Background(), step)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no workflow in context")
}

// TestDaemonExecutor_WorkflowStep_MissingWorkflowName verifies that a
// workflow_name step without the config key returns an error.
func TestDaemonExecutor_WorkflowStep_MissingWorkflowName(t *testing.T) {
	wf := buildHostWFForTest("main")

	de := NewDaemonExecutor(DaemonExecutorConfig{
		ProjectDir: t.TempDir(),
		AttemptID:  "att1",
		AllWFs:     map[string]*domain.Workflow{"main": wf},
	})

	step := &domain.Step{
		Name:    "dispatch",
		Type:    domain.StepTypeWorkflow,
		Results: []string{"success", "fail"},
		Config:  map[string]string{}, // no workflow_name
	}

	ctx := engine.WithWorkflow(context.Background(), wf)
	_, err := de.Execute(ctx, step)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "workflow_name")
}

// TestDaemonExecutor_WorkflowStep_NotFound verifies that a workflow_name step
// that references an unknown workflow returns an error.
func TestDaemonExecutor_WorkflowStep_NotFound(t *testing.T) {
	wf := buildHostWFForTest("main")

	de := NewDaemonExecutor(DaemonExecutorConfig{
		ProjectDir: t.TempDir(),
		AttemptID:  "att1",
		AllWFs:     map[string]*domain.Workflow{"main": wf},
	})

	step := &domain.Step{
		Name:    "dispatch",
		Type:    domain.StepTypeWorkflow,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"workflow_name": "nonexistent"},
	}

	ctx := engine.WithWorkflow(context.Background(), wf)
	_, err := de.Execute(ctx, step)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nonexistent")
}

// TestDaemonExecutor_WorkflowStep_RunsSubWorkflow verifies that a workflow_name
// step triggers a sub-workflow run using the same DaemonExecutor recursively.
func TestDaemonExecutor_WorkflowStep_RunsSubWorkflow(t *testing.T) {
	tmpDir := t.TempDir()

	// Sub-workflow: a host workflow with one script step.
	subWF := &domain.Workflow{
		Name:     "develop",
		Location: domain.LocationHost,
		Steps: map[string]*domain.Step{
			"build": {
				Name:    "build",
				Type:    domain.StepTypeScript,
				Results: []string{"success", "fail"},
				Config:  map[string]string{"run": "echo built"},
			},
		},
		Wiring: []domain.Wire{
			{From: "build", Result: "success", To: domain.StepDone},
			{From: "build", Result: "fail", To: domain.StepAbort},
		},
		EntryStep: "build",
	}

	allWFs := map[string]*domain.Workflow{
		"develop": subWF,
	}

	// Host executor handles actual script execution.
	hostExec := &host.Executor{
		ProjectDir: tmpDir,
		OutputDir:  tmpDir + "/output",
	}

	de := NewDaemonExecutor(DaemonExecutorConfig{
		HostExec:   hostExec,
		ProjectDir: tmpDir,
		AttemptID:  "att1",
		AllWFs:     allWFs,
	})

	step := &domain.Step{
		Name:    "dispatch",
		Type:    domain.StepTypeWorkflow,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"workflow_name": "develop"},
	}

	// Provide a parent workflow context (the step belongs to a host workflow).
	mainWF := buildHostWFForTest("main")
	ctx := engine.WithWorkflow(context.Background(), mainWF)

	result, err := de.Execute(ctx, step)
	require.NoError(t, err)
	assert.Equal(t, "success", result.Result)
}

// TestDaemonExecutor_WorkflowStep_FailedSubWorkflow verifies that when the
// sub-workflow fails, the step result is "fail".
func TestDaemonExecutor_WorkflowStep_FailedSubWorkflow(t *testing.T) {
	tmpDir := t.TempDir()

	// Sub-workflow: a script that exits non-zero.
	subWF := &domain.Workflow{
		Name:     "develop",
		Location: domain.LocationHost,
		Steps: map[string]*domain.Step{
			"build": {
				Name:    "build",
				Type:    domain.StepTypeScript,
				Results: []string{"success", "fail"},
				Config:  map[string]string{"run": "exit 1"},
			},
		},
		Wiring: []domain.Wire{
			{From: "build", Result: "success", To: domain.StepDone},
			{From: "build", Result: "fail", To: domain.StepAbort},
		},
		EntryStep: "build",
	}

	allWFs := map[string]*domain.Workflow{"develop": subWF}

	hostExec := &host.Executor{
		ProjectDir: tmpDir,
		OutputDir:  tmpDir + "/output",
	}

	de := NewDaemonExecutor(DaemonExecutorConfig{
		HostExec:   hostExec,
		ProjectDir: tmpDir,
		AttemptID:  "att1",
		AllWFs:     allWFs,
	})

	step := &domain.Step{
		Name:    "dispatch",
		Type:    domain.StepTypeWorkflow,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"workflow_name": "develop"},
	}

	mainWF := buildHostWFForTest("main")
	ctx := engine.WithWorkflow(context.Background(), mainWF)

	result, err := de.Execute(ctx, step)
	require.NoError(t, err)
	assert.Equal(t, "fail", result.Result)
}

// TestDaemonExecutor_ContainerStep_NoPool verifies that container steps return
// an error when no pool is configured.
func TestDaemonExecutor_ContainerStep_NoPool(t *testing.T) {
	wf := buildContainerWFForTest("develop")

	de := NewDaemonExecutor(DaemonExecutorConfig{
		ProjectDir: t.TempDir(),
		AttemptID:  "att1",
		Pool:       nil, // no pool
		AllWFs:     map[string]*domain.Workflow{"develop": wf},
	})

	step := wf.Steps["step1"]
	ctx := engine.WithWorkflow(context.Background(), wf)

	_, err := de.Execute(ctx, step)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no container pool")
}

// TestDaemonExecutor_ContainerStep_StartError verifies that a container start
// error is propagated through SessionFor.
func TestDaemonExecutor_ContainerStep_StartError(t *testing.T) {
	rt := &errContainerRuntime{err: fmt.Errorf("docker not available")}
	pool := docker.NewContainerPool(rt)

	wf := buildContainerWFForTest("develop")

	de := NewDaemonExecutor(DaemonExecutorConfig{
		Pool:       pool,
		ProjectDir: t.TempDir(),
		AttemptID:  "att-start-err",
		AllWFs:     map[string]*domain.Workflow{"develop": wf},
	})

	step := wf.Steps["step1"]
	ctx := engine.WithWorkflow(context.Background(), wf)

	_, err := de.Execute(ctx, step)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "docker not available")
}

// errContainerRuntime always fails on Start.
type errContainerRuntime struct{ err error }

func (e *errContainerRuntime) Start(_ context.Context, _ ports.ContainerConfig) (string, error) {
	return "", e.err
}
func (e *errContainerRuntime) Stop(_ context.Context, _ string) error { return nil }
func (e *errContainerRuntime) AttachOutput(_ context.Context, _ string) (io.ReadCloser, error) {
	return nil, nil
}
func (e *errContainerRuntime) Wait(_ context.Context, _ string) (int, error)    { return 0, nil }
func (e *errContainerRuntime) CopyFrom(_ context.Context, _, _, _ string) error { return nil }
func (e *errContainerRuntime) Logs(_ context.Context, _ string) (string, error) { return "", nil }
func (e *errContainerRuntime) Remove(_ context.Context, _ string) error         { return nil }
func (e *errContainerRuntime) Inspect(_ context.Context, _ string) (*ports.ContainerStatus, error) {
	return &ports.ContainerStatus{}, nil
}
func (e *errContainerRuntime) Attach(_ context.Context, _ string) (io.ReadWriteCloser, error) {
	return nil, nil
}
