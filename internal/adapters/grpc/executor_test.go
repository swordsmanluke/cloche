package grpc

import (
	"context"
	"fmt"
	"io"
	"os"
	osExec "os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloche-dev/cloche/internal/activitylog"
	"github.com/cloche-dev/cloche/internal/adapters/docker"
	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/engine"
	"github.com/cloche-dev/cloche/internal/host"
	"github.com/cloche-dev/cloche/internal/logstream"
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

// TestDaemonExecutor_ContainerStep_UsesWorkflowImage verifies that when the
// workflow declares container.image, that image is passed to the container
// runtime instead of the daemon default.
func TestDaemonExecutor_ContainerStep_UsesWorkflowImage(t *testing.T) {
	rt := &recordingContainerRuntime{}
	pool := docker.NewContainerPool(rt)

	wf := buildContainerWFForTest("develop")
	wf.Config = map[string]string{"container.image": "custom-image:v2"}

	de := NewDaemonExecutor(DaemonExecutorConfig{
		Pool:       pool,
		ProjectDir: t.TempDir(),
		AttemptID:  "att-wf-img",
		Image:      "daemon-default:latest",
		AllWFs:     map[string]*domain.Workflow{"develop": wf},
	})

	step := wf.Steps["step1"]
	// Pre-cancelled context so SessionFor returns fast after Start is called.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ctx = engine.WithWorkflow(ctx, wf)

	_, err := de.Execute(ctx, step)
	require.Error(t, err)

	require.True(t, rt.startCalled, "container runtime Start should have been called")
	assert.Equal(t, "custom-image:v2", rt.lastConfig.Image,
		"workflow container.image should override the daemon default")
}

// TestDaemonExecutor_ContainerStep_FallsBackToDaemonImage verifies that when
// the workflow does not declare container.image, the daemon default is used.
func TestDaemonExecutor_ContainerStep_FallsBackToDaemonImage(t *testing.T) {
	rt := &recordingContainerRuntime{}
	pool := docker.NewContainerPool(rt)

	wf := buildContainerWFForTest("develop")
	// No container.image in wf.Config — should fall back to daemon default.

	de := NewDaemonExecutor(DaemonExecutorConfig{
		Pool:       pool,
		ProjectDir: t.TempDir(),
		AttemptID:  "att-wf-img-fb",
		Image:      "daemon-default:latest",
		AllWFs:     map[string]*domain.Workflow{"develop": wf},
	})

	step := wf.Steps["step1"]
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ctx = engine.WithWorkflow(ctx, wf)

	_, err := de.Execute(ctx, step)
	require.Error(t, err)

	require.True(t, rt.startCalled, "container runtime Start should have been called")
	assert.Equal(t, "daemon-default:latest", rt.lastConfig.Image,
		"daemon default image should be used when workflow has no container.image")
}

// TestDaemonExecutor_ContainerStep_InjectsAgentConfig verifies that the daemon
// bridges workflow-level `container { agent_command = ... }` and `agent_args`
// into per-step config before dispatching to the in-container cloche-agent.
// Without this, the in-container prompt adapter would silently fall back to
// its built-in default of ["claude"], ignoring whatever .cloche files declare.
//
// Step-level overrides must win over the workflow-level defaults.
func TestDaemonExecutor_ContainerStep_InjectsAgentConfig(t *testing.T) {
	rt := &recordingContainerRuntime{}
	pool := docker.NewContainerPool(rt)

	wf := buildContainerWFForTest("develop")
	wf.Config = map[string]string{
		"container.agent_command": "opencode",
		"container.agent_args":    "run --model digitalocean/kimi-k2.6 --dangerously-skip-permissions",
	}

	de := NewDaemonExecutor(DaemonExecutorConfig{
		Pool:       pool,
		ProjectDir: t.TempDir(),
		AttemptID:  "att-agent-inject",
		Image:      "daemon-default:latest",
		AllWFs:     map[string]*domain.Workflow{"develop": wf},
	})

	step := wf.Steps["step1"]
	// Pre-cancelled ctx so SessionFor returns fast after Start is called.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ctx = engine.WithWorkflow(ctx, wf)

	_, _ = de.Execute(ctx, step)

	// Injection happens before SessionFor, so the assertion runs regardless of
	// the cancelled-context error path.
	assert.Equal(t, "opencode", step.Config["agent_command"],
		"workflow container.agent_command should propagate to step.Config[agent_command]")
	assert.Equal(t, "run --model digitalocean/kimi-k2.6 --dangerously-skip-permissions", step.Config["agent_args"],
		"workflow container.agent_args should propagate to step.Config[agent_args]")
}

// TestDaemonExecutor_ContainerStep_StepLevelAgentWins verifies that an explicit
// step-level agent_command takes precedence over the container-block default.
func TestDaemonExecutor_ContainerStep_StepLevelAgentWins(t *testing.T) {
	rt := &recordingContainerRuntime{}
	pool := docker.NewContainerPool(rt)

	wf := buildContainerWFForTest("develop")
	wf.Config = map[string]string{
		"container.agent_command": "opencode",
		"container.agent_args":    "run --model some-default",
	}

	de := NewDaemonExecutor(DaemonExecutorConfig{
		Pool:       pool,
		ProjectDir: t.TempDir(),
		AttemptID:  "att-agent-step-win",
		Image:      "daemon-default:latest",
		AllWFs:     map[string]*domain.Workflow{"develop": wf},
	})

	step := wf.Steps["step1"]
	step.Config["agent_command"] = "codex"
	step.Config["agent_args"] = "--explicit"

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ctx = engine.WithWorkflow(ctx, wf)

	_, _ = de.Execute(ctx, step)

	assert.Equal(t, "codex", step.Config["agent_command"],
		"step-level agent_command should NOT be overwritten by container-block default")
	assert.Equal(t, "--explicit", step.Config["agent_args"],
		"step-level agent_args should NOT be overwritten by container-block default")
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
	hostCloche := `workflow main {
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
	containerCloche := `workflow develop {
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

// TestDaemonExecutorFor_SetsContainerSeedSHA verifies that daemonExecutorFor
// captures the project's current git HEAD into ContainerSeedSHA, so that
// container sub-workflow copies are protected against host steps that check
// out a different branch on the shared project directory mid-run (see
// TestDaemonExecutor_ContainerProjectDir_SurvivesLaterCheckout).
func TestDaemonExecutorFor_SetsContainerSeedSHA(t *testing.T) {
	tmpDir := t.TempDir()
	gitCmd(t, tmpDir, "init")
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "README"), []byte("x"), 0644))
	gitCmd(t, tmpDir, "add", "README")
	gitCmd(t, tmpDir, "commit", "-m", "init")
	headSHA := gitCmd(t, tmpDir, "rev-parse", "HEAD")

	srv := &ClocheServer{
		store:           &fakeRunStore{},
		pool:            docker.NewContainerPool(&recordingContainerRuntime{}),
		defaultImage:    "test-image:latest",
		activityLoggers: make(map[string]*activitylog.Logger),
	}

	exec := srv.daemonExecutorFor(tmpDir, "task-1", "att-1")
	de, ok := exec.(*DaemonExecutor)
	require.True(t, ok)
	assert.Equal(t, headSHA, de.containerSeedSHA)
}

// TestDaemonExecutorFor_NonGitProject verifies that non-git project
// directories don't break daemonExecutorFor — ContainerSeedSHA stays empty
// and containerProjectDir falls back to the live directory.
func TestDaemonExecutorFor_NonGitProject(t *testing.T) {
	tmpDir := t.TempDir()

	srv := &ClocheServer{
		store:           &fakeRunStore{},
		pool:            docker.NewContainerPool(&recordingContainerRuntime{}),
		defaultImage:    "test-image:latest",
		activityLoggers: make(map[string]*activitylog.Logger),
	}

	exec := srv.daemonExecutorFor(tmpDir, "task-1", "att-1")
	de, ok := exec.(*DaemonExecutor)
	require.True(t, ok)
	assert.Empty(t, de.containerSeedSHA)
	assert.Equal(t, tmpDir, de.containerProjectDir(context.Background()))
}

// blockingWait provides a Wait that blocks until Stop signals the container
// exited (or context is cancelled). Embed in fake runtimes; call bwInit(id) in
// Start and bwHalt(id) in Stop so the pool exit-watcher doesn't fire early.
type blockingWait struct {
	bwMu   sync.Mutex
	bwChs  map[string]chan struct{}
	bwOnce map[string]*sync.Once
}

func (b *blockingWait) bwInit(id string) {
	b.bwMu.Lock()
	defer b.bwMu.Unlock()
	if b.bwChs == nil {
		b.bwChs = make(map[string]chan struct{})
		b.bwOnce = make(map[string]*sync.Once)
	}
	b.bwChs[id] = make(chan struct{})
	b.bwOnce[id] = &sync.Once{}
}

func (b *blockingWait) bwHalt(id string) {
	b.bwMu.Lock()
	ch := b.bwChs[id]
	once := b.bwOnce[id]
	b.bwMu.Unlock()
	if ch != nil && once != nil {
		once.Do(func() { close(ch) })
	}
}

func (b *blockingWait) Wait(ctx context.Context, id string) (int, error) {
	b.bwMu.Lock()
	ch, ok := b.bwChs[id]
	b.bwMu.Unlock()
	if !ok {
		<-ctx.Done()
		return -1, ctx.Err()
	}
	select {
	case <-ch:
		return 0, nil
	case <-ctx.Done():
		return -1, ctx.Err()
	}
}

// recordingContainerRuntime records Start calls so tests can verify the image
// and config passed through the production wiring path.
type recordingContainerRuntime struct {
	blockingWait
	startCalled bool
	lastConfig  ports.ContainerConfig
}

func (r *recordingContainerRuntime) Start(_ context.Context, cfg ports.ContainerConfig) (string, error) {
	r.startCalled = true
	r.lastConfig = cfg
	const id = "fake-container-id"
	r.bwInit(id)
	return id, nil
}
func (r *recordingContainerRuntime) Stop(_ context.Context, id string) error {
	r.bwHalt(id)
	return nil
}
func (r *recordingContainerRuntime) AttachOutput(_ context.Context, _ string) (io.ReadCloser, error) {
	return nil, nil
}
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
func (f *fakeRunStore) DeleteRun(_ context.Context, _ string) error { return nil }
func (f *fakeRunStore) ListRuns(_ context.Context, _ time.Time) ([]*domain.Run, error) {
	return nil, nil
}
func (f *fakeRunStore) ListRunsByProject(_ context.Context, _ string, _ time.Time) ([]*domain.Run, error) {
	return nil, nil
}
func (f *fakeRunStore) ListRunsFiltered(_ context.Context, _ domain.RunListFilter) ([]*domain.Run, error) {
	return nil, nil
}
func (f *fakeRunStore) ListProjects(_ context.Context) ([]string, error) { return nil, nil }
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
	blockingWait
	mu          sync.Mutex
	copyFromSrc []string
	containerID string
}

func (r *copyTrackingRuntime) Start(_ context.Context, _ ports.ContainerConfig) (string, error) {
	r.containerID = "track-container-1"
	r.bwInit(r.containerID)
	return r.containerID, nil
}
func (r *copyTrackingRuntime) Stop(_ context.Context, id string) error {
	r.bwHalt(id)
	return nil
}
func (r *copyTrackingRuntime) AttachOutput(_ context.Context, _ string) (io.ReadCloser, error) {
	return nil, nil
}
func (r *copyTrackingRuntime) CopyFrom(_ context.Context, _, src, _ string) error {
	r.mu.Lock()
	r.copyFromSrc = append(r.copyFromSrc, src)
	r.mu.Unlock()
	return nil
}
func (r *copyTrackingRuntime) Logs(_ context.Context, _ string) (string, error) { return "", nil }
func (r *copyTrackingRuntime) Remove(_ context.Context, _ string) error         { return nil }
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
	// Legacy single-tree mode (no [[repositories]] in tmpDir's .cloche/config.toml)
	// produces a single-element slice with an unnamed repo and writes the bare
	// child_branch key for back-compat.
	assert.Equal(t, 1, prepareCalls, "first invocation should call prepare once")
	poolKey := "att-pre:" + containerWF.ContainerID()
	prepared, exists := de.worktrees[poolKey]
	require.True(t, exists, "worktree should be tracked for pool key")
	require.Len(t, prepared, 1, "legacy single-tree mode should produce one worktree")
	expectedBranch := "cloche/att-pre-" + containerWF.ContainerID()
	assert.Equal(t, expectedBranch, prepared[0].Worktree.Branch)
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

func (s *recordingRunStore) GetContextKey(_ context.Context, _, _, _, key string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	val, ok := s.ctxKeys[key]
	return val, ok, nil
}

// execCommand is a thin wrapper so tests don't have to import os/exec at call sites.
func execCommand(name string, args ...string) *osExec.Cmd {
	return osExec.Command(name, args...)
}

// initGitRepo creates a git repo at dir with a single commit so gitHEAD returns a valid SHA.
func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0755))
	git := func(args ...string) {
		t.Helper()
		cmd := execCommand("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v in %s: %s: %v", args, dir, out, err)
		}
	}
	git("init")
	git("config", "user.email", "test@test")
	git("config", "user.name", "test")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README"), []byte("x"), 0644))
	git("add", "README")
	git("commit", "-m", "init")
}

// writeConfigTOML writes a .cloche/config.toml with the given [[repositories]] entries.
// Each entry is a pair of (name, relativePath).
func writeConfigTOML(t *testing.T, projectDir string, repos [][2]string) {
	t.Helper()
	clocheDir := filepath.Join(projectDir, ".cloche")
	require.NoError(t, os.MkdirAll(clocheDir, 0755))
	var sb strings.Builder
	for _, r := range repos {
		fmt.Fprintf(&sb, "[[repositories]]\nname = %q\npath = %q\n\n", r[0], r[1])
	}
	require.NoError(t, os.WriteFile(filepath.Join(clocheDir, "config.toml"), []byte(sb.String()), 0644))
}

// TestDaemonExecutor_PrepareExtractWorktrees_MultiRepo verifies that with two
// [[repositories]] entries, prepareExtractWorktrees calls the prepare hook once
// per repo, populates de.worktrees[poolKey] with two entries (in config order),
// and writes child_repos, child_branch:<name>, and child_repo_path:<name> KV keys.
func TestDaemonExecutor_PrepareExtractWorktrees_MultiRepo(t *testing.T) {
	tmpDir := t.TempDir()

	repo1Dir := filepath.Join(tmpDir, "repos", "alpha")
	repo2Dir := filepath.Join(tmpDir, "repos", "beta")
	initGitRepo(t, repo1Dir)
	initGitRepo(t, repo2Dir)

	writeConfigTOML(t, tmpDir, [][2]string{
		{"alpha", "./repos/alpha"},
		{"beta", "./repos/beta"},
	})

	var preparedDirs []string
	origPrepare := prepareExtractWorktreeFn
	prepareExtractWorktreeFn = func(_ context.Context, opts docker.PrepareOptions) (docker.ExtractWorktree, error) {
		preparedDirs = append(preparedDirs, opts.ProjectDir)
		return docker.ExtractWorktree{Dir: opts.TargetDir, Branch: opts.Branch}, nil
	}
	t.Cleanup(func() { prepareExtractWorktreeFn = origPrepare })

	store := &recordingRunStore{ctxKeys: map[string]string{}}
	containerWF := buildContainerWFForTest("develop")
	poolKey := "att-multi:" + containerWF.ContainerID()

	de := NewDaemonExecutor(DaemonExecutorConfig{
		Store:      store,
		ProjectDir: tmpDir,
		TaskID:     "task-multi",
		AttemptID:  "att-multi",
		AllWFs:     map[string]*domain.Workflow{"develop": containerWF},
	})

	err := de.prepareExtractWorktrees(context.Background(), poolKey, containerWF)
	require.NoError(t, err)

	assert.Len(t, preparedDirs, 2, "prepare should be called once per repo")

	prepared, exists := de.worktrees[poolKey]
	require.True(t, exists, "worktrees should be tracked for pool key")
	require.Len(t, prepared, 2, "one worktree per repo")
	assert.Equal(t, "alpha", prepared[0].Repo.Name)
	assert.Equal(t, "beta", prepared[1].Repo.Name)

	assert.Equal(t, "alpha,beta", store.ctxKeys["child_repos"])
	assert.NotEmpty(t, store.ctxKeys["child_branch:alpha"])
	assert.NotEmpty(t, store.ctxKeys["child_branch:beta"])
	assert.Equal(t, repo1Dir, store.ctxKeys["child_repo_path:alpha"])
	assert.Equal(t, repo2Dir, store.ctxKeys["child_repo_path:beta"])
	// With two repos, bare child_branch is NOT written.
	_, hasBareBranch := store.ctxKeys["child_branch"]
	assert.False(t, hasBareBranch, "bare child_branch should not be written for multi-repo")
}

// TestDaemonExecutor_PrepareExtractWorktrees_RollbackOnFailure verifies that
// when prepareExtractWorktreeFn succeeds for the first repo but errors for the
// second, prepareExtractWorktrees returns a wrapped error mentioning the failing
// repo name, no worktrees entry is kept for the pool key, and no KV keys are written.
func TestDaemonExecutor_PrepareExtractWorktrees_RollbackOnFailure(t *testing.T) {
	tmpDir := t.TempDir()

	repo1Dir := filepath.Join(tmpDir, "repos", "alpha")
	repo2Dir := filepath.Join(tmpDir, "repos", "beta")
	initGitRepo(t, repo1Dir)
	initGitRepo(t, repo2Dir)

	writeConfigTOML(t, tmpDir, [][2]string{
		{"alpha", "./repos/alpha"},
		{"beta", "./repos/beta"},
	})

	callCount := 0
	origPrepare := prepareExtractWorktreeFn
	prepareExtractWorktreeFn = func(_ context.Context, opts docker.PrepareOptions) (docker.ExtractWorktree, error) {
		callCount++
		if callCount == 1 {
			return docker.ExtractWorktree{Dir: opts.TargetDir + "-first", Branch: opts.Branch}, nil
		}
		return docker.ExtractWorktree{}, fmt.Errorf("simulated prepare failure for beta")
	}
	t.Cleanup(func() { prepareExtractWorktreeFn = origPrepare })

	store := &recordingRunStore{ctxKeys: map[string]string{}}
	containerWF := buildContainerWFForTest("develop")
	poolKey := "att-rollback:" + containerWF.ContainerID()

	de := NewDaemonExecutor(DaemonExecutorConfig{
		Store:      store,
		ProjectDir: tmpDir,
		TaskID:     "task-rollback",
		AttemptID:  "att-rollback",
		AllWFs:     map[string]*domain.Workflow{"develop": containerWF},
	})

	err := de.prepareExtractWorktrees(context.Background(), poolKey, containerWF)
	require.Error(t, err, "should return error when second prepare fails")
	assert.Contains(t, err.Error(), "beta", "error should mention the failing repo name")

	_, exists := de.worktrees[poolKey]
	assert.False(t, exists, "no worktrees entry should remain after rollback")

	assert.Empty(t, store.ctxKeys, "no KV keys should be written after rollback")
}

// TestDaemonExecutor_PrepareExtractWorktrees_SingleRepoBackCompat verifies that
// with exactly one [[repositories]] entry, writeRepoBranchKV writes both the
// namespaced child_branch:<name> AND the bare child_branch key.
func TestDaemonExecutor_PrepareExtractWorktrees_SingleRepoBackCompat(t *testing.T) {
	tmpDir := t.TempDir()

	repoDir := filepath.Join(tmpDir, "repos", "main")
	initGitRepo(t, repoDir)

	writeConfigTOML(t, tmpDir, [][2]string{
		{"main", "./repos/main"},
	})

	origPrepare := prepareExtractWorktreeFn
	prepareExtractWorktreeFn = func(_ context.Context, opts docker.PrepareOptions) (docker.ExtractWorktree, error) {
		return docker.ExtractWorktree{Dir: opts.TargetDir, Branch: opts.Branch}, nil
	}
	t.Cleanup(func() { prepareExtractWorktreeFn = origPrepare })

	store := &recordingRunStore{ctxKeys: map[string]string{}}
	containerWF := buildContainerWFForTest("develop")
	poolKey := "att-compat:" + containerWF.ContainerID()

	de := NewDaemonExecutor(DaemonExecutorConfig{
		Store:      store,
		ProjectDir: tmpDir,
		TaskID:     "task-compat",
		AttemptID:  "att-compat",
		AllWFs:     map[string]*domain.Workflow{"develop": containerWF},
	})

	err := de.prepareExtractWorktrees(context.Background(), poolKey, containerWF)
	require.NoError(t, err)

	namespacedBranch := store.ctxKeys["child_branch:main"]
	require.NotEmpty(t, namespacedBranch, "child_branch:main should be written")
	assert.Equal(t, namespacedBranch, store.ctxKeys["child_branch"],
		"bare child_branch should equal child_branch:main for single-repo back-compat")
}

// TestDaemonExecutor_AggregateHostSubWorkflowLogs verifies that after a host
// sub-workflow runs, aggregateHostSubWorkflowLogs creates a single log file
// named after the workflow step containing all inner step outputs.
func TestDaemonExecutor_AggregateHostSubWorkflowLogs(t *testing.T) {
	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, ".cloche", "logs", "task1", "att1")
	require.NoError(t, os.MkdirAll(logDir, 0755))

	// Write two inner step log files.
	require.NoError(t, os.WriteFile(filepath.Join(logDir, "checkout.log"), []byte("checkout output\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(logDir, "build.log"), []byte("build output\n"), 0644))

	de := &DaemonExecutor{
		projectDir: tmpDir,
		taskID:     "task1",
		attemptID:  "att1",
	}

	run := &domain.Run{}
	run.RecordStepStart("checkout")
	run.RecordStepComplete("checkout", "success")
	run.RecordStepStart("build")
	run.RecordStepComplete("build", "success")

	de.aggregateHostSubWorkflowLogs("finalize", run)

	// The aggregated file should exist and contain both step outputs.
	data, err := os.ReadFile(filepath.Join(logDir, "finalize.log"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "checkout output")
	assert.Contains(t, string(data), "build output")
}

// TestDaemonExecutor_AggregateHostSubWorkflowLogs_SkipsWhenNoTaskID verifies
// that aggregation is a no-op when taskID or attemptID is empty.
func TestDaemonExecutor_AggregateHostSubWorkflowLogs_SkipsWhenNoTaskID(t *testing.T) {
	tmpDir := t.TempDir()

	de := &DaemonExecutor{
		projectDir: tmpDir,
		taskID:     "", // empty → should skip
		attemptID:  "att1",
	}

	run := &domain.Run{}
	run.RecordStepStart("step1")
	run.RecordStepComplete("step1", "success")

	// Should not panic or create any file.
	de.aggregateHostSubWorkflowLogs("outer", run)
	_, err := os.Stat(filepath.Join(tmpDir, "outer.log"))
	assert.True(t, os.IsNotExist(err))
}

// TestInnerHostStatusHandler_BroadcastsEvents verifies that the
// innerHostStatusHandler publishes step_started, script content, and
// step_completed events to the broadcaster under the given hostRunID.
func TestInnerHostStatusHandler_BroadcastsEvents(t *testing.T) {
	tmpDir := t.TempDir()

	broadcaster := logstream.NewBroadcaster()
	broadcaster.Start("host-run-1")

	// Write a step log file that the handler will read on step completion.
	logDir := filepath.Join(tmpDir, ".cloche", "logs", "task1", "att1")
	require.NoError(t, os.MkdirAll(logDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(logDir, "checkout.log"), []byte("git clone ok\n"), 0644))

	h := &innerHostStatusHandler{
		logBroadcast: broadcaster,
		hostRunID:    "host-run-1",
		outputDir:    logDir,
	}

	sub, _ := broadcaster.SubscribeWithHistory("host-run-1")
	defer broadcaster.Unsubscribe("host-run-1", sub)

	step := &domain.Step{Name: "checkout", Type: domain.StepTypeScript}
	h.OnStepStart(nil, step)
	h.OnStepComplete(nil, step, "success", nil)

	// Collect published lines from channel (non-blocking).
	var lines []logstream.LogLine
	for {
		select {
		case line, ok := <-sub.C:
			if !ok {
				goto done
			}
			lines = append(lines, line)
		default:
			goto done
		}
	}
done:

	require.GreaterOrEqual(t, len(lines), 3, "expected at least 3 lines (start + script + complete)")
	assert.Equal(t, "status", lines[0].Type)
	assert.Contains(t, lines[0].Content, "step_started: checkout")
	assert.Equal(t, "script", lines[1].Type)
	assert.Contains(t, lines[1].Content, "git clone ok")
	assert.Equal(t, "status", lines[2].Type)
	assert.Contains(t, lines[2].Content, "step_completed: checkout -> success")
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

// gitCmd runs a git command in dir, failing the test on error.
func gitCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := execCommand("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test",
	)
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "git %v: %s", args, out)
	return strings.TrimSpace(string(out))
}

// TestDaemonExecutor_ContainerProjectDir_NoSeedSHA verifies that when no
// ContainerSeedSHA is configured (e.g. non-git projects, or callers that
// don't opt in), containerProjectDir returns the live projectDir unchanged —
// preserving the pre-fix behavior.
func TestDaemonExecutor_ContainerProjectDir_NoSeedSHA(t *testing.T) {
	tmpDir := t.TempDir()
	de := NewDaemonExecutor(DaemonExecutorConfig{ProjectDir: tmpDir})

	assert.Equal(t, tmpDir, de.containerProjectDir(context.Background()))
}

// TestDaemonExecutor_ContainerProjectDir_SurvivesLaterCheckout is the
// regression test for the "project copy into sub-workflow containers misses
// files" bug: a host workflow step (e.g. vertical-prepare-design-branch.sh)
// runs `git checkout -B <branch> <base>` directly against the shared project
// working tree before a later step dispatches a container sub-workflow. If
// the container copy reads the live tree at that point, it inherits whatever
// happens to be checked out — which can be missing files that existed when
// the run started. containerProjectDir must instead snapshot the SHA
// projectDir was at when the executor was built, before any step ran.
func TestDaemonExecutor_ContainerProjectDir_SurvivesLaterCheckout(t *testing.T) {
	tmpDir := t.TempDir()
	gitCmd(t, tmpDir, "init")

	// Commit 1: only the file that predates the run's new addition.
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "develop.cloche"), []byte("old"), 0644))
	gitCmd(t, tmpDir, "add", "develop.cloche")
	gitCmd(t, tmpDir, "commit", "-m", "commit1")

	// Commit 2: the new file that must survive into the container copy —
	// present on disk when "cloche run vertical" is invoked.
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "vertical.cloche"), []byte("new"), 0644))
	gitCmd(t, tmpDir, "add", "vertical.cloche")
	gitCmd(t, tmpDir, "commit", "-m", "commit2")
	seedSHA := gitCmd(t, tmpDir, "rev-parse", "HEAD")

	de := NewDaemonExecutor(DaemonExecutorConfig{
		ProjectDir:       tmpDir,
		ContainerSeedSHA: seedSHA,
	})

	// Simulate a host step (vertical-prepare-design-branch.sh) checking out a
	// branch based on the pre-vertical.cloche commit, directly on the shared
	// working tree — exactly what happens between "prepare-design-branch" and
	// "bdd-test-plan" in vertical.cloche.
	gitCmd(t, tmpDir, "checkout", "-B", "vertical/feat/design", "HEAD~1")
	require.NoFileExists(t, filepath.Join(tmpDir, "vertical.cloche"),
		"sanity check: checkout should have removed vertical.cloche from the live tree")

	seedDir := de.containerProjectDir(context.Background())
	assert.NotEqual(t, tmpDir, seedDir, "should return a snapshot dir, not the live (mutated) tree")
	assert.FileExists(t, filepath.Join(seedDir, "vertical.cloche"),
		"container copy must still see files present when the run started, despite the later checkout")
	assert.FileExists(t, filepath.Join(seedDir, "develop.cloche"))

	de.Close(true)
	assert.NoDirExists(t, seedDir, "Close should clean up the snapshot dir")
}

// TestDaemonExecutor_ContainerProjectDir_Memoized verifies the snapshot is
// only materialized once per executor and reused across calls (each
// container sub-workflow step in an attempt calls containerProjectDir again).
func TestDaemonExecutor_ContainerProjectDir_Memoized(t *testing.T) {
	tmpDir := t.TempDir()
	gitCmd(t, tmpDir, "init")
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "a.txt"), []byte("a"), 0644))
	gitCmd(t, tmpDir, "add", "a.txt")
	gitCmd(t, tmpDir, "commit", "-m", "init")
	seedSHA := gitCmd(t, tmpDir, "rev-parse", "HEAD")

	de := NewDaemonExecutor(DaemonExecutorConfig{
		ProjectDir:       tmpDir,
		ContainerSeedSHA: seedSHA,
	})

	first := de.containerProjectDir(context.Background())
	second := de.containerProjectDir(context.Background())
	assert.Equal(t, first, second, "the snapshot should be materialized once and reused")
}

// TestDaemonExecutor_ContainerProjectDir_InvalidSHAFallsBack verifies that a
// bad seed SHA (snapshot creation failure) falls back to the live projectDir
// instead of breaking the container copy entirely.
func TestDaemonExecutor_ContainerProjectDir_InvalidSHAFallsBack(t *testing.T) {
	tmpDir := t.TempDir()
	gitCmd(t, tmpDir, "init")
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "a.txt"), []byte("a"), 0644))
	gitCmd(t, tmpDir, "add", "a.txt")
	gitCmd(t, tmpDir, "commit", "-m", "init")

	de := NewDaemonExecutor(DaemonExecutorConfig{
		ProjectDir:       tmpDir,
		ContainerSeedSHA: "not-a-real-sha",
	})

	assert.Equal(t, tmpDir, de.containerProjectDir(context.Background()))
}

// TestDaemonExecutor_ExecuteContainerStep_UsesSnapshotProjectDir exercises
// the fix through the full Execute path (not just containerProjectDir
// directly): a container sub-workflow step must receive the pre-run snapshot
// as ProjectDir, not the live tree, even after it's been checked out
// elsewhere by an earlier host step.
func TestDaemonExecutor_ExecuteContainerStep_UsesSnapshotProjectDir(t *testing.T) {
	tmpDir := t.TempDir()
	gitCmd(t, tmpDir, "init")
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "develop.cloche"), []byte("old"), 0644))
	gitCmd(t, tmpDir, "add", "develop.cloche")
	gitCmd(t, tmpDir, "commit", "-m", "commit1")
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "vertical.cloche"), []byte("new"), 0644))
	gitCmd(t, tmpDir, "add", "vertical.cloche")
	gitCmd(t, tmpDir, "commit", "-m", "commit2")
	seedSHA := gitCmd(t, tmpDir, "rev-parse", "HEAD")

	containerWF := buildContainerWFForTest("develop")
	allWFs := map[string]*domain.Workflow{"develop": containerWF}

	rt := &recordingContainerRuntime{}
	pool := docker.NewContainerPool(rt)

	de := NewDaemonExecutor(DaemonExecutorConfig{
		Pool:             pool,
		ProjectDir:       tmpDir,
		ContainerSeedSHA: seedSHA,
		TaskID:           "task-1",
		AttemptID:        "att-1",
		AllWFs:           allWFs,
	})

	// Host step mutates the shared tree before the container step runs.
	gitCmd(t, tmpDir, "checkout", "-B", "vertical/feat/design", "HEAD~1")

	step := &domain.Step{
		Name:    "develop",
		Type:    domain.StepTypeWorkflow,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"workflow_name": "develop"},
	}
	mainWF := buildHostWFForTest("main")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, _ = de.Execute(engine.WithWorkflow(ctx, mainWF), step)

	require.True(t, rt.startCalled)
	assert.NotEqual(t, tmpDir, rt.lastConfig.ProjectDir)
	assert.FileExists(t, filepath.Join(rt.lastConfig.ProjectDir, "vertical.cloche"),
		"the container should be seeded from the pre-run snapshot, not the live checked-out tree")
}
