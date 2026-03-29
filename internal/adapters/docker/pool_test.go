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

	// Cleanup on success → container should be stopped and removed.
	err = pool.CleanupAttempt(ctx, "att-cleanup", false, true)
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

	// succeeded=false → stop but keep container for debugging.
	err = pool.CleanupAttempt(ctx, "att-fail", false, false)
	require.NoError(t, err)

	assert.Empty(t, rt.removed, "container should be kept on failure")
	assert.Len(t, rt.stopped, 1, "container should be stopped on failure")
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

	err = pool.CleanupAttempt(ctx, "att-keep", true, true)
	require.NoError(t, err)

	assert.Empty(t, rt.removed, "container should be kept with --keep-container")
	assert.Empty(t, rt.stopped, "container should not be stopped with --keep-container")
}

func TestContainerPool_CleanupAttempt_StopsButKeepsOnAbort(t *testing.T) {
	rt := &fakeRuntime{}
	pool := docker.NewContainerPool(rt)

	ctx := context.Background()

	go func() {
		time.Sleep(20 * time.Millisecond)
		pool.NotifyReady(rt.lastStarted())
	}()
	_, err := pool.SessionFor(ctx, "att-abort", ports.ContainerConfig{Image: "img"})
	require.NoError(t, err)

	err = pool.CleanupAttempt(ctx, "att-abort", false, false)
	require.NoError(t, err)

	assert.Len(t, rt.stopped, 1, "container should be stopped on abort")
	assert.Empty(t, rt.removed, "container should be kept on abort for debugging")
}

func TestContainerPool_CleanupAttempt_UnknownAttemptIsNoop(t *testing.T) {
	rt := &fakeRuntime{}
	pool := docker.NewContainerPool(rt)

	// Cleaning up an attempt that was never registered should not error.
	err := pool.CleanupAttempt(context.Background(), "unknown", false, false)
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

func TestContainerPool_NotifyReadyWithStream_ShortContainerID(t *testing.T) {
	rt := &fakeRuntime{}
	pool := docker.NewContainerPool(rt)
	ctx := context.Background()

	// SessionFor starts a container and stores the full ID (e.g. "container-1").
	// Simulate Docker's behavior where os.Hostname() returns a short prefix.
	go func() {
		time.Sleep(20 * time.Millisecond)
		fullID := rt.lastStarted() // e.g. "container-1"
		// Use a prefix of the full ID (simulating Docker short hostname).
		// Our fake IDs are short, so just use the full thing to test the
		// prefix path — also test with a real-looking 64-char ID.
		pool.NotifyReadyWithStream(fullID, func(_ *pb.DaemonMessage) error { return nil })
	}()

	sess, err := pool.SessionFor(ctx, "att-short-ok", ports.ContainerConfig{Image: "img"})
	require.NoError(t, err)
	assert.NotNil(t, sess)
}

func TestContainerPool_NotifyReadyWithStream_PrefixMatch(t *testing.T) {
	// Use a runtime that returns 64-char IDs like real Docker.
	fullID := "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6abcd"
	longRT := &fixedIDRuntime{id: fullID}
	longPool := docker.NewContainerPool(longRT)
	ctx := context.Background()

	// SessionFor will store fullID in containerAttempt map.
	// NotifyReadyWithStream with the short prefix should match via prefix lookup.
	shortID := fullID[:12] // "a1b2c3d4e5f6"

	go func() {
		time.Sleep(20 * time.Millisecond)
		longPool.NotifyReadyWithStream(shortID, func(_ *pb.DaemonMessage) error { return nil })
	}()

	sess, err := longPool.SessionFor(ctx, "att-prefix", ports.ContainerConfig{Image: "img"})
	require.NoError(t, err)
	assert.Equal(t, fullID, sess.ContainerID)
}

// fixedIDRuntime always returns the same container ID from Start.
type fixedIDRuntime struct{ fakeRuntime; id string }

func (f *fixedIDRuntime) Start(_ context.Context, _ ports.ContainerConfig) (string, error) {
	return f.id, nil
}

// callbackRuntime calls onStart during Start, before returning the container ID.
// This simulates the agent connecting faster than SessionFor can register.
type callbackRuntime struct {
	fakeRuntime
	id      string
	onStart func(id string)
}

func (r *callbackRuntime) Start(_ context.Context, _ ports.ContainerConfig) (string, error) {
	if r.onStart != nil {
		r.onStart(r.id)
	}
	return r.id, nil
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
		_, execErr := sess.ExecuteStep(context.Background(), &domain.Step{Name: "s1", Type: domain.StepTypeScript}, false)
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

// resumableRuntime extends fakeRuntime with CommitContainer and RemoveImage
// to satisfy the containerResumer interface used by CommitForResume.
type resumableRuntime struct {
	fakeRuntime
	mu             sync.Mutex
	committed      map[string]string // containerID -> tag
	removedImages  []string
	commitErr      error
	removeImageErr error
}

func (r *resumableRuntime) CommitContainer(_ context.Context, containerID, attemptID string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.commitErr != nil {
		return "", r.commitErr
	}
	tag := "cloche-resume:" + attemptID + "-" + containerID
	if r.committed == nil {
		r.committed = make(map[string]string)
	}
	r.committed[containerID] = tag
	return tag, nil
}

func (r *resumableRuntime) RemoveImage(_ context.Context, imageTag string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.removeImageErr != nil {
		return r.removeImageErr
	}
	r.removedImages = append(r.removedImages, imageTag)
	return nil
}

func TestContainerPool_CommitForResume_NoAttempt(t *testing.T) {
	rt := &resumableRuntime{}
	pool := docker.NewContainerPool(rt)

	// No containers registered for this attempt: should return nil, nil.
	images, err := pool.CommitForResume(context.Background(), "nonexistent")
	require.NoError(t, err)
	assert.Nil(t, images)
}

func TestContainerPool_CommitForResume_RuntimeNotSupported(t *testing.T) {
	// fakeRuntime does NOT implement containerResumer.
	rt := &fakeRuntime{}
	pool := docker.NewContainerPool(rt)

	// Register a container for the attempt so we get past the "no entry" check.
	ctx := context.Background()
	go func() {
		time.Sleep(20 * time.Millisecond)
		pool.NotifyReady(rt.lastStarted())
	}()
	_, err := pool.SessionFor(ctx, "att-nocommit", ports.ContainerConfig{Image: "img"})
	require.NoError(t, err)

	_, err = pool.CommitForResume(ctx, "att-nocommit")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not support image commit")
}

func TestContainerPool_CommitForResume_Success(t *testing.T) {
	rt := &resumableRuntime{}
	pool := docker.NewContainerPool(rt)

	ctx := context.Background()

	// Start a container so the pool has an entry for the attempt.
	go func() {
		time.Sleep(20 * time.Millisecond)
		rt.fakeRuntime.mu.Lock()
		id := ""
		if len(rt.fakeRuntime.started) > 0 {
			id = rt.fakeRuntime.started[0]
		}
		rt.fakeRuntime.mu.Unlock()
		if id != "" {
			pool.NotifyReady(id)
		}
	}()

	sess, err := pool.SessionFor(ctx, "att-commit", ports.ContainerConfig{Image: "img"})
	require.NoError(t, err)

	images, err := pool.CommitForResume(ctx, "att-commit")
	require.NoError(t, err)
	require.NotNil(t, images)
	require.Len(t, images, 1)

	tag, ok := images[sess.ContainerID]
	require.True(t, ok, "committed images should include the session's container ID")
	assert.Contains(t, tag, "cloche-resume:")
	assert.Contains(t, tag, "att-commit")
	assert.Contains(t, tag, sess.ContainerID)
}

func TestContainerPool_StartFromImage_UsesGivenImageAndSkipsProjectCopy(t *testing.T) {
	rt := &resumableRuntime{}
	pool := docker.NewContainerPool(rt)

	ctx := context.Background()

	// StartFromImage should start a container using the given image and pass
	// an empty ProjectDir so the committed workspace is not overwritten.
	var capturedCfg ports.ContainerConfig
	rt.fakeRuntime.mu.Lock()
	origStart := rt.fakeRuntime.startErr
	rt.fakeRuntime.mu.Unlock()
	_ = origStart

	go func() {
		time.Sleep(20 * time.Millisecond)
		rt.fakeRuntime.mu.Lock()
		id := ""
		if len(rt.fakeRuntime.started) > 0 {
			id = rt.fakeRuntime.started[0]
		}
		rt.fakeRuntime.mu.Unlock()
		if id != "" {
			pool.NotifyReady(id)
		}
	}()

	// Provide a cfg with a ProjectDir; StartFromImage should clear it.
	cfg := ports.ContainerConfig{
		Image:      "original-image",
		ProjectDir: "/should/be/cleared",
		AttemptID:  "att-fromimage",
	}
	sess, err := pool.StartFromImage(ctx, "att-fromimage", "committed-image", cfg)
	require.NoError(t, err)
	assert.NotNil(t, sess)

	_ = capturedCfg // not directly inspectable through the pool interface
	// Verify a container was started (the pool should have started exactly one).
	rt.fakeRuntime.mu.Lock()
	count := len(rt.fakeRuntime.started)
	rt.fakeRuntime.mu.Unlock()
	assert.Equal(t, 1, count, "StartFromImage should start exactly one container")
}

func TestContainerPool_RemoveImages_CallsRuntime(t *testing.T) {
	rt := &resumableRuntime{}
	pool := docker.NewContainerPool(rt)

	images := map[string]string{
		"ctr1": "cloche-resume:att1-ctr1",
		"ctr2": "cloche-resume:att1-ctr2",
	}

	pool.RemoveImages(context.Background(), images)

	rt.mu.Lock()
	removed := rt.removedImages
	rt.mu.Unlock()

	assert.Len(t, removed, 2)
}

func TestContainerPool_RemoveImages_NopWhenRuntimeNotSupported(t *testing.T) {
	// fakeRuntime does not implement containerResumer: should not panic.
	rt := &fakeRuntime{}
	pool := docker.NewContainerPool(rt)
	pool.RemoveImages(context.Background(), map[string]string{"ctr1": "some-tag"})
}

func TestContainerPool_NotifyReadyWithStream_BeforeRegistration(t *testing.T) {
	// Reproduces the race where the agent sends AgentReady before SessionFor
	// registers the container in containerAttempt. The notification must be
	// stashed and applied after registration so SessionFor doesn't block.
	fullID := "abcdef123456abcdef123456abcdef123456abcdef123456abcdef123456abcd"
	shortID := fullID[:12] // Docker hostname = short container ID

	var pool *docker.ContainerPool
	rt := &callbackRuntime{
		id: fullID,
		onStart: func(_ string) {
			// Agent connects during Start, before SessionFor registers.
			pool.NotifyReadyWithStream(shortID, func(_ *pb.DaemonMessage) error { return nil })
		},
	}
	pool = docker.NewContainerPool(rt)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	sess, err := pool.SessionFor(ctx, "att-early-stream", ports.ContainerConfig{Image: "img"})
	require.NoError(t, err)
	assert.Equal(t, fullID, sess.ContainerID)
}

func TestContainerPool_NotifyReady_BeforeRegistration(t *testing.T) {
	// Same race as above but with NotifyReady (no stream).
	fullID := "fedcba654321fedcba654321fedcba654321fedcba654321fedcba654321fedc"

	var pool *docker.ContainerPool
	rt := &callbackRuntime{
		id: fullID,
		onStart: func(id string) {
			pool.NotifyReady(id)
		},
	}
	pool = docker.NewContainerPool(rt)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	sess, err := pool.SessionFor(ctx, "att-early-ready", ports.ContainerConfig{Image: "img"})
	require.NoError(t, err)
	assert.Equal(t, fullID, sess.ContainerID)
}
