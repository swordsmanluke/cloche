package integration_test

// workflow_refactor_test.go covers the full refactored dispatch flow:
//   - daemon walks a host main workflow that dispatches a container develop workflow
//   - container steps share state (KV store)
//   - workflow_name dispatch works bidirectionally (host→container, container→host)
//   - container reuse: same container.id → same container per attempt
//   - container separation: different container.id → different containers
//   - default container sharing: no explicit id → shared DefaultContainerID
//   - resume across attempts with image commit
//   - error handling: agent disconnect, container startup failure

import (
	"context"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	pb "github.com/cloche-dev/cloche/api/clochepb"
	"github.com/cloche-dev/cloche/internal/adapters/docker"
	grpcadapter "github.com/cloche-dev/cloche/internal/adapters/grpc"
	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/engine"
	"github.com/cloche-dev/cloche/internal/host"
	"github.com/cloche-dev/cloche/internal/ports"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Fake container runtime
// ---------------------------------------------------------------------------

// fakeContainerRuntime implements ports.ContainerRuntime (and the private
// containerResumer interface used by CommitForResume) for integration tests.
type fakeContainerRuntime struct {
	mu        sync.Mutex
	started   []string
	startErr  error
	idCounter int
	// startedCh receives container IDs as containers are started; buffered so
	// Start never blocks.
	startedCh chan string

	// resume support (implements docker's unexported containerResumer interface)
	committed map[string]string // containerID → imageTag
	commitErr error
}

func newFakeRuntime() *fakeContainerRuntime {
	return &fakeContainerRuntime{
		startedCh: make(chan string, 16),
	}
}

func (f *fakeContainerRuntime) Start(_ context.Context, _ ports.ContainerConfig) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.startErr != nil {
		return "", f.startErr
	}
	f.idCounter++
	id := fmt.Sprintf("ctr-%d", f.idCounter)
	f.started = append(f.started, id)
	select {
	case f.startedCh <- id:
	default:
	}
	return id, nil
}

func (f *fakeContainerRuntime) Stop(_ context.Context, _ string) error   { return nil }
func (f *fakeContainerRuntime) Remove(_ context.Context, _ string) error { return nil }
func (f *fakeContainerRuntime) AttachOutput(_ context.Context, _ string) (io.ReadCloser, error) {
	return io.NopCloser(nil), nil
}
func (f *fakeContainerRuntime) Wait(_ context.Context, _ string) (int, error) { return 0, nil }
func (f *fakeContainerRuntime) CopyFrom(_ context.Context, _, _, _ string) error { return nil }
func (f *fakeContainerRuntime) Logs(_ context.Context, _ string) (string, error) { return "", nil }
func (f *fakeContainerRuntime) Inspect(_ context.Context, _ string) (*ports.ContainerStatus, error) {
	return &ports.ContainerStatus{Running: true}, nil
}
func (f *fakeContainerRuntime) Attach(_ context.Context, _ string) (io.ReadWriteCloser, error) {
	return nil, nil
}

// CommitContainer satisfies the private docker.containerResumer interface used
// by ContainerPool.CommitForResume.
func (f *fakeContainerRuntime) CommitContainer(_ context.Context, containerID, attemptID string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.commitErr != nil {
		return "", f.commitErr
	}
	tag := fmt.Sprintf("cloche-resume:%s-%s", attemptID, containerID)
	if f.committed == nil {
		f.committed = make(map[string]string)
	}
	f.committed[containerID] = tag
	return tag, nil
}

// RemoveImage satisfies the private docker.containerResumer interface.
func (f *fakeContainerRuntime) RemoveImage(_ context.Context, _ string) error { return nil }

func (f *fakeContainerRuntime) startedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.started)
}

// ---------------------------------------------------------------------------
// Fake run store
// ---------------------------------------------------------------------------

// fakeRunStore is a thread-safe in-memory ports.RunStore for tests.
type fakeRunStore struct {
	mu     sync.Mutex
	runs   map[string]*domain.Run
	kvData map[string]string
}

func newFakeRunStore() *fakeRunStore {
	return &fakeRunStore{
		runs:   make(map[string]*domain.Run),
		kvData: make(map[string]string),
	}
}

func (s *fakeRunStore) CreateRun(_ context.Context, run *domain.Run) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runs[run.ID] = run
	return nil
}
func (s *fakeRunStore) GetRun(_ context.Context, id string) (*domain.Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r, ok := s.runs[id]; ok {
		return r, nil
	}
	return nil, fmt.Errorf("run %s not found", id)
}
func (s *fakeRunStore) GetRunByAttempt(_ context.Context, attemptID, id string) (*domain.Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r, ok := s.runs[id]; ok && r.AttemptID == attemptID {
		return r, nil
	}
	return nil, fmt.Errorf("run not found")
}
func (s *fakeRunStore) UpdateRun(_ context.Context, run *domain.Run) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runs[run.ID] = run
	return nil
}
func (s *fakeRunStore) DeleteRun(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.runs, id)
	return nil
}
func (s *fakeRunStore) ListRuns(_ context.Context, _ time.Time) ([]*domain.Run, error) {
	return nil, nil
}
func (s *fakeRunStore) ListRunsByProject(_ context.Context, _ string, _ time.Time) ([]*domain.Run, error) {
	return nil, nil
}
func (s *fakeRunStore) ListRunsFiltered(_ context.Context, _ domain.RunListFilter) ([]*domain.Run, error) {
	return nil, nil
}
func (s *fakeRunStore) ListProjects(_ context.Context) ([]string, error) { return nil, nil }
func (s *fakeRunStore) ListChildRuns(_ context.Context, parentRunID string) ([]*domain.Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*domain.Run
	for _, r := range s.runs {
		if r.ParentRunID == parentRunID {
			out = append(out, r)
		}
	}
	return out, nil
}
func (s *fakeRunStore) QueryUsage(_ context.Context, _ ports.UsageQuery) ([]domain.UsageSummary, error) {
	return nil, nil
}
func (s *fakeRunStore) GetContextKey(_ context.Context, taskID, attemptID, runID, key string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.kvData[taskID+"/"+attemptID+"/"+runID+"/"+key]
	return v, ok, nil
}
func (s *fakeRunStore) SetContextKey(_ context.Context, taskID, attemptID, runID, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.kvData[taskID+"/"+attemptID+"/"+runID+"/"+key] = value
	return nil
}
func (s *fakeRunStore) ListContextKeys(_ context.Context, _, _, _ string) ([]string, error) {
	return nil, nil
}
func (s *fakeRunStore) DeleteContextKeys(_ context.Context, _, _ string) error { return nil }
func (s *fakeRunStore) SaveAttempt(_ context.Context, _ *domain.Attempt) error { return nil }
func (s *fakeRunStore) GetAttempt(_ context.Context, _ string) (*domain.Attempt, error) {
	return nil, fmt.Errorf("not found")
}
func (s *fakeRunStore) ListAttempts(_ context.Context, _ string) ([]*domain.Attempt, error) {
	return nil, nil
}
func (s *fakeRunStore) FailStaleAttempts(_ context.Context) (int64, error) { return 0, nil }

// ---------------------------------------------------------------------------
// Agent simulation helpers
// ---------------------------------------------------------------------------

// makeAgentSendFn returns a send function that acts as a fake in-container agent:
// it reads ExecuteStep messages and synchronously delivers a StepResult based on
// stepResults (falls back to "success" for unknown step names).
func makeAgentSendFn(pool *docker.ContainerPool, containerID string, stepResults map[string]string) func(*pb.DaemonMessage) error {
	return func(msg *pb.DaemonMessage) error {
		exe := msg.GetExecuteStep()
		if exe == nil {
			return nil // Shutdown or other non-step messages; ignore
		}
		result := "success"
		if r, ok := stepResults[exe.StepName]; ok {
			result = r
		}
		pool.DeliverResult(containerID, &pb.StepResult{
			RequestId: exe.RequestId,
			Result:    result,
		})
		return nil
	}
}

// simulateAgentsAsync starts a background goroutine that watches rt.startedCh,
// and for each new container signals AgentReady with a sendFn derived from
// stepResults. The goroutine exits when done is closed.
func simulateAgentsAsync(pool *docker.ContainerPool, rt *fakeContainerRuntime, stepResults map[string]string, done <-chan struct{}) {
	go func() {
		for {
			select {
			case <-done:
				return
			case containerID := <-rt.startedCh:
				id := containerID
				// Brief sleep so SessionFor has time to register containerAttempt
				// before NotifyReadyWithStream looks it up.
				time.Sleep(20 * time.Millisecond)
				sendFn := makeAgentSendFn(pool, id, stepResults)
				pool.NotifyReadyWithStream(id, sendFn)
			}
		}
	}()
}

// ---------------------------------------------------------------------------
// Workflow construction helpers
// ---------------------------------------------------------------------------

// hostScript builds a simple host-location script step.
func hostScript(name, cmd string) *domain.Step {
	return &domain.Step{
		Name:    name,
		Type:    domain.StepTypeScript,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"run": cmd},
	}
}

// containerAgentStep builds a container-location agent step.
func containerAgentStep(name string) *domain.Step {
	return &domain.Step{
		Name:    name,
		Type:    domain.StepTypeAgent,
		Results: []string{"success", "fail"},
		Config:  map[string]string{},
	}
}

// workflowNameStep builds a step that dispatches to a named workflow.
func workflowNameStep(name, targetWorkflow string) *domain.Step {
	return &domain.Step{
		Name:    name,
		Type:    domain.StepTypeWorkflow,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"workflow_name": targetWorkflow},
	}
}

// successWire is a wire from:result "success" → to.
func successWire(from, to string) domain.Wire {
	return domain.Wire{From: from, Result: "success", To: to}
}

// buildHostWorkflow constructs a minimal host-location workflow.
func buildHostWorkflow(name string, steps []*domain.Step, wires []domain.Wire) *domain.Workflow {
	stepMap := make(map[string]*domain.Step, len(steps))
	for _, s := range steps {
		stepMap[s.Name] = s
	}
	return &domain.Workflow{
		Name:      name,
		Location:  domain.LocationHost,
		Steps:     stepMap,
		Wiring:    wires,
		EntryStep: steps[0].Name,
		Config:    map[string]string{},
	}
}

// buildContainerWorkflow constructs a minimal container-location workflow.
// containerID may be "" to use the default, or a non-empty string for an explicit id.
func buildContainerWorkflow(name, containerID string, steps []*domain.Step, wires []domain.Wire) *domain.Workflow {
	stepMap := make(map[string]*domain.Step, len(steps))
	for _, s := range steps {
		stepMap[s.Name] = s
	}
	cfg := map[string]string{}
	if containerID != "" {
		cfg["container.id"] = containerID
	}
	return &domain.Workflow{
		Name:      name,
		Location:  domain.LocationContainer,
		Steps:     stepMap,
		Wiring:    wires,
		EntryStep: steps[0].Name,
		Config:    cfg,
	}
}

// newDaemonExecutor wires up a DaemonExecutor backed by the given pool and
// host.Executor, ready to use in integration tests.
func newDaemonExecutor(tmpDir, attemptID string, pool *docker.ContainerPool, store *fakeRunStore, allWFs map[string]*domain.Workflow) *grpcadapter.DaemonExecutor {
	hostExec := &host.Executor{
		ProjectDir: tmpDir,
		OutputDir:  tmpDir + "/output",
		Store:      store,
		TaskID:     "task-1",
		AttemptID:  attemptID,
	}
	return grpcadapter.NewDaemonExecutor(grpcadapter.DaemonExecutorConfig{
		HostExec:   hostExec,
		Pool:       pool,
		ProjectDir: tmpDir,
		AttemptID:  attemptID,
		Image:      "test-image:latest",
		AllWFs:     allWFs,
	})
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestWorkflowRefactor_FullFlow verifies the complete refactored dispatch path:
// a host "main" workflow has a script step followed by a workflow_name step that
// dispatches to a container "develop" workflow. All steps succeed.
func TestWorkflowRefactor_FullFlow(t *testing.T) {
	tmpDir := t.TempDir()
	rt := newFakeRuntime()
	pool := docker.NewContainerPool(rt)
	store := newFakeRunStore()

	// develop: container workflow with one agent step
	developWF := buildContainerWorkflow("develop", "", []*domain.Step{
		containerAgentStep("implement"),
	}, []domain.Wire{
		successWire("implement", domain.StepDone),
		{From: "implement", Result: "fail", To: domain.StepAbort},
	})

	// main: host workflow — prepare (script) → dispatch develop → done
	mainWF := buildHostWorkflow("main", []*domain.Step{
		hostScript("prepare", "echo prepared"),
		workflowNameStep("dispatch_develop", "develop"),
	}, []domain.Wire{
		successWire("prepare", "dispatch_develop"),
		{From: "prepare", Result: "fail", To: domain.StepAbort},
		successWire("dispatch_develop", domain.StepDone),
		{From: "dispatch_develop", Result: "fail", To: domain.StepAbort},
	})

	allWFs := map[string]*domain.Workflow{"main": mainWF, "develop": developWF}

	done := make(chan struct{})
	defer close(done)
	simulateAgentsAsync(pool, rt, map[string]string{"implement": "success"}, done)

	de := newDaemonExecutor(tmpDir, "att-full", pool, store, allWFs)

	eng := engine.New(de)
	run, err := eng.Run(context.Background(), mainWF)

	require.NoError(t, err)
	assert.Equal(t, domain.RunStateSucceeded, run.State, "full flow should succeed")
	assert.Equal(t, 1, rt.startedCount(), "exactly one container should be started for develop")

	// prepare step ran: step executions recorded
	stepNames := make(map[string]string)
	for _, se := range run.StepExecutions {
		stepNames[se.StepName] = se.Result
	}
	assert.Equal(t, "success", stepNames["prepare"], "prepare step should succeed")
	assert.Equal(t, "success", stepNames["dispatch_develop"], "dispatch_develop step should succeed")
}

// TestWorkflowRefactor_BidirectionalDispatch tests workflow_name dispatch in
// both directions: host → container → host. The main (host) workflow dispatches
// to develop (container), which dispatches to finalize (host).
func TestWorkflowRefactor_BidirectionalDispatch(t *testing.T) {
	tmpDir := t.TempDir()
	rt := newFakeRuntime()
	pool := docker.NewContainerPool(rt)
	store := newFakeRunStore()

	// finalize: host workflow
	finalizeWF := buildHostWorkflow("finalize", []*domain.Step{
		hostScript("cleanup", "echo finalized"),
	}, []domain.Wire{
		successWire("cleanup", domain.StepDone),
		{From: "cleanup", Result: "fail", To: domain.StepAbort},
	})

	// develop: container workflow — implement (agent) → dispatch finalize (workflow_name) → done
	developWF := buildContainerWorkflow("develop", "", []*domain.Step{
		containerAgentStep("implement"),
		workflowNameStep("dispatch_finalize", "finalize"),
	}, []domain.Wire{
		successWire("implement", "dispatch_finalize"),
		{From: "implement", Result: "fail", To: domain.StepAbort},
		successWire("dispatch_finalize", domain.StepDone),
		{From: "dispatch_finalize", Result: "fail", To: domain.StepAbort},
	})

	// main: host workflow → dispatch develop
	mainWF := buildHostWorkflow("main", []*domain.Step{
		hostScript("prepare", "echo prepared"),
		workflowNameStep("dispatch_develop", "develop"),
	}, []domain.Wire{
		successWire("prepare", "dispatch_develop"),
		{From: "prepare", Result: "fail", To: domain.StepAbort},
		successWire("dispatch_develop", domain.StepDone),
		{From: "dispatch_develop", Result: "fail", To: domain.StepAbort},
	})

	allWFs := map[string]*domain.Workflow{
		"main":     mainWF,
		"develop":  developWF,
		"finalize": finalizeWF,
	}

	done := make(chan struct{})
	defer close(done)
	simulateAgentsAsync(pool, rt, map[string]string{"implement": "success"}, done)

	de := newDaemonExecutor(tmpDir, "att-bidir", pool, store, allWFs)

	eng := engine.New(de)
	run, err := eng.Run(context.Background(), mainWF)

	require.NoError(t, err)
	assert.Equal(t, domain.RunStateSucceeded, run.State, "bidirectional dispatch should succeed")

	// Verify all three workflows ran by checking step executions in the top-level run.
	// The top-level run records host steps directly; sub-workflow steps are recorded
	// within the recursive engine.Run calls (not aggregated up), so we at minimum
	// check the main workflow steps succeed.
	stepResults := make(map[string]string)
	for _, se := range run.StepExecutions {
		stepResults[se.StepName] = se.Result
	}
	assert.Equal(t, "success", stepResults["prepare"])
	assert.Equal(t, "success", stepResults["dispatch_develop"])
}

// TestWorkflowRefactor_ContainerReuse_SameContainerID verifies that two
// container workflows sharing the same container.id reuse a single container
// session within the same attempt (pool key = attemptID + ":" + containerID).
func TestWorkflowRefactor_ContainerReuse_SameContainerID(t *testing.T) {
	tmpDir := t.TempDir()
	rt := newFakeRuntime()
	pool := docker.NewContainerPool(rt)
	store := newFakeRunStore()

	// Both wf1 and wf2 use the same container.id "shared-env".
	wf1 := buildContainerWorkflow("wf1", "shared-env", []*domain.Step{
		containerAgentStep("work1"),
	}, []domain.Wire{
		successWire("work1", domain.StepDone),
		{From: "work1", Result: "fail", To: domain.StepAbort},
	})
	wf2 := buildContainerWorkflow("wf2", "shared-env", []*domain.Step{
		containerAgentStep("work2"),
	}, []domain.Wire{
		successWire("work2", domain.StepDone),
		{From: "work2", Result: "fail", To: domain.StepAbort},
	})

	// main dispatches wf1 then wf2 sequentially.
	mainWF := buildHostWorkflow("main", []*domain.Step{
		workflowNameStep("dispatch_wf1", "wf1"),
		workflowNameStep("dispatch_wf2", "wf2"),
	}, []domain.Wire{
		successWire("dispatch_wf1", "dispatch_wf2"),
		{From: "dispatch_wf1", Result: "fail", To: domain.StepAbort},
		successWire("dispatch_wf2", domain.StepDone),
		{From: "dispatch_wf2", Result: "fail", To: domain.StepAbort},
	})

	allWFs := map[string]*domain.Workflow{
		"main": mainWF, "wf1": wf1, "wf2": wf2,
	}

	done := make(chan struct{})
	defer close(done)
	// The single container handles both work1 and work2.
	simulateAgentsAsync(pool, rt, map[string]string{"work1": "success", "work2": "success"}, done)

	de := newDaemonExecutor(tmpDir, "att-reuse", pool, store, allWFs)

	eng := engine.New(de)
	run, err := eng.Run(context.Background(), mainWF)

	require.NoError(t, err)
	assert.Equal(t, domain.RunStateSucceeded, run.State)
	// Only one container should have been started because both workflows share
	// the same container.id "shared-env".
	assert.Equal(t, 1, rt.startedCount(), "same container.id should reuse one container")
}

// TestWorkflowRefactor_ContainerSeparation_DifferentContainerID verifies that
// two container workflows with different container.id values start separate
// containers within the same attempt.
func TestWorkflowRefactor_ContainerSeparation_DifferentContainerID(t *testing.T) {
	tmpDir := t.TempDir()
	rt := newFakeRuntime()
	pool := docker.NewContainerPool(rt)
	store := newFakeRunStore()

	// wf3 uses "env-a"; wf4 uses "env-b" — they must get separate containers.
	wf3 := buildContainerWorkflow("wf3", "env-a", []*domain.Step{
		containerAgentStep("step_a"),
	}, []domain.Wire{
		successWire("step_a", domain.StepDone),
		{From: "step_a", Result: "fail", To: domain.StepAbort},
	})
	wf4 := buildContainerWorkflow("wf4", "env-b", []*domain.Step{
		containerAgentStep("step_b"),
	}, []domain.Wire{
		successWire("step_b", domain.StepDone),
		{From: "step_b", Result: "fail", To: domain.StepAbort},
	})

	mainWF := buildHostWorkflow("main", []*domain.Step{
		workflowNameStep("dispatch_wf3", "wf3"),
		workflowNameStep("dispatch_wf4", "wf4"),
	}, []domain.Wire{
		successWire("dispatch_wf3", "dispatch_wf4"),
		{From: "dispatch_wf3", Result: "fail", To: domain.StepAbort},
		successWire("dispatch_wf4", domain.StepDone),
		{From: "dispatch_wf4", Result: "fail", To: domain.StepAbort},
	})

	allWFs := map[string]*domain.Workflow{
		"main": mainWF, "wf3": wf3, "wf4": wf4,
	}

	done := make(chan struct{})
	defer close(done)
	simulateAgentsAsync(pool, rt, map[string]string{"step_a": "success", "step_b": "success"}, done)

	de := newDaemonExecutor(tmpDir, "att-sep", pool, store, allWFs)

	eng := engine.New(de)
	run, err := eng.Run(context.Background(), mainWF)

	require.NoError(t, err)
	assert.Equal(t, domain.RunStateSucceeded, run.State)
	// Two distinct container.ids → two containers.
	assert.Equal(t, 2, rt.startedCount(), "different container.id should start separate containers")
}

// TestWorkflowRefactor_DefaultContainerSharing verifies that container workflows
// without an explicit container.id both use domain.DefaultContainerID and
// therefore share a single container session.
func TestWorkflowRefactor_DefaultContainerSharing(t *testing.T) {
	tmpDir := t.TempDir()
	rt := newFakeRuntime()
	pool := docker.NewContainerPool(rt)
	store := newFakeRunStore()

	// Neither wf5 nor wf6 sets a container.id — both resolve to DefaultContainerID.
	wf5 := buildContainerWorkflow("wf5", "", []*domain.Step{
		containerAgentStep("step5"),
	}, []domain.Wire{
		successWire("step5", domain.StepDone),
		{From: "step5", Result: "fail", To: domain.StepAbort},
	})
	wf6 := buildContainerWorkflow("wf6", "", []*domain.Step{
		containerAgentStep("step6"),
	}, []domain.Wire{
		successWire("step6", domain.StepDone),
		{From: "step6", Result: "fail", To: domain.StepAbort},
	})

	assert.Equal(t, domain.DefaultContainerID, wf5.ContainerID())
	assert.Equal(t, domain.DefaultContainerID, wf6.ContainerID())

	mainWF := buildHostWorkflow("main", []*domain.Step{
		workflowNameStep("dispatch_wf5", "wf5"),
		workflowNameStep("dispatch_wf6", "wf6"),
	}, []domain.Wire{
		successWire("dispatch_wf5", "dispatch_wf6"),
		{From: "dispatch_wf5", Result: "fail", To: domain.StepAbort},
		successWire("dispatch_wf6", domain.StepDone),
		{From: "dispatch_wf6", Result: "fail", To: domain.StepAbort},
	})

	allWFs := map[string]*domain.Workflow{
		"main": mainWF, "wf5": wf5, "wf6": wf6,
	}

	done := make(chan struct{})
	defer close(done)
	simulateAgentsAsync(pool, rt, map[string]string{"step5": "success", "step6": "success"}, done)

	de := newDaemonExecutor(tmpDir, "att-default", pool, store, allWFs)

	eng := engine.New(de)
	run, err := eng.Run(context.Background(), mainWF)

	require.NoError(t, err)
	assert.Equal(t, domain.RunStateSucceeded, run.State)
	// Default container sharing: only one container started.
	assert.Equal(t, 1, rt.startedCount(), "default container.id should share one container")
}

// TestWorkflowRefactor_KVStateSharing verifies that the host executor seeds
// context keys into the KV store so subsequent steps can observe shared state.
func TestWorkflowRefactor_KVStateSharing(t *testing.T) {
	tmpDir := t.TempDir()
	rt := newFakeRuntime()
	pool := docker.NewContainerPool(rt)
	store := newFakeRunStore()

	// A simple host workflow: two script steps so the executor seeds context.
	developWF := buildContainerWorkflow("develop", "", []*domain.Step{
		containerAgentStep("implement"),
	}, []domain.Wire{
		successWire("implement", domain.StepDone),
		{From: "implement", Result: "fail", To: domain.StepAbort},
	})

	mainWF := buildHostWorkflow("main", []*domain.Step{
		hostScript("prepare", "echo prepared"),
		workflowNameStep("dispatch_develop", "develop"),
	}, []domain.Wire{
		successWire("prepare", "dispatch_develop"),
		{From: "prepare", Result: "fail", To: domain.StepAbort},
		successWire("dispatch_develop", domain.StepDone),
		{From: "dispatch_develop", Result: "fail", To: domain.StepAbort},
	})

	allWFs := map[string]*domain.Workflow{"main": mainWF, "develop": developWF}

	done := make(chan struct{})
	defer close(done)
	simulateAgentsAsync(pool, rt, nil, done)

	de := newDaemonExecutor(tmpDir, "att-kv", pool, store, allWFs)

	eng := engine.New(de)
	run, err := eng.Run(context.Background(), mainWF)
	require.NoError(t, err)
	assert.Equal(t, domain.RunStateSucceeded, run.State)

	// The host.Executor seeds "task_id", "attempt_id", "workflow", "run_id"
	// when TaskID is set. Verify these are present.
	ctx := context.Background()
	val, ok, err := store.GetContextKey(ctx, "task-1", "att-kv", "", "attempt_id")
	require.NoError(t, err)
	assert.True(t, ok, "attempt_id should be seeded into the KV store")
	assert.Equal(t, "att-kv", val)

	// Verify task_id is also seeded.
	val, ok, err = store.GetContextKey(ctx, "task-1", "att-kv", "", "task_id")
	require.NoError(t, err)
	assert.True(t, ok, "task_id should be seeded into the KV store")
	assert.Equal(t, "task-1", val)
}

// TestWorkflowRefactor_ResumeAcrossAttempts_ImageCommit verifies the resume
// flow: a container is committed to an image at the end of attempt 1, then
// attempt 2 starts a fresh container from that image.
func TestWorkflowRefactor_ResumeAcrossAttempts_ImageCommit(t *testing.T) {
	rt := newFakeRuntime()
	pool := docker.NewContainerPool(rt)

	ctx := context.Background()
	attemptID := "att-resume-1"
	poolKey := attemptID + ":" + domain.DefaultContainerID

	// Simulate starting a container for attempt 1.
	go func() {
		select {
		case containerID := <-rt.startedCh:
			time.Sleep(20 * time.Millisecond)
			pool.NotifyReady(containerID)
		case <-time.After(5 * time.Second):
		}
	}()

	sess, err := pool.SessionFor(ctx, poolKey, ports.ContainerConfig{
		Image:     "base-image",
		AttemptID: attemptID,
	})
	require.NoError(t, err)
	require.NotNil(t, sess)

	// Commit the container to an image.
	images, err := pool.CommitForResume(ctx, poolKey)
	require.NoError(t, err)
	require.Len(t, images, 1, "one container should have been committed")

	committedTag, ok := images[sess.ContainerID]
	require.True(t, ok, "committed images map must include the session container ID")
	assert.Contains(t, committedTag, "cloche-resume:")

	// Attempt 2: start from the committed image.
	attemptID2 := "att-resume-2"
	poolKey2 := attemptID2 + ":" + domain.DefaultContainerID

	go func() {
		select {
		case containerID := <-rt.startedCh:
			time.Sleep(20 * time.Millisecond)
			pool.NotifyReady(containerID)
		case <-time.After(5 * time.Second):
		}
	}()

	sess2, err := pool.StartFromImage(ctx, poolKey2, committedTag, ports.ContainerConfig{
		ProjectDir: "/should-be-cleared",
		AttemptID:  attemptID2,
	})
	require.NoError(t, err)
	require.NotNil(t, sess2)
	assert.NotEqual(t, sess.ContainerID, sess2.ContainerID, "resume must start a new container")

	// Two containers total (one per attempt).
	assert.Equal(t, 2, rt.startedCount())
}

// TestWorkflowRefactor_AgentDisconnect verifies that when an agent disconnects
// mid-step (FailPendingRequests), all pending ExecuteStep calls return "fail"
// rather than blocking indefinitely.
func TestWorkflowRefactor_AgentDisconnect(t *testing.T) {
	rt := newFakeRuntime()
	pool := docker.NewContainerPool(rt)

	ctx := context.Background()
	poolKey := "att-disc:" + domain.DefaultContainerID

	// Stalling sendFn: receives ExecuteStep messages but never delivers results.
	var capturedContainerID string
	go func() {
		select {
		case containerID := <-rt.startedCh:
			time.Sleep(20 * time.Millisecond)
			capturedContainerID = containerID
			stallingFn := func(msg *pb.DaemonMessage) error {
				return nil // do nothing; result never delivered
			}
			pool.NotifyReadyWithStream(containerID, stallingFn)
		case <-time.After(5 * time.Second):
		}
	}()

	sess, err := pool.SessionFor(ctx, poolKey, ports.ContainerConfig{Image: "img"})
	require.NoError(t, err)

	step := &domain.Step{
		Name:    "stalled_step",
		Type:    domain.StepTypeAgent,
		Results: []string{"success", "fail"},
		Config:  map[string]string{},
	}

	// Run ExecuteStep in a goroutine; it will block because the stalling sendFn
	// never delivers a result.
	type stepOutcome struct {
		result domain.StepResult
		err    error
	}
	ch := make(chan stepOutcome, 1)
	go func() {
		result, err := sess.ExecuteStep(ctx, step, false)
		ch <- stepOutcome{result, err}
	}()

	// Allow the goroutine to block on ExecuteStep.
	time.Sleep(50 * time.Millisecond)

	// Simulate agent disconnect: fail all pending requests.
	pool.FailPendingRequests(capturedContainerID)

	// ExecuteStep should now return with result "fail".
	select {
	case outcome := <-ch:
		require.NoError(t, outcome.err)
		assert.Equal(t, "fail", outcome.result.Result, "disconnected agent should yield fail result")
	case <-time.After(2 * time.Second):
		t.Fatal("ExecuteStep did not unblock after FailPendingRequests")
	}
}

// TestWorkflowRefactor_ContainerStartupFailure verifies that a container
// runtime Start error propagates through the DaemonExecutor and engine,
// causing the workflow to fail rather than hanging.
func TestWorkflowRefactor_ContainerStartupFailure(t *testing.T) {
	tmpDir := t.TempDir()
	rt := newFakeRuntime()
	rt.startErr = fmt.Errorf("docker daemon unavailable")
	pool := docker.NewContainerPool(rt)
	store := newFakeRunStore()

	developWF := buildContainerWorkflow("develop", "", []*domain.Step{
		containerAgentStep("implement"),
	}, []domain.Wire{
		successWire("implement", domain.StepDone),
		{From: "implement", Result: "fail", To: domain.StepAbort},
	})

	mainWF := buildHostWorkflow("main", []*domain.Step{
		workflowNameStep("dispatch_develop", "develop"),
	}, []domain.Wire{
		successWire("dispatch_develop", domain.StepDone),
		{From: "dispatch_develop", Result: "fail", To: domain.StepAbort},
	})

	allWFs := map[string]*domain.Workflow{"main": mainWF, "develop": developWF}

	de := newDaemonExecutor(tmpDir, "att-startup-err", pool, store, allWFs)

	eng := engine.New(de)
	run, err := eng.Run(context.Background(), mainWF)

	// The engine should surface the error or abort.
	if err != nil {
		assert.Contains(t, err.Error(), "docker daemon unavailable")
	} else {
		// The engine may absorb the error and fail the run.
		assert.Equal(t, domain.RunStateFailed, run.State,
			"workflow should fail when container startup fails")
	}
	assert.Equal(t, 0, rt.startedCount(), "no container should have been started successfully")
}
