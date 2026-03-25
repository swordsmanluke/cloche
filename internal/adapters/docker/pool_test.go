package docker_test

import (
	"context"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	pb "github.com/cloche-dev/cloche/api/clochepb"
	"github.com/cloche-dev/cloche/internal/adapters/docker"
	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/ports"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeRuntime is a minimal ContainerRuntime stub for pool tests.
type fakeRuntime struct {
	mu        sync.Mutex
	started   []string // containerIDs returned by Start
	stopped   []string
	removed   []string
	startErr  error
	removeErr error
	idCounter int
}

func (f *fakeRuntime) Start(_ context.Context, _ ports.ContainerConfig) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.startErr != nil {
		return "", f.startErr
	}
	f.idCounter++
	id := fmt.Sprintf("container-%d", f.idCounter)
	f.started = append(f.started, id)
	return id, nil
}

func (f *fakeRuntime) Stop(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = append(f.stopped, id)
	return nil
}

func (f *fakeRuntime) Remove(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, id)
	return f.removeErr
}

func (f *fakeRuntime) AttachOutput(_ context.Context, _ string) (io.ReadCloser, error) {
	return io.NopCloser(nil), nil
}

func (f *fakeRuntime) Wait(_ context.Context, _ string) (int, error) { return 0, nil }

func (f *fakeRuntime) CopyFrom(_ context.Context, _, _, _ string) error { return nil }

func (f *fakeRuntime) Logs(_ context.Context, _ string) (string, error) { return "", nil }

func (f *fakeRuntime) Inspect(_ context.Context, _ string) (*ports.ContainerStatus, error) {
	return &ports.ContainerStatus{Running: true}, nil
}

func (f *fakeRuntime) Attach(_ context.Context, _ string) (io.ReadWriteCloser, error) {
	return nil, nil
}

func (f *fakeRuntime) startedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.started)
}

func (f *fakeRuntime) lastStarted() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.started) == 0 {
		return ""
	}
	return f.started[len(f.started)-1]
}

// notifyAfter sends NotifyReady on the pool after the given delay.
func notifyAfter(p *docker.ContainerPool, containerID string, delay time.Duration) {
	go func() {
		time.Sleep(delay)
		p.NotifyReady(containerID)
	}()
}

func TestContainerPool_SessionFor_CreatesContainer(t *testing.T) {
	rt := &fakeRuntime{}
	pool := docker.NewContainerPool(rt)

	ctx := context.Background()
	cfg := ports.ContainerConfig{Image: "test-image", AttemptID: "att1"}

	// Notify ready shortly after SessionFor starts.
	go func() {
		// Wait briefly to let SessionFor register the container.
		time.Sleep(20 * time.Millisecond)
		rt.mu.Lock()
		id := ""
		if len(rt.started) > 0 {
			id = rt.started[0]
		}
		rt.mu.Unlock()
		if id != "" {
			pool.NotifyReady(id)
		}
	}()

	sess, err := pool.SessionFor(ctx, "att1", cfg)
	require.NoError(t, err)
	assert.NotNil(t, sess)
	assert.NotEmpty(t, sess.ContainerID)
	assert.Equal(t, 1, rt.startedCount())
}

func TestContainerPool_SessionFor_ReusesExistingSession(t *testing.T) {
	rt := &fakeRuntime{}
	pool := docker.NewContainerPool(rt)

	ctx := context.Background()
	cfg := ports.ContainerConfig{Image: "test-image", AttemptID: "att2"}

	// First call: start container then notify ready.
	go func() {
		time.Sleep(20 * time.Millisecond)
		id := rt.lastStarted()
		if id != "" {
			pool.NotifyReady(id)
		}
	}()

	sess1, err := pool.SessionFor(ctx, "att2", cfg)
	require.NoError(t, err)

	// Second call: should return same session without starting another container.
	sess2, err := pool.SessionFor(ctx, "att2", cfg)
	require.NoError(t, err)

	assert.Equal(t, sess1.ContainerID, sess2.ContainerID, "should reuse existing session")
	assert.Equal(t, 1, rt.startedCount(), "should only start one container")
}

func TestContainerPool_SessionFor_DifferentAttempts(t *testing.T) {
	rt := &fakeRuntime{}
	pool := docker.NewContainerPool(rt)

	ctx := context.Background()

	var wg sync.WaitGroup
	results := make([]*docker.ContainerSession, 2)
	errs := make([]error, 2)

	for i, attemptID := range []string{"attA", "attB"} {
		i, attemptID := i, attemptID
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Notify ready after a short delay for each attempt.
			go func() {
				time.Sleep(20 * time.Millisecond)
				// Find the container for this attempt.
				// We'll notify all started containers to unblock.
				rt.mu.Lock()
				var ids []string
				ids = append(ids, rt.started...)
				rt.mu.Unlock()
				for _, id := range ids {
					pool.NotifyReady(id)
				}
			}()
			results[i], errs[i] = pool.SessionFor(ctx, attemptID, ports.ContainerConfig{
				Image:     "test-image",
				AttemptID: attemptID,
			})
		}()
	}

	wg.Wait()

	require.NoError(t, errs[0])
	require.NoError(t, errs[1])
	assert.NotEqual(t, results[0].ContainerID, results[1].ContainerID,
		"different attempts should get different containers")
	assert.Equal(t, 2, rt.startedCount())
}

func TestContainerPool_SessionFor_ContextCancelled(t *testing.T) {
	rt := &fakeRuntime{}
	pool := docker.NewContainerPool(rt)

	ctx, cancel := context.WithCancel(context.Background())

	// Cancel context before NotifyReady is called.
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	_, err := pool.SessionFor(ctx, "att-cancel", ports.ContainerConfig{Image: "test-image"})
	assert.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestContainerPool_SessionFor_StartError(t *testing.T) {
	rt := &fakeRuntime{startErr: fmt.Errorf("docker unavailable")}
	pool := docker.NewContainerPool(rt)

	_, err := pool.SessionFor(context.Background(), "att-err", ports.ContainerConfig{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "docker unavailable")
}

func TestContainerPool_CleanupAttempt_RemovesContainers(t *testing.T) {
	rt := &fakeRuntime{}
	pool := docker.NewContainerPool(rt)

	ctx := context.Background()

	// Create a session for the attempt.
	go func() {
		time.Sleep(20 * time.Millisecond)
		pool.NotifyReady(rt.lastStarted())
	}()
	_, err := pool.SessionFor(ctx, "att-cleanup", ports.ContainerConfig{Image: "img"})
	require.NoError(t, err)

	// Cleanup with no keep flags → container should be stopped and removed.
	err = pool.CleanupAttempt(ctx, "att-cleanup", false, false, false)
	require.NoError(t, err)

	assert.Len(t, rt.stopped, 1)
	assert.Len(t, rt.removed, 1)
}

func TestContainerPool_CleanupAttempt_KeepOnFailure(t *testing.T) {
	rt := &fakeRuntime{}
	pool := docker.NewContainerPool(rt)

	ctx := context.Background()

	go func() {
		time.Sleep(20 * time.Millisecond)
		pool.NotifyReady(rt.lastStarted())
	}()
	_, err := pool.SessionFor(ctx, "att-fail", ports.ContainerConfig{Image: "img"})
	require.NoError(t, err)

	// runFailed = true → keep container.
	err = pool.CleanupAttempt(ctx, "att-fail", false, true, false)
	require.NoError(t, err)

	assert.Empty(t, rt.removed, "container should be kept on failure")
	assert.Empty(t, rt.stopped, "container should not be stopped on failure")
}

func TestContainerPool_CleanupAttempt_KeepOnKeepContainerFlag(t *testing.T) {
	rt := &fakeRuntime{}
	pool := docker.NewContainerPool(rt)

	ctx := context.Background()

	go func() {
		time.Sleep(20 * time.Millisecond)
		pool.NotifyReady(rt.lastStarted())
	}()
	_, err := pool.SessionFor(ctx, "att-keep", ports.ContainerConfig{Image: "img"})
	require.NoError(t, err)

	err = pool.CleanupAttempt(ctx, "att-keep", true, false, false)
	require.NoError(t, err)

	assert.Empty(t, rt.removed, "container should be kept with --keep-container")
}

func TestContainerPool_CleanupAttempt_KeepOnAbort(t *testing.T) {
	rt := &fakeRuntime{}
	pool := docker.NewContainerPool(rt)

	ctx := context.Background()

	go func() {
		time.Sleep(20 * time.Millisecond)
		pool.NotifyReady(rt.lastStarted())
	}()
	_, err := pool.SessionFor(ctx, "att-abort", ports.ContainerConfig{Image: "img"})
	require.NoError(t, err)

	err = pool.CleanupAttempt(ctx, "att-abort", false, false, true)
	require.NoError(t, err)

	assert.Empty(t, rt.removed, "container should be kept on abort")
}

func TestContainerPool_CleanupAttempt_UnknownAttemptIsNoop(t *testing.T) {
	rt := &fakeRuntime{}
	pool := docker.NewContainerPool(rt)

	// Cleaning up an attempt that was never registered should not error.
	err := pool.CleanupAttempt(context.Background(), "unknown", false, false, false)
	assert.NoError(t, err)
}

func TestContainerPool_NotifyReady_UnknownContainerIsNoop(t *testing.T) {
	rt := &fakeRuntime{}
	pool := docker.NewContainerPool(rt)

	// Should not panic.
	pool.NotifyReady("nonexistent-container")
}

func TestContainerPool_NotifyReady_CalledTwiceIsNoop(t *testing.T) {
	rt := &fakeRuntime{}
	pool := docker.NewContainerPool(rt)

	ctx := context.Background()

	go func() {
		time.Sleep(20 * time.Millisecond)
		id := rt.lastStarted()
		pool.NotifyReady(id)
		// Second call must not panic (double-close of channel).
		pool.NotifyReady(id)
	}()

	_, err := pool.SessionFor(ctx, "att-double", ports.ContainerConfig{Image: "img"})
	require.NoError(t, err)
}

func TestContainerPool_FailPendingRequests_UnblocksCallers(t *testing.T) {
	rt := &fakeRuntime{}
	pool := docker.NewContainerPool(rt)
	ctx := context.Background()

	// Get a session and register a send function so ExecuteStep can dispatch.
	go func() {
		time.Sleep(20 * time.Millisecond)
		id := rt.lastStarted()
		pool.NotifyReadyWithStream(id, func(_ *pb.DaemonMessage) error { return nil })
	}()

	sess, err := pool.SessionFor(ctx, "att-fail", ports.ContainerConfig{Image: "img"})
	require.NoError(t, err)
	containerID := sess.ContainerID

	// Start an ExecuteStep in a goroutine — it will block waiting for a result.
	done := make(chan error, 1)
	go func() {
		_, execErr := sess.ExecuteStep(context.Background(), &domain.Step{Name: "s1", Type: domain.StepTypeScript})
		done <- execErr
	}()

	// Give the goroutine a moment to register its pending channel.
	time.Sleep(20 * time.Millisecond)

	// FailPendingRequests should unblock the ExecuteStep call.
	pool.FailPendingRequests(containerID)

	select {
	case execErr := <-done:
		assert.NoError(t, execErr, "ExecuteStep should return without error (synthetic fail result)")
	case <-time.After(2 * time.Second):
		t.Fatal("ExecuteStep did not unblock after FailPendingRequests")
	}
}

func TestContainerPool_FailPendingRequests_UnknownContainerIsNoop(t *testing.T) {
	rt := &fakeRuntime{}
	pool := docker.NewContainerPool(rt)
	// Should not panic.
	pool.FailPendingRequests("nonexistent-container")
}
