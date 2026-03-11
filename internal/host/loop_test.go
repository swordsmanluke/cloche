package host

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/stretchr/testify/assert"
)

func TestLoop_StartStop(t *testing.T) {
	store := &fakeStore{runs: map[string]*domain.Run{}}
	var called atomic.Int32

	runFn := func(ctx context.Context, projectDir string, taskID string) (*RunResult, error) {
		called.Add(1)
		// Simulate a run that finds no work (failed = aborted).
		time.Sleep(50 * time.Millisecond)
		return &RunResult{State: domain.RunStateFailed}, nil
	}

	loop := NewLoop(LoopConfig{
		ProjectDir:    "/tmp/test-project",
		MaxConcurrent: 1,
	}, store, runFn)

	if loop.Running() {
		t.Fatal("loop should not be running before Start")
	}

	loop.Start()
	if !loop.Running() {
		t.Fatal("loop should be running after Start")
	}

	// Wait enough time for at least one run to happen.
	time.Sleep(200 * time.Millisecond)
	loop.Stop()

	if loop.Running() {
		t.Fatal("loop should not be running after Stop")
	}

	if called.Load() < 1 {
		t.Fatal("expected at least one run to be launched")
	}
}

func TestLoop_RampsUpOnSuccess(t *testing.T) {
	store := &fakeStore{runs: map[string]*domain.Run{}}
	var called atomic.Int32

	runFn := func(ctx context.Context, projectDir string, taskID string) (*RunResult, error) {
		n := called.Add(1)
		time.Sleep(20 * time.Millisecond)
		// First 3 succeed, then fail (no more work).
		if n <= 3 {
			return &RunResult{State: domain.RunStateSucceeded}, nil
		}
		return &RunResult{State: domain.RunStateFailed}, nil
	}

	loop := NewLoop(LoopConfig{
		ProjectDir:    "/tmp/test-project",
		MaxConcurrent: 1,
	}, store, runFn)

	loop.Start()
	// With no backoff between successes, 3 successful + 1 failed should happen fast.
	time.Sleep(300 * time.Millisecond)
	loop.Stop()

	if called.Load() < 4 {
		t.Fatalf("expected at least 4 runs, got %d", called.Load())
	}
}

func TestLoop_DoubleStartIsNoop(t *testing.T) {
	store := &fakeStore{runs: map[string]*domain.Run{}}
	runFn := func(ctx context.Context, projectDir string, taskID string) (*RunResult, error) {
		time.Sleep(100 * time.Millisecond)
		return &RunResult{State: domain.RunStateFailed}, nil
	}

	loop := NewLoop(LoopConfig{
		ProjectDir:    "/tmp/test-project",
		MaxConcurrent: 1,
	}, store, runFn)

	loop.Start()
	loop.Start() // Should be a no-op.
	loop.Stop()
}

// fakeTaskAssigner implements TaskAssigner for testing.
type fakeTaskAssigner struct {
	mu    sync.Mutex
	tasks []Task
	calls int
}

func (f *fakeTaskAssigner) ListTasks(_ context.Context, _ string) ([]Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	result := make([]Task, len(f.tasks))
	copy(result, f.tasks)
	return result, nil
}

func TestLoop_TaskAssigner_PassesTaskID(t *testing.T) {
	store := &fakeStore{runs: map[string]*domain.Run{}}

	assigner := &fakeTaskAssigner{
		tasks: []Task{
			{ID: "task-1", Title: "Fix bug"},
			{ID: "task-2", Title: "Add feature"},
		},
	}

	var receivedTaskIDs []string
	var mu sync.Mutex

	runFn := func(ctx context.Context, projectDir string, taskID string) (*RunResult, error) {
		mu.Lock()
		receivedTaskIDs = append(receivedTaskIDs, taskID)
		mu.Unlock()
		time.Sleep(20 * time.Millisecond)
		return &RunResult{State: domain.RunStateSucceeded}, nil
	}

	loop := NewLoop(LoopConfig{
		ProjectDir:    "/tmp/test-project",
		MaxConcurrent: 1,
		DedupTimeout:  1 * time.Second,
	}, store, runFn)
	loop.SetTaskAssigner(assigner)

	loop.Start()
	time.Sleep(200 * time.Millisecond)
	loop.Stop()

	mu.Lock()
	defer mu.Unlock()
	// Should have received task-1 first, then task-2 (task-1 is deduped).
	assert.GreaterOrEqual(t, len(receivedTaskIDs), 2)
	assert.Equal(t, "task-1", receivedTaskIDs[0])
	assert.Equal(t, "task-2", receivedTaskIDs[1])
}

func TestLoop_TaskAssigner_DedupPreventsReassignment(t *testing.T) {
	store := &fakeStore{runs: map[string]*domain.Run{}}

	assigner := &fakeTaskAssigner{
		tasks: []Task{{ID: "task-1"}},
	}

	var receivedTaskIDs []string
	var mu sync.Mutex

	runFn := func(ctx context.Context, projectDir string, taskID string) (*RunResult, error) {
		mu.Lock()
		receivedTaskIDs = append(receivedTaskIDs, taskID)
		mu.Unlock()
		time.Sleep(20 * time.Millisecond)
		return &RunResult{State: domain.RunStateSucceeded}, nil
	}

	loop := NewLoop(LoopConfig{
		ProjectDir:    "/tmp/test-project",
		MaxConcurrent: 1,
		DedupTimeout:  2 * time.Second, // long enough that task-1 won't be reassigned during test
	}, store, runFn)
	loop.SetTaskAssigner(assigner)

	loop.Start()
	time.Sleep(200 * time.Millisecond)
	loop.Stop()

	mu.Lock()
	defer mu.Unlock()
	// Only one run should have been launched since task-1 is deduped after first assignment.
	assert.Equal(t, 1, len(receivedTaskIDs))
	assert.Equal(t, "task-1", receivedTaskIDs[0])
}

func TestLoop_TaskAssigner_DedupExpires(t *testing.T) {
	store := &fakeStore{runs: map[string]*domain.Run{}}

	assigner := &fakeTaskAssigner{
		tasks: []Task{{ID: "task-1"}},
	}

	var receivedTaskIDs []string
	var mu sync.Mutex

	runFn := func(ctx context.Context, projectDir string, taskID string) (*RunResult, error) {
		mu.Lock()
		receivedTaskIDs = append(receivedTaskIDs, taskID)
		mu.Unlock()
		// Run takes longer than dedup timeout, so by the time the run
		// completes the dedup window has already expired and pickTask
		// can re-assign immediately without hitting capacityPollInterval.
		time.Sleep(100 * time.Millisecond)
		return &RunResult{State: domain.RunStateSucceeded}, nil
	}

	loop := NewLoop(LoopConfig{
		ProjectDir:    "/tmp/test-project",
		MaxConcurrent: 1,
		DedupTimeout:  50 * time.Millisecond,
	}, store, runFn)
	loop.SetTaskAssigner(assigner)

	loop.Start()
	time.Sleep(500 * time.Millisecond)
	loop.Stop()

	mu.Lock()
	defer mu.Unlock()
	// With 50ms dedup and 100ms run time, dedup expires before run completes,
	// so task-1 should be assigned multiple times.
	assert.GreaterOrEqual(t, len(receivedTaskIDs), 2, "dedup should have expired, allowing reassignment")
	for _, id := range receivedTaskIDs {
		assert.Equal(t, "task-1", id)
	}
}

func TestLoop_TaskAssigner_NoTasks_BacksOff(t *testing.T) {
	store := &fakeStore{runs: map[string]*domain.Run{}}

	assigner := &fakeTaskAssigner{
		tasks: nil, // no tasks available
	}

	var called atomic.Int32

	runFn := func(ctx context.Context, projectDir string, taskID string) (*RunResult, error) {
		called.Add(1)
		return &RunResult{State: domain.RunStateSucceeded}, nil
	}

	loop := NewLoop(LoopConfig{
		ProjectDir:    "/tmp/test-project",
		MaxConcurrent: 1,
	}, store, runFn)
	loop.SetTaskAssigner(assigner)

	loop.Start()
	time.Sleep(200 * time.Millisecond)
	loop.Stop()

	// With no tasks, no runs should have been launched.
	assert.Equal(t, int32(0), called.Load())
}

func TestLoop_NoTaskAssigner_EmptyTaskID(t *testing.T) {
	store := &fakeStore{runs: map[string]*domain.Run{}}

	var receivedTaskID string
	runFn := func(ctx context.Context, projectDir string, taskID string) (*RunResult, error) {
		receivedTaskID = taskID
		time.Sleep(20 * time.Millisecond)
		return &RunResult{State: domain.RunStateFailed}, nil
	}

	loop := NewLoop(LoopConfig{
		ProjectDir:    "/tmp/test-project",
		MaxConcurrent: 1,
	}, store, runFn)
	// No SetTaskAssigner call — backward compatible mode.

	loop.Start()
	time.Sleep(200 * time.Millisecond)
	loop.Stop()

	// Without a task assigner, taskID should be empty.
	assert.Empty(t, receivedTaskID)
}
