package grpc

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cloche-dev/cloche/internal/activitylog"
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

// TestDaemonExecutor_ProductionWiring validates the full construction path that
// the daemon uses: daemonExecutorFor → host.Runner.RunNamed → engine. This
// exercises the wiring that unit tests bypass by constructing DaemonExecutor
// directly. The test creates a project with:
//   - a host workflow "main" containing a script step and a workflow_name step
//   - a container workflow "develop" targeted by the workflow_name step
//
// It verifies that:
//  1. daemonExecutorFor sets Image, Pool, and AllWFs correctly
//  2. host script steps execute with correct OutputDir (not project root)
//  3. workflow_name steps route through the DaemonExecutor to the sub-workflow
//  4. container steps in the sub-workflow reach the pool with the correct image
func TestDaemonExecutor_ProductionWiring(t *testing.T) {
	tmpDir := t.TempDir()
	clocheDir := filepath.Join(tmpDir, ".cloche")
	require.NoError(t, os.MkdirAll(clocheDir, 0755))

	// Host workflow: script step → workflow_name step dispatching "develop"
	hostCloche := `workflow "main" {
  host {}

  step setup {
    run     = "echo setup-done"
    results = [success, fail]
  }

  step develop {
    workflow_name = "develop"
    results       = [success, fail]
  }

  setup:success   -> develop
  setup:fail      -> abort
  develop:success -> done
  develop:fail    -> abort
}`
	require.NoError(t, os.WriteFile(filepath.Join(clocheDir, "host.cloche"), []byte(hostCloche), 0644))

	// Container workflow targeted by the workflow_name step
	containerCloche := `workflow "develop" {
  step build {
    run     = "echo building"
    results = [success, fail]
  }
  build:success -> done
  build:fail    -> abort
}`
	require.NoError(t, os.WriteFile(filepath.Join(clocheDir, "develop.cloche"), []byte(containerCloche), 0644))

	// Track what the runtime receives to verify image propagation.
	rt := &recordingContainerRuntime{}
	pool := docker.NewContainerPool(rt)

	srv := &ClocheServer{
		store:           &fakeRunStore{},
		container:       rt,
		pool:            pool,
		defaultImage:    "test-image:latest",
		runIDs:          make(map[string]string),
		containerRun:    make(map[string]string),
		hostCancels:     make(map[string]context.CancelFunc),
		loops:           make(map[string]*host.Loop),
		activityLoggers: make(map[string]*activitylog.Logger),
	}

	// 1. Verify daemonExecutorFor produces a properly configured executor.
	exec := srv.daemonExecutorFor(tmpDir, "task-1", "att-1")
	require.NotNil(t, exec)

	de, ok := exec.(*DaemonExecutor)
	require.True(t, ok, "expected *DaemonExecutor")
	assert.Equal(t, "test-image:latest", de.image, "Image should be set from server default")
	assert.NotNil(t, de.pool, "Pool should be set")
	assert.Contains(t, de.allWFs, "main", "AllWFs should contain host workflow")
	assert.Contains(t, de.allWFs, "develop", "AllWFs should contain container workflow")

	// 2. Run the host workflow through host.Runner (same path as createPhaseLoop).
	//    The "setup" script step should work. The "develop" workflow_name step
	//    should route to the container workflow and attempt to start a container.
	runner := &host.Runner{
		Store:     &fakeRunStore{},
		Executor:  exec,
		TaskID:    "task-1",
		AttemptID: "att-1",
	}

	// Use a short timeout — the container step will block waiting for AgentReady
	// which never comes, but we just need to verify it reaches the pool.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	result, err := runner.RunNamed(ctx, tmpDir, "main")

	// The run will fail because the container step blocks on AgentReady and times
	// out, but that's expected — we're testing wiring, not full container lifecycle.
	// What matters is that:

	// a) The setup script step produced output in the correct directory (not project root).
	assert.NoFileExists(t, filepath.Join(tmpDir, "setup.log"),
		"step output should NOT be written to project root")

	// b) The container runtime was called with the correct image.
	require.True(t, rt.startCalled, "container runtime Start should have been called")
	assert.Equal(t, "test-image:latest", rt.lastConfig.Image,
		"container should be started with the server default image")
	assert.Equal(t, "develop", rt.lastConfig.WorkflowName,
		"container should be started for the 'develop' workflow")
	assert.Equal(t, tmpDir, rt.lastConfig.ProjectDir,
		"container should receive the project directory")

	// c) The run should have failed (timeout waiting for AgentReady), not errored
	//    with "unsupported step type" or "no container pool".
	if err != nil {
		assert.NotContains(t, err.Error(), "unsupported step type",
			"workflow_name steps should be handled by DaemonExecutor")
		assert.NotContains(t, err.Error(), "no container pool",
			"container pool should be wired")
		assert.NotContains(t, err.Error(), "invalid reference format",
			"image should be set")
	}
	_ = result // result may be nil on context cancellation
}

// recordingContainerRuntime records Start calls so tests can verify the image
// and config passed through the production wiring path.
type recordingContainerRuntime struct {
	startCalled bool
	lastConfig  ports.ContainerConfig
}

func (r *recordingContainerRuntime) Start(_ context.Context, cfg ports.ContainerConfig) (string, error) {
	r.startCalled = true
	r.lastConfig = cfg
	return "fake-container-id", nil
}
func (r *recordingContainerRuntime) Stop(_ context.Context, _ string) error { return nil }
func (r *recordingContainerRuntime) AttachOutput(_ context.Context, _ string) (io.ReadCloser, error) {
	return nil, nil
}
func (r *recordingContainerRuntime) Wait(_ context.Context, _ string) (int, error) { return 0, nil }
func (r *recordingContainerRuntime) CopyFrom(_ context.Context, _, _, _ string) error {
	return nil
}
func (r *recordingContainerRuntime) Logs(_ context.Context, _ string) (string, error) {
	return "", nil
}
func (r *recordingContainerRuntime) Remove(_ context.Context, _ string) error { return nil }
func (r *recordingContainerRuntime) Inspect(_ context.Context, _ string) (*ports.ContainerStatus, error) {
	return &ports.ContainerStatus{}, nil
}
func (r *recordingContainerRuntime) Attach(_ context.Context, _ string) (io.ReadWriteCloser, error) {
	return nil, nil
}

// fakeRunStore is a minimal RunStore that satisfies the interface without
// persisting anything. Used for wiring tests that don't need real storage.
type fakeRunStore struct{}

func (f *fakeRunStore) CreateRun(_ context.Context, _ *domain.Run) error { return nil }
func (f *fakeRunStore) UpdateRun(_ context.Context, _ *domain.Run) error { return nil }
func (f *fakeRunStore) GetRun(_ context.Context, _ string) (*domain.Run, error) {
	return nil, fmt.Errorf("not found")
}
func (f *fakeRunStore) GetRunByAttempt(_ context.Context, _, _ string) (*domain.Run, error) {
	return nil, fmt.Errorf("not found")
}
func (f *fakeRunStore) DeleteRun(_ context.Context, _ string) error             { return nil }
func (f *fakeRunStore) ListRuns(_ context.Context, _ time.Time) ([]*domain.Run, error) {
	return nil, nil
}
func (f *fakeRunStore) ListRunsByProject(_ context.Context, _ string, _ time.Time) ([]*domain.Run, error) {
	return nil, nil
}
func (f *fakeRunStore) ListRunsFiltered(_ context.Context, _ domain.RunListFilter) ([]*domain.Run, error) {
	return nil, nil
}
func (f *fakeRunStore) ListProjects(_ context.Context) ([]string, error)  { return nil, nil }
func (f *fakeRunStore) ListChildRuns(_ context.Context, _ string) ([]*domain.Run, error) {
	return nil, nil
}
func (f *fakeRunStore) QueryUsage(_ context.Context, _ ports.UsageQuery) ([]domain.UsageSummary, error) {
	return nil, nil
}
func (f *fakeRunStore) SetContextKey(_ context.Context, _, _, _, _ string) error { return nil }
func (f *fakeRunStore) GetContextKey(_ context.Context, _, _, _ string) (string, bool, error) {
	return "", false, nil
}
func (f *fakeRunStore) ListContextKeys(_ context.Context, _, _ string) ([]string, error) {
	return nil, nil
}
func (f *fakeRunStore) DeleteContextKeys(_ context.Context, _, _ string) error { return nil }

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
