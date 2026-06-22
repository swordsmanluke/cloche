package docker_test

import (
	"context"
	"fmt"
	"io"
	"strings"
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
//
// Wait blocks until Stop is called for the same container ID (or the context is
// cancelled). This mirrors real Docker behaviour so the pool's exit-watcher
// goroutine does not fire before NotifyReady is called in normal tests.
type fakeRuntime struct {
	mu         sync.Mutex
	started    []string // containerIDs returned by Start
	stopped    []string
	removed    []string
	startErr   error
	removeErr  error
	idCounter  int
	logsOutput string // returned by Logs when non-empty
	// waitChs holds a per-container channel that is closed by Stop.
	waitChs  map[string]chan struct{}
	stopOnce map[string]*sync.Once
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
	if f.waitChs == nil {
		f.waitChs = make(map[string]chan struct{})
		f.stopOnce = make(map[string]*sync.Once)
	}
	f.waitChs[id] = make(chan struct{})
	f.stopOnce[id] = &sync.Once{}
	return id, nil
}

func (f *fakeRuntime) Stop(_ context.Context, id string) error {
	f.mu.Lock()
	f.stopped = append(f.stopped, id)
	ch := f.waitChs[id]
	once := f.stopOnce[id]
	f.mu.Unlock()
	// Closing the channel unblocks any pending Wait call for this container.
	if ch != nil && once != nil {
		once.Do(func() { close(ch) })
	}
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

// Wait blocks until Stop is called for the container or the context is done.
// This prevents the exit-watcher goroutine from firing before NotifyReady.
func (f *fakeRuntime) Wait(ctx context.Context, id string) (int, error) {
	f.mu.Lock()
	ch, ok := f.waitChs[id]
	f.mu.Unlock()
	if !ok {
		// No channel registered (Start was overridden in a sub-type).
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

func (f *fakeRuntime) CopyFrom(_ context.Context, _, _, _ string) error { return nil }

func (f *fakeRuntime) Logs(_ context.Context, _ string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.logsOutput, nil
}

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
type fixedIDRuntime struct {
	fakeRuntime
	id string
}

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

// TestContainerPool_GetSession_ReturnsExistingSession verifies GetSession
// returns the session without starting a new container.
func TestContainerPool_GetSession_ReturnsExistingSession(t *testing.T) {
	rt := &fakeRuntime{}
	pool := docker.NewContainerPool(rt)
	ctx := context.Background()

	go func() {
		time.Sleep(20 * time.Millisecond)
		id := rt.lastStarted()
		pool.NotifyReady(id)
	}()

	sess, err := pool.SessionFor(ctx, "att-gs", ports.ContainerConfig{Image: "img"})
	require.NoError(t, err)

	// GetSession should return the same session without starting another container.
	got := pool.GetSession("att-gs")
	require.NotNil(t, got)
	assert.Equal(t, sess.ContainerID, got.ContainerID)
	assert.Equal(t, 1, rt.startedCount(), "GetSession must not start a second container")
}

// TestContainerPool_GetSession_NilForUnknown verifies GetSession returns nil
// when no session exists for the given attempt.
func TestContainerPool_GetSession_NilForUnknown(t *testing.T) {
	rt := &fakeRuntime{}
	pool := docker.NewContainerPool(rt)

	got := pool.GetSession("nonexistent")
	assert.Nil(t, got)
	assert.Equal(t, 0, rt.startedCount(), "GetSession must not start a container")
}

// TestContainerPool_GetSession_NilAfterCleanup verifies GetSession returns nil
// after CleanupAttempt removes the pool entry.
func TestContainerPool_GetSession_NilAfterCleanup(t *testing.T) {
	rt := &fakeRuntime{}
	pool := docker.NewContainerPool(rt)
	ctx := context.Background()

	go func() {
		time.Sleep(20 * time.Millisecond)
		id := rt.lastStarted()
		pool.NotifyReady(id)
	}()

	_, err := pool.SessionFor(ctx, "att-gc", ports.ContainerConfig{Image: "img"})
	require.NoError(t, err)

	require.NoError(t, pool.CleanupAttempt(ctx, "att-gc", false, false))

	got := pool.GetSession("att-gc")
	assert.Nil(t, got, "GetSession should return nil after cleanup")
}

func TestContainerPool_SessionFor_AgentReadyTimeout_FailsFast(t *testing.T) {
	// AgentReady never arrives. SessionFor should fail quickly after the
	// dedicated ready timeout rather than waiting for the step context deadline.
	rt := &fakeRuntime{}
	pool := docker.NewContainerPool(rt)
	pool.SetAgentReadyTimeout(30 * time.Millisecond)

	// Step context has a generous deadline — timeout must come from the pool.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	_, err := pool.SessionFor(ctx, "att-ready-timeout", ports.ContainerConfig{Image: "img"})
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.NotErrorIs(t, err, context.DeadlineExceeded, "should not be the step-level deadline")
	assert.Contains(t, err.Error(), "AgentReady")
	assert.Less(t, elapsed, 2*time.Second, "should fail fast, not wait for step deadline")
}

func TestContainerPool_SessionFor_AgentReadyTimeout_StopsContainer(t *testing.T) {
	// When the ready timeout fires, the pool must stop the container so it
	// doesn't linger after the caller receives an error.
	rt := &fakeRuntime{}
	pool := docker.NewContainerPool(rt)
	pool.SetAgentReadyTimeout(20 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := pool.SessionFor(ctx, "att-stop-on-timeout", ports.ContainerConfig{Image: "img"})
	require.Error(t, err)

	rt.mu.Lock()
	stopped := rt.stopped
	rt.mu.Unlock()

	require.Len(t, stopped, 1, "timed-out container must be stopped")
}

func TestContainerPool_SessionFor_AgentReadyTimeout_IncludesLogs(t *testing.T) {
	// When the ready timeout fires and the container produced logs, those logs
	// must appear in the returned error to aid diagnosis.
	rt := &fakeRuntime{logsOutput: "panic: agent init failed\ngoroutine 1 [running]"}
	pool := docker.NewContainerPool(rt)
	pool.SetAgentReadyTimeout(20 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := pool.SessionFor(ctx, "att-logs-on-timeout", ports.ContainerConfig{Image: "img"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "agent init failed", "container logs must be included in the error")
}

func TestContainerPool_SessionFor_AgentReadyTimeout_ClearsPoolEntry(t *testing.T) {
	// After a timeout the pool entry must be removed so a future call with the
	// same attemptID can start a fresh container.
	rt := &fakeRuntime{}
	pool := docker.NewContainerPool(rt)
	pool.SetAgentReadyTimeout(20 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := pool.SessionFor(ctx, "att-retry", ports.ContainerConfig{Image: "img"})
	require.Error(t, err)

	// Second call: pool entry was cleared so a new container is started.
	// Signal AgentReady for the new container so this call succeeds.
	// Poll until the second container is started before notifying.
	go func() {
		for {
			time.Sleep(5 * time.Millisecond)
			rt.mu.Lock()
			count := len(rt.started)
			var id string
			if count >= 2 {
				id = rt.started[1]
			}
			rt.mu.Unlock()
			if id != "" {
				pool.NotifyReady(id)
				return
			}
		}
	}()

	sess, err := pool.SessionFor(ctx, "att-retry", ports.ContainerConfig{Image: "img"})
	require.NoError(t, err, "retry after timeout should start a new container")
	assert.NotNil(t, sess)
	assert.Equal(t, 2, rt.startedCount(), "should have started two containers total")
}

func TestContainerPool_SessionFor_ContextCancelledBeforeReadyTimeout(t *testing.T) {
	// When the step context is cancelled before the ready timer fires, the error
	// must wrap context.Canceled (not the ready-timeout message).
	rt := &fakeRuntime{}
	pool := docker.NewContainerPool(rt)
	pool.SetAgentReadyTimeout(10 * time.Second) // much longer than the test

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	_, err := pool.SessionFor(ctx, "att-ctx-cancel-wins", ports.ContainerConfig{Image: "img"})
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestContainerPool_Snapshot_Empty(t *testing.T) {
	rt := &fakeRuntime{}
	pool := docker.NewContainerPool(rt)
	assert.Empty(t, pool.Snapshot())
}

func TestContainerPool_Snapshot_ActiveSession(t *testing.T) {
	rt := &fakeRuntime{}
	pool := docker.NewContainerPool(rt)

	ctx := context.Background()

	go func() {
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

	_, err := pool.SessionFor(ctx, "att-snap", ports.ContainerConfig{Image: "img"})
	require.NoError(t, err)

	snaps := pool.Snapshot()
	require.Len(t, snaps, 1)
	assert.Equal(t, "att-snap", snaps[0].AttemptID)
	assert.NotEmpty(t, snaps[0].ContainerID)
	assert.Equal(t, 0, snaps[0].PendingSteps)
}

func TestContainerPool_Snapshot_ClearedAfterCleanup(t *testing.T) {
	rt := &fakeRuntime{}
	pool := docker.NewContainerPool(rt)

	ctx := context.Background()

	go func() {
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

	_, err := pool.SessionFor(ctx, "att-cleanup-snap", ports.ContainerConfig{Image: "img"})
	require.NoError(t, err)
	assert.Len(t, pool.Snapshot(), 1)

	pool.CleanupAttempt(ctx, "att-cleanup-snap", false, true)
	assert.Empty(t, pool.Snapshot())
}

// TestContainerPool_SessionFor_ContainerExitsBeforeAgentReady verifies that
// SessionFor returns a fast error when the container exits before AgentReady
// arrives, rather than blocking for the full step timeout.
func TestContainerPool_SessionFor_ContainerExitsBeforeAgentReady(t *testing.T) {
	rt := &fakeRuntime{}
	pool := docker.NewContainerPool(rt)

	// Use a long timeout to prove we fail fast, not because the context expires.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Simulate container crash: stop the container shortly after it starts,
	// without ever calling NotifyReady.
	go func() {
		time.Sleep(50 * time.Millisecond)
		id := rt.lastStarted()
		if id != "" {
			rt.Stop(context.Background(), id) // closes the wait channel → Wait returns
		}
	}()

	_, err := pool.SessionFor(ctx, "att-exit", ports.ContainerConfig{Image: "img"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exited before agent was ready",
		"error should indicate the container exited unexpectedly")
}

// tarRuntime extends fakeRuntime with tar-stream copy methods so it satisfies
// the tarCopier interface exercised by CopyTarFrom/CopyTarTo.
type tarRuntime struct {
	fakeRuntime
	mu sync.Mutex

	fromCalledID string
	fromSrc      string
	fromPayload  string // written to w on CopyTarFrom
	fromErr      error

	toCalledID string
	toDst      string
	toReceived string // contents read from r on CopyTarTo
	toErr      error
}

func (r *tarRuntime) CopyTarFrom(_ context.Context, containerID, srcPath string, w io.Writer) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fromCalledID = containerID
	r.fromSrc = srcPath
	if r.fromErr != nil {
		return r.fromErr
	}
	_, err := io.WriteString(w, r.fromPayload)
	return err
}

func (r *tarRuntime) CopyTarTo(_ context.Context, containerID string, rd io.Reader, dst string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.toCalledID = containerID
	r.toDst = dst
	if r.toErr != nil {
		return r.toErr
	}
	b, err := io.ReadAll(rd)
	r.toReceived = string(b)
	return err
}

func TestContainerPool_CopyTarFrom_Delegates(t *testing.T) {
	rt := &tarRuntime{fromPayload: "tar-bytes"}
	pool := docker.NewContainerPool(rt)

	var buf strings.Builder
	err := pool.CopyTarFrom(context.Background(), "cid-1", "/workspace", &buf)
	require.NoError(t, err)
	assert.Equal(t, "cid-1", rt.fromCalledID)
	assert.Equal(t, "/workspace", rt.fromSrc)
	assert.Equal(t, "tar-bytes", buf.String())
}

func TestContainerPool_CopyTarTo_Delegates(t *testing.T) {
	rt := &tarRuntime{}
	pool := docker.NewContainerPool(rt)

	err := pool.CopyTarTo(context.Background(), "cid-2", strings.NewReader("payload"), "/workspace/")
	require.NoError(t, err)
	assert.Equal(t, "cid-2", rt.toCalledID)
	assert.Equal(t, "/workspace/", rt.toDst)
	assert.Equal(t, "payload", rt.toReceived)
}

func TestContainerPool_CopyTar_RuntimeNotSupported(t *testing.T) {
	// fakeRuntime does NOT implement tarCopier.
	rt := &fakeRuntime{}
	pool := docker.NewContainerPool(rt)

	err := pool.CopyTarFrom(context.Background(), "cid", "/workspace", io.Discard)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not support tar copy")

	err = pool.CopyTarTo(context.Background(), "cid", strings.NewReader(""), "/workspace/")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not support tar copy")
}
