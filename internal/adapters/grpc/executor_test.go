package grpc

import (
	"context"
	"fmt"
	"io"
	"os"
	osExec "os/exec"
	"path/filepath"
	"sync"
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
	assert.Equal(t, []string{"cloche-agent"}, rt.lastConfig.Cmd,
		"container should start agent in session mode (no workflow file)")
	assert.Equal(t, "att-1", rt.lastConfig.AttemptID,
		"container should receive the attempt ID")

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
func (f *fakeRunStore) SetContextKey(_ context.Context, _, _, _, _, _ string) error { return nil }
func (f *fakeRunStore) GetContextKey(_ context.Context, _, _, _, _ string) (string, bool, error) {
	return "", false, nil
}
func (f *fakeRunStore) ListContextKeys(_ context.Context, _, _, _ string) ([]string, error) {
	return nil, nil
}
func (f *fakeRunStore) DeleteContextKeys(_ context.Context, _, _ string) error { return nil }
func (f *fakeRunStore) SaveAttempt(_ context.Context, _ *domain.Attempt) error { return nil }
func (f *fakeRunStore) GetAttempt(_ context.Context, _ string) (*domain.Attempt, error) {
	return nil, fmt.Errorf("not found")
}
func (f *fakeRunStore) ListAttempts(_ context.Context, _ string) ([]*domain.Attempt, error) {
	return nil, nil
}
func (f *fakeRunStore) FailStaleAttempts(_ context.Context) (int64, error) { return 0, nil }

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

// copyTrackingRuntime records CopyFrom calls so tests can verify log extraction.
type copyTrackingRuntime struct {
	mu          sync.Mutex
	copyFromSrc []string
	containerID string
}

func (r *copyTrackingRuntime) Start(_ context.Context, _ ports.ContainerConfig) (string, error) {
	r.containerID = "track-container-1"
	return r.containerID, nil
}
func (r *copyTrackingRuntime) Stop(_ context.Context, _ string) error { return nil }
func (r *copyTrackingRuntime) AttachOutput(_ context.Context, _ string) (io.ReadCloser, error) {
	return nil, nil
}
func (r *copyTrackingRuntime) Wait(_ context.Context, _ string) (int, error) { return 0, nil }
func (r *copyTrackingRuntime) CopyFrom(_ context.Context, _, src, _ string) error {
	r.mu.Lock()
	r.copyFromSrc = append(r.copyFromSrc, src)
	r.mu.Unlock()
	return nil
}
func (r *copyTrackingRuntime) Logs(_ context.Context, _ string) (string, error)  { return "", nil }
func (r *copyTrackingRuntime) Remove(_ context.Context, _ string) error           { return nil }
func (r *copyTrackingRuntime) Inspect(_ context.Context, _ string) (*ports.ContainerStatus, error) {
	return &ports.ContainerStatus{}, nil
}
func (r *copyTrackingRuntime) Attach(_ context.Context, _ string) (io.ReadWriteCloser, error) {
	return nil, nil
}

func (r *copyTrackingRuntime) copiedFrom() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]string, len(r.copyFromSrc))
	copy(result, r.copyFromSrc)
	return result
}

// TestDaemonExecutor_WorkflowStep_ExtractsLogsOnContextError verifies that
// when eng.Run returns an error (e.g. context timeout), container logs are
// extracted from any session that was already established before the failure.
// This prevents logs from being lost when a container sub-workflow times out.
func TestDaemonExecutor_WorkflowStep_ExtractsLogsOnContextError(t *testing.T) {
	tmpDir := t.TempDir()

	containerWF := buildContainerWFForTest("develop")
	allWFs := map[string]*domain.Workflow{"develop": containerWF}

	rt := &copyTrackingRuntime{}
	pool := docker.NewContainerPool(rt)

	// Pre-establish a container session to simulate a container that was
	// already running when the context timed out. The pool key matches what
	// DaemonExecutor constructs: attemptID + ":" + workflow.ContainerID().
	poolKey := "att-timeout:" + containerWF.ContainerID()
	go func() {
		time.Sleep(20 * time.Millisecond)
		pool.NotifyReady(rt.containerID)
	}()
	_, err := pool.SessionFor(context.Background(), poolKey, ports.ContainerConfig{Image: "img"})
	require.NoError(t, err, "pre-establishing session should succeed")

	de := NewDaemonExecutor(DaemonExecutorConfig{
		Pool:       pool,
		ProjectDir: tmpDir,
		TaskID:     "task-timeout",
		AttemptID:  "att-timeout",
		AllWFs:     allWFs,
	})

	step := &domain.Step{
		Name:    "develop",
		Type:    domain.StepTypeWorkflow,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"workflow_name": "develop"},
	}

	mainWF := buildHostWFForTest("main")
	// Use an already-cancelled context to simulate a timeout forcing eng.Run to fail.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, execErr := de.Execute(engine.WithWorkflow(ctx, mainWF), step)
	require.NoError(t, execErr)
	assert.Equal(t, "fail", result.Result)

	// The CopyFrom path for container log extraction should have been called.
	copied := rt.copiedFrom()
	assert.NotEmpty(t, copied, "extractContainerLogs should have called CopyFrom even on context error")
}

// TestDaemonExecutor_WorkflowStep_PreCreatesWorktree verifies that the first
// sub-workflow invocation for a given pool key pre-creates an extract worktree
// and writes child_branch to the KV store, and that a second invocation for
// the same pool key reuses it instead of creating another.
func TestDaemonExecutor_WorkflowStep_PreCreatesWorktree(t *testing.T) {
	tmpDir := t.TempDir()

	// Minimal git repo so gitHEAD(projectDir) returns a real SHA. Without this
	// the pre-create path bails out and the hook is never called.
	runGit := func(args ...string) {
		t.Helper()
		cmd := execCommand("git", args...)
		cmd.Dir = tmpDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s: %v", args, out, err)
		}
	}
	runGit("init")
	runGit("config", "user.email", "test@test")
	runGit("config", "user.name", "test")
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "README"), []byte("x"), 0644))
	runGit("add", "README")
	runGit("commit", "-m", "init")

	containerWF := buildContainerWFForTest("develop")
	allWFs := map[string]*domain.Workflow{"develop": containerWF}

	rt := &copyTrackingRuntime{}
	pool := docker.NewContainerPool(rt)

	// Stub both docker hooks so the test doesn't touch real git/docker.
	prepareCalls := 0
	origPrepare := prepareExtractWorktreeFn
	prepareExtractWorktreeFn = func(_ context.Context, opts docker.PrepareOptions) (docker.ExtractWorktree, error) {
		prepareCalls++
		return docker.ExtractWorktree{Dir: opts.TargetDir, Branch: opts.Branch}, nil
	}
	t.Cleanup(func() { prepareExtractWorktreeFn = origPrepare })

	origExtract := extractResultsFn
	extractResultsFn = func(_ context.Context, opts docker.ExtractOptions) (docker.ExtractResult, error) {
		return docker.ExtractResult{TargetDir: opts.WorktreeDir, Branch: opts.Branch}, nil
	}
	t.Cleanup(func() { extractResultsFn = origExtract })

	store := &recordingRunStore{ctxKeys: map[string]string{}}

	de := NewDaemonExecutor(DaemonExecutorConfig{
		Pool:       pool,
		Store:      store,
		ProjectDir: tmpDir,
		TaskID:     "task-pre",
		AttemptID:  "att-pre",
		AllWFs:     allWFs,
	})

	step := &domain.Step{
		Name:    "develop",
		Type:    domain.StepTypeWorkflow,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"workflow_name": "develop"},
	}

	mainWF := buildHostWFForTest("main")
	// Use cancelled ctx so eng.Run fails fast — we're only verifying the
	// pre-create side effect, which happens BEFORE eng.Run.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := de.Execute(engine.WithWorkflow(ctx, mainWF), step)
	require.NoError(t, err)

	// Expectation: prepare called once; KV got child_branch; worktree map populated.
	assert.Equal(t, 1, prepareCalls, "first invocation should call prepare once")
	poolKey := "att-pre:" + containerWF.ContainerID()
	wt, exists := de.worktrees[poolKey]
	require.True(t, exists, "worktree should be tracked for pool key")
	expectedBranch := "cloche/att-pre-" + containerWF.ContainerID()
	assert.Equal(t, expectedBranch, wt.Branch)
	assert.Equal(t, expectedBranch, store.ctxKeys["child_branch"])

	// Second invocation for the same pool key must NOT re-prepare.
	_, err = de.Execute(engine.WithWorkflow(ctx, mainWF), step)
	require.NoError(t, err)
	assert.Equal(t, 1, prepareCalls, "second invocation must reuse the existing worktree")
}

// recordingRunStore is a minimal RunStore that only supports SetContextKey —
// used to assert which KV keys the executor writes. All other methods are no-ops.
type recordingRunStore struct {
	ports.RunStore
	mu      sync.Mutex
	ctxKeys map[string]string
}

func (s *recordingRunStore) SetContextKey(_ context.Context, _, _, _, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ctxKeys[key] = value
	return nil
}

// execCommand is a thin wrapper so tests don't have to import os/exec at call sites.
func execCommand(name string, args ...string) *osExec.Cmd {
	return osExec.Command(name, args...)
}

// TestDaemonExecutor_WorkflowStep_NoExtractionWhenNoSession verifies that
// no log extraction is attempted when the container was never started (i.e.
// the context was cancelled before the first container step ran).
func TestDaemonExecutor_WorkflowStep_NoExtractionWhenNoSession(t *testing.T) {
	tmpDir := t.TempDir()

	containerWF := buildContainerWFForTest("develop")
	allWFs := map[string]*domain.Workflow{"develop": containerWF}

	rt := &copyTrackingRuntime{}
	pool := docker.NewContainerPool(rt)

	de := NewDaemonExecutor(DaemonExecutorConfig{
		Pool:       pool,
		ProjectDir: tmpDir,
		TaskID:     "task-no-sess",
		AttemptID:  "att-no-sess",
		AllWFs:     allWFs,
	})

	step := &domain.Step{
		Name:    "develop",
		Type:    domain.StepTypeWorkflow,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"workflow_name": "develop"},
	}

	mainWF := buildHostWFForTest("main")
	// Already-cancelled context; no container was started.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, execErr := de.Execute(engine.WithWorkflow(ctx, mainWF), step)
	require.NoError(t, execErr)
	assert.Equal(t, "fail", result.Result)

	// No session was ever established, so CopyFrom should NOT have been called.
	copied := rt.copiedFrom()
	assert.Empty(t, copied, "CopyFrom must not be called when no container session exists")
}
