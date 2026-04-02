package integration_test

// kv_host_container_test.go verifies that KV values set on the host side
// (via cloche set / store.SetContextKey) are retrievable inside the container
// (via clo get / store.GetContextKey) — meaning the container is started with
// the correct CLOCHE_TASK_ID and CLOCHE_ATTEMPT_ID environment variables.

import (
	"context"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

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
// Config-capturing fake runtime
// ---------------------------------------------------------------------------

// kvTestRuntime wraps the fake runtime pattern but captures the ContainerConfig
// passed to Start so tests can inspect it.
type kvTestRuntime struct {
	mu        sync.Mutex
	configs   []ports.ContainerConfig
	idCounter int
	started   chan string
}

func newKVTestRuntime() *kvTestRuntime {
	return &kvTestRuntime{
		started: make(chan string, 16),
	}
}

func (r *kvTestRuntime) Start(_ context.Context, cfg ports.ContainerConfig) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.idCounter++
	id := fmt.Sprintf("kv-ctr-%d", r.idCounter)
	r.configs = append(r.configs, cfg)
	select {
	case r.started <- id:
	default:
	}
	return id, nil
}

func (r *kvTestRuntime) Stop(_ context.Context, _ string) error   { return nil }
func (r *kvTestRuntime) Remove(_ context.Context, _ string) error { return nil }
func (r *kvTestRuntime) AttachOutput(_ context.Context, _ string) (io.ReadCloser, error) {
	return io.NopCloser(nil), nil
}
func (r *kvTestRuntime) Wait(_ context.Context, _ string) (int, error) { return 0, nil }
func (r *kvTestRuntime) CopyFrom(_ context.Context, _, _, _ string) error { return nil }
func (r *kvTestRuntime) Logs(_ context.Context, _ string) (string, error) { return "", nil }
func (r *kvTestRuntime) Inspect(_ context.Context, _ string) (*ports.ContainerStatus, error) {
	return &ports.ContainerStatus{Running: true}, nil
}
func (r *kvTestRuntime) Attach(_ context.Context, _ string) (io.ReadWriteCloser, error) {
	return nil, nil
}
func (r *kvTestRuntime) CommitContainer(_ context.Context, containerID, attemptID string) (string, error) {
	return fmt.Sprintf("cloche-resume:%s-%s", attemptID, containerID), nil
}
func (r *kvTestRuntime) RemoveImage(_ context.Context, _ string) error { return nil }

func (r *kvTestRuntime) capturedConfigs() []ports.ContainerConfig {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]ports.ContainerConfig, len(r.configs))
	copy(out, r.configs)
	return out
}

// simulateKVAgents starts a background goroutine that watches rt.started,
// and for each new container signals AgentReady with a sendFn derived from
// stepResults. The goroutine exits when done is closed.
func simulateKVAgents(pool *docker.ContainerPool, rt *kvTestRuntime, stepResults map[string]string, done <-chan struct{}) {
	go func() {
		for {
			select {
			case <-done:
				return
			case containerID := <-rt.started:
				id := containerID
				time.Sleep(20 * time.Millisecond)
				sendFn := makeAgentSendFn(pool, id, stepResults)
				pool.NotifyReadyWithStream(id, sendFn)
			}
		}
	}()
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestKV_HostToContainer_TaskIDPropagated verifies that when a host workflow
// dispatches to a container workflow, the container is started with the correct
// TaskID so that clo get can read CLOCHE_TASK_ID from the environment.
//
// This is the regression test for the bug where executeContainerStep did not
// pass TaskID in ContainerConfig, causing CLOCHE_TASK_ID to be absent in the
// container environment and making clo get fail with "key not found".
func TestKV_HostToContainer_TaskIDPropagated(t *testing.T) {
	tmpDir := t.TempDir()
	rt := newKVTestRuntime()
	pool := docker.NewContainerPool(rt)
	store := newFakeRunStore()

	const taskID = "task-kv-1"
	const attemptID = "att-kv-1"

	// Simulate host-side KV set (what cloche set does):
	ctx := context.Background()
	err := store.SetContextKey(ctx, taskID, attemptID, "prompt_file", "/workspace/.cloche/runs/task-kv-1/prompt.txt")
	require.NoError(t, err)

	// Container workflow: one agent step
	developWF := buildContainerWorkflow("develop", "", []*domain.Step{
		containerAgentStep("implement"),
	}, []domain.Wire{
		successWire("implement", domain.StepDone),
		{From: "implement", Result: "fail", To: domain.StepAbort},
	})

	// Host workflow: dispatch to container
	mainWF := buildHostWorkflow("main", []*domain.Step{
		workflowNameStep("dispatch_develop", "develop"),
	}, []domain.Wire{
		successWire("dispatch_develop", domain.StepDone),
		{From: "dispatch_develop", Result: "fail", To: domain.StepAbort},
	})

	allWFs := map[string]*domain.Workflow{"main": mainWF, "develop": developWF}

	// Simulate the in-container agent responding to steps.
	done := make(chan struct{})
	defer close(done)
	simulateKVAgents(pool, rt, map[string]string{"implement": "success"}, done)

	// Build DaemonExecutor with TaskID.
	hostExec := &host.Executor{
		ProjectDir: tmpDir,
		OutputDir:  tmpDir + "/output",
		Store:      store,
		TaskID:     taskID,
		AttemptID:  attemptID,
	}
	de := grpcadapter.NewDaemonExecutor(grpcadapter.DaemonExecutorConfig{
		HostExec:   hostExec,
		Pool:       pool,
		Store:      store,
		ProjectDir: tmpDir,
		TaskID:     taskID,
		AttemptID:  attemptID,
		Image:      "test-image:latest",
		AllWFs:     allWFs,
	})

	eng := engine.New(de)
	run, runErr := eng.Run(ctx, mainWF)

	require.NoError(t, runErr)
	assert.Equal(t, domain.RunStateSucceeded, run.State, "workflow should succeed")

	// --- Core assertion: the container was started with the correct TaskID ---
	configs := rt.capturedConfigs()
	require.Len(t, configs, 1, "exactly one container should have been started")
	assert.Equal(t, taskID, configs[0].TaskID,
		"container must receive TaskID so clo get can read CLOCHE_TASK_ID")
	assert.Equal(t, attemptID, configs[0].AttemptID,
		"container must receive AttemptID so clo get can read CLOCHE_ATTEMPT_ID")

	// --- Verify KV roundtrip: same task/attempt IDs should return the value ---
	val, found, kvErr := store.GetContextKey(ctx, taskID, attemptID, "prompt_file")
	require.NoError(t, kvErr)
	assert.True(t, found, "prompt_file key should be found in KV store")
	assert.Equal(t, "/workspace/.cloche/runs/task-kv-1/prompt.txt", val)
}

// TestKV_HostToContainer_StoreRoundtrip verifies that the store-level KV
// operations work correctly when host and container use the same task/attempt
// IDs, and that different attempts are properly isolated.
func TestKV_HostToContainer_StoreRoundtrip(t *testing.T) {
	store := newFakeRunStore()
	ctx := context.Background()

	const taskID = "task-rt-1"
	const attemptID = "att-rt-1"

	// Host side: set multiple keys.
	require.NoError(t, store.SetContextKey(ctx, taskID, attemptID, "prompt_file", "/workspace/prompt.txt"))
	require.NoError(t, store.SetContextKey(ctx, taskID, attemptID, "branch", "feature-x"))
	require.NoError(t, store.SetContextKey(ctx, taskID, attemptID, "workflow", "develop"))

	// Container side: retrieve same keys with same task/attempt IDs.
	for _, tc := range []struct {
		key  string
		want string
	}{
		{"prompt_file", "/workspace/prompt.txt"},
		{"branch", "feature-x"},
		{"workflow", "develop"},
	} {
		val, found, err := store.GetContextKey(ctx, taskID, attemptID, tc.key)
		require.NoError(t, err, "GetContextKey(%q)", tc.key)
		assert.True(t, found, "key %q should be found", tc.key)
		assert.Equal(t, tc.want, val, "key %q value mismatch", tc.key)
	}

	// Verify namespace isolation: different attempt cannot see these keys.
	_, found, err := store.GetContextKey(ctx, taskID, "different-attempt", "prompt_file")
	require.NoError(t, err)
	assert.False(t, found, "different attempt should not see the key")
}
