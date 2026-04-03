package host

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/ports"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeTaskStore records saved tasks for testing.
type fakeTaskStore struct {
	mu    sync.Mutex
	tasks map[string]*domain.Task
}

func newFakeTaskStore() *fakeTaskStore {
	return &fakeTaskStore{tasks: make(map[string]*domain.Task)}
}

func (f *fakeTaskStore) SaveTask(_ context.Context, t *domain.Task) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *t
	f.tasks[t.ID] = &cp
	return nil
}

func (f *fakeTaskStore) GetTask(_ context.Context, id string) (*domain.Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.tasks[id]
	if !ok {
		return nil, fmt.Errorf("task %q not found", id)
	}
	cp := *t
	return &cp, nil
}

func (f *fakeTaskStore) ListTasks(_ context.Context, _ string) ([]*domain.Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*domain.Task, 0, len(f.tasks))
	for _, t := range f.tasks {
		cp := *t
		out = append(out, &cp)
	}
	return out, nil
}

var _ ports.TaskStore = (*fakeTaskStore)(nil)

func TestLoop_StartStop(t *testing.T) {
	store := &fakeStore{runs: map[string]*domain.Run{}}
	var called atomic.Int32

	runFn := func(ctx context.Context, projectDir string, taskID string, attemptID string) (*RunResult, error) {
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

	runFn := func(ctx context.Context, projectDir string, taskID string, attemptID string) (*RunResult, error) {
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
	runFn := func(ctx context.Context, projectDir string, taskID string, attemptID string) (*RunResult, error) {
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

	runFn := func(ctx context.Context, projectDir string, taskID string, attemptID string) (*RunResult, error) {
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

	runFn := func(ctx context.Context, projectDir string, taskID string, attemptID string) (*RunResult, error) {
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

	runFn := func(ctx context.Context, projectDir string, taskID string, attemptID string) (*RunResult, error) {
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

	runFn := func(ctx context.Context, projectDir string, taskID string, attemptID string) (*RunResult, error) {
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
	runFn := func(ctx context.Context, projectDir string, taskID string, attemptID string) (*RunResult, error) {
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

// --- Three-phase (NewPhaseLoop) tests ---

func TestPhaseLoop_BasicFlow(t *testing.T) {
	store := &fakeStore{runs: map[string]*domain.Run{}}

	var mainCalls atomic.Int32
	var receivedTaskIDs []string
	var mu sync.Mutex

	listTasksFn := func(ctx context.Context, projectDir string) ([]Task, error) {
		return []Task{
			{ID: "task-1", Status: "open", Title: "Fix bug"},
			{ID: "task-2", Status: "closed", Title: "Done task"},
		}, nil
	}

	mainFn := func(ctx context.Context, projectDir string, taskID string, _ string, attemptID string) (*RunResult, error) {
		mainCalls.Add(1)
		mu.Lock()
		receivedTaskIDs = append(receivedTaskIDs, taskID)
		mu.Unlock()
		time.Sleep(20 * time.Millisecond)
		return &RunResult{RunID: "run-1", State: domain.RunStateSucceeded}, nil
	}

	loop := NewPhaseLoop(LoopConfig{
		ProjectDir:    "/tmp/test-project",
		MaxConcurrent: 1,
		DedupTimeout:  2 * time.Second,
	}, store, listTasksFn, mainFn)

	loop.Start()
	time.Sleep(300 * time.Millisecond)
	loop.Stop()

	// Should have picked task-1 (open) and skipped task-2 (closed).
	mu.Lock()
	defer mu.Unlock()
	assert.GreaterOrEqual(t, int(mainCalls.Load()), 1)
	assert.Equal(t, "task-1", receivedTaskIDs[0])
}

func TestPhaseLoop_SkipsClosedTasks(t *testing.T) {
	store := &fakeStore{runs: map[string]*domain.Run{}}

	var mainCalls atomic.Int32

	listTasksFn := func(ctx context.Context, projectDir string) ([]Task, error) {
		return []Task{
			{ID: "task-1", Status: "closed"},
			{ID: "task-2", Status: "in-progress"},
		}, nil
	}

	mainFn := func(ctx context.Context, projectDir string, taskID string, _ string, attemptID string) (*RunResult, error) {
		mainCalls.Add(1)
		return &RunResult{State: domain.RunStateSucceeded}, nil
	}

	loop := NewPhaseLoop(LoopConfig{
		ProjectDir:    "/tmp/test-project",
		MaxConcurrent: 1,
	}, store, listTasksFn, mainFn)

	loop.Start()
	time.Sleep(200 * time.Millisecond)
	loop.Stop()

	// No open tasks → no main runs.
	assert.Equal(t, int32(0), mainCalls.Load())
}

func TestPhaseLoop_DedupFiltersOpenTasks(t *testing.T) {
	store := &fakeStore{runs: map[string]*domain.Run{}}

	var receivedTaskIDs []string
	var mu sync.Mutex

	listTasksFn := func(ctx context.Context, projectDir string) ([]Task, error) {
		return []Task{{ID: "task-1", Status: "open"}}, nil
	}

	mainFn := func(ctx context.Context, projectDir string, taskID string, _ string, attemptID string) (*RunResult, error) {
		mu.Lock()
		receivedTaskIDs = append(receivedTaskIDs, taskID)
		mu.Unlock()
		time.Sleep(20 * time.Millisecond)
		return &RunResult{State: domain.RunStateSucceeded}, nil
	}

	loop := NewPhaseLoop(LoopConfig{
		ProjectDir:    "/tmp/test-project",
		MaxConcurrent: 1,
		DedupTimeout:  2 * time.Second,
	}, store, listTasksFn, mainFn)

	loop.Start()
	time.Sleep(200 * time.Millisecond)
	loop.Stop()

	mu.Lock()
	defer mu.Unlock()
	// With 2s dedup, task-1 should only be assigned once.
	assert.Equal(t, 1, len(receivedTaskIDs))
}

// --- GetTaskSnapshot tests ---

func TestLoop_GetTaskSnapshot_Empty(t *testing.T) {
	store := &fakeStore{runs: map[string]*domain.Run{}}
	runFn := func(ctx context.Context, projectDir string, taskID string, attemptID string) (*RunResult, error) {
		time.Sleep(100 * time.Millisecond)
		return &RunResult{State: domain.RunStateFailed}, nil
	}

	loop := NewLoop(LoopConfig{
		ProjectDir:    "/tmp/test-project",
		MaxConcurrent: 1,
	}, store, runFn)

	snapshot := loop.GetTaskSnapshot()
	assert.Empty(t, snapshot, "snapshot should be empty when no tasks fetched")
}

func TestLoop_GetTaskSnapshot_ReturnsLastTasks(t *testing.T) {
	store := &fakeStore{runs: map[string]*domain.Run{}}

	tasks := []Task{
		{ID: "task-1", Status: "open", Title: "Fix bug"},
		{ID: "task-2", Status: "closed", Title: "Done task"},
		{ID: "task-3", Status: "open", Title: "Add feature"},
	}

	listTasksFn := func(ctx context.Context, projectDir string) ([]Task, error) {
		return tasks, nil
	}

	mainFn := func(ctx context.Context, projectDir string, taskID string, _ string, attemptID string) (*RunResult, error) {
		time.Sleep(50 * time.Millisecond)
		return &RunResult{RunID: "run-" + taskID, State: domain.RunStateSucceeded}, nil
	}

	loop := NewPhaseLoop(LoopConfig{
		ProjectDir:    "/tmp/test-project",
		MaxConcurrent: 1,
		DedupTimeout:  2 * time.Second,
	}, store, listTasksFn, mainFn)

	loop.Start()
	time.Sleep(300 * time.Millisecond)
	loop.Stop()

	snapshot := loop.GetTaskSnapshot()
	assert.Len(t, snapshot, 3, "should return all 3 tasks")

	// task-1 should be assigned (it's open and was picked).
	assert.Equal(t, "task-1", snapshot[0].Task.ID)
	assert.True(t, snapshot[0].Assigned, "task-1 should be assigned")
	assert.NotEmpty(t, snapshot[0].RunID, "task-1 should have a run ID")

	// task-2 is closed — should not be assigned.
	assert.Equal(t, "task-2", snapshot[1].Task.ID)
	assert.False(t, snapshot[1].Assigned, "task-2 (closed) should not be assigned")

	// task-3 should be assigned (it's open and would be picked after task-1).
	assert.Equal(t, "task-3", snapshot[2].Task.ID)
	assert.True(t, snapshot[2].Assigned, "task-3 should be assigned")
}

func TestLoop_GetTaskSnapshot_LegacyWithAssigner(t *testing.T) {
	store := &fakeStore{runs: map[string]*domain.Run{}}

	assigner := &fakeTaskAssigner{
		tasks: []Task{
			{ID: "task-1", Title: "Fix bug"},
			{ID: "task-2", Title: "Add feature"},
		},
	}

	runFn := func(ctx context.Context, projectDir string, taskID string, attemptID string) (*RunResult, error) {
		time.Sleep(20 * time.Millisecond)
		return &RunResult{State: domain.RunStateSucceeded}, nil
	}

	loop := NewLoop(LoopConfig{
		ProjectDir:    "/tmp/test-project",
		MaxConcurrent: 1,
		DedupTimeout:  2 * time.Second,
	}, store, runFn)
	loop.SetTaskAssigner(assigner)

	loop.Start()
	time.Sleep(200 * time.Millisecond)
	loop.Stop()

	snapshot := loop.GetTaskSnapshot()
	assert.Len(t, snapshot, 2, "should return tasks from assigner")
	assert.Equal(t, "task-1", snapshot[0].Task.ID)
	assert.True(t, snapshot[0].Assigned, "task-1 should be assigned")
	assert.Equal(t, "task-2", snapshot[1].Task.ID)
	assert.True(t, snapshot[1].Assigned, "task-2 should be assigned")
}

func TestLoop_GetTaskSnapshot_DedupExpired(t *testing.T) {
	store := &fakeStore{runs: map[string]*domain.Run{}}

	tasks := []Task{
		{ID: "task-1", Status: "open", Title: "Fix bug"},
	}

	listTasksFn := func(ctx context.Context, projectDir string) ([]Task, error) {
		return tasks, nil
	}

	mainFn := func(ctx context.Context, projectDir string, taskID string, _ string, attemptID string) (*RunResult, error) {
		return &RunResult{RunID: "run-1", State: domain.RunStateSucceeded}, nil
	}

	loop := NewPhaseLoop(LoopConfig{
		ProjectDir:    "/tmp/test-project",
		MaxConcurrent: 1,
		DedupTimeout:  10 * time.Millisecond, // very short dedup
	}, store, listTasksFn, mainFn)

	loop.Start()
	time.Sleep(200 * time.Millisecond)
	loop.Stop()

	snapshot := loop.GetTaskSnapshot()
	assert.Len(t, snapshot, 1)
	// After dedup expires, the task should show as unassigned in the snapshot
	// (the dedup check in GetTaskSnapshot uses current time).
	// But the run ID may still be recorded from a previous assignment.
	assert.Equal(t, "task-1", snapshot[0].Task.ID)
}

// --- StopOnError tests ---

func TestLoop_StopOnError_HaltsOnFailure(t *testing.T) {
	store := &fakeStore{runs: map[string]*domain.Run{}}
	var called atomic.Int32

	runFn := func(ctx context.Context, projectDir string, taskID string, attemptID string) (*RunResult, error) {
		n := called.Add(1)
		time.Sleep(20 * time.Millisecond)
		if n == 1 {
			return &RunResult{State: domain.RunStateFailed}, nil
		}
		// Subsequent calls should not happen while halted.
		return &RunResult{State: domain.RunStateSucceeded}, nil
	}

	loop := NewLoop(LoopConfig{
		ProjectDir:    "/tmp/test-project",
		MaxConcurrent: 1,
		StopOnError:   true,
	}, store, runFn)

	loop.Start()
	// Wait for the first run to fail and halt.
	time.Sleep(300 * time.Millisecond)

	halted, haltErr := loop.Halted()
	assert.True(t, halted, "loop should be halted after failure")
	assert.NotEmpty(t, haltErr, "halt error should be set")
	assert.True(t, loop.Running(), "loop should still be running (not stopped)")

	// Only 1 run should have been launched since the loop halts after.
	assert.Equal(t, int32(1), called.Load(), "only one run should have been launched before halt")

	loop.Stop()
}

func TestLoop_StopOnError_ResumeAllowsNewWork(t *testing.T) {
	store := &fakeStore{runs: map[string]*domain.Run{}}
	var called atomic.Int32
	var resumeReady atomic.Bool

	runFn := func(ctx context.Context, projectDir string, taskID string, attemptID string) (*RunResult, error) {
		n := called.Add(1)
		time.Sleep(20 * time.Millisecond)
		if n == 1 {
			return &RunResult{State: domain.RunStateFailed}, nil
		}
		resumeReady.Store(true)
		return &RunResult{State: domain.RunStateSucceeded}, nil
	}

	loop := NewLoop(LoopConfig{
		ProjectDir:    "/tmp/test-project",
		MaxConcurrent: 1,
		StopOnError:   true,
	}, store, runFn)

	loop.Start()
	// Wait for the first run to fail and halt.
	time.Sleep(300 * time.Millisecond)

	halted, _ := loop.Halted()
	assert.True(t, halted, "loop should be halted after failure")
	assert.Equal(t, int32(1), called.Load())

	// Resume the loop.
	loop.Resume()

	halted, _ = loop.Halted()
	assert.False(t, halted, "loop should no longer be halted after resume")

	// Resume() signals the loop to wake from its sleep, so new work should
	// be picked up quickly.
	deadline := time.After(2 * time.Second)
	for !resumeReady.Load() {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for resumed run")
		case <-time.After(50 * time.Millisecond):
		}
	}
	loop.Stop()

	// After resume, at least one more run should have been launched.
	assert.GreaterOrEqual(t, called.Load(), int32(2), "resume should allow new runs")
}

func TestLoop_StopOnError_Disabled_ContinuesOnFailure(t *testing.T) {
	store := &fakeStore{runs: map[string]*domain.Run{}}
	var called atomic.Int32

	runFn := func(ctx context.Context, projectDir string, taskID string, attemptID string) (*RunResult, error) {
		called.Add(1)
		time.Sleep(20 * time.Millisecond)
		return &RunResult{State: domain.RunStateFailed}, nil
	}

	loop := NewLoop(LoopConfig{
		ProjectDir:    "/tmp/test-project",
		MaxConcurrent: 1,
		StopOnError:   false, // disabled
	}, store, runFn)

	loop.Start()
	// After first failure, loop backs off for idlePollInterval (2 min), so
	// we can't easily wait for multiple runs. Instead, verify state after
	// the first run: loop should not be halted and should still be running.
	time.Sleep(300 * time.Millisecond)
	loop.Stop()

	halted, _ := loop.Halted()
	assert.False(t, halted, "loop should not be halted when stop_on_error is disabled")
	assert.GreaterOrEqual(t, called.Load(), int32(1), "at least one run should have been launched")
}

func TestPhaseLoop_StopOnError_HaltsOnFailure(t *testing.T) {
	store := &fakeStore{runs: map[string]*domain.Run{}}
	var mainCalls atomic.Int32

	listTasksFn := func(ctx context.Context, projectDir string) ([]Task, error) {
		return []Task{
			{ID: "task-1", Status: "open"},
			{ID: "task-2", Status: "open"},
		}, nil
	}

	mainFn := func(ctx context.Context, projectDir string, taskID string, _ string, attemptID string) (*RunResult, error) {
		mainCalls.Add(1)
		time.Sleep(20 * time.Millisecond)
		return &RunResult{RunID: "run-" + taskID, State: domain.RunStateFailed}, nil
	}

	loop := NewPhaseLoop(LoopConfig{
		ProjectDir:    "/tmp/test-project",
		MaxConcurrent: 1,
		DedupTimeout:  2 * time.Second,
		StopOnError:   true,
	}, store, listTasksFn, mainFn)

	loop.Start()
	time.Sleep(300 * time.Millisecond)

	halted, haltErr := loop.Halted()
	assert.True(t, halted, "phased loop should be halted after failure")
	assert.Contains(t, haltErr, "task-1", "halt error should reference the failed task")

	// Only task-1 should have been attempted.
	assert.Equal(t, int32(1), mainCalls.Load())

	loop.Stop()
}

// --- MaxConsecutiveFailures tests ---

func TestLoop_ConsecutiveFailures_RecordAndReset(t *testing.T) {
	store := &fakeStore{runs: map[string]*domain.Run{}}
	loop := NewLoop(LoopConfig{
		ProjectDir:             "/tmp/test-project",
		MaxConcurrent:          1,
		MaxConsecutiveFailures: 3,
	}, store, func(ctx context.Context, projectDir string, taskID string, attemptID string) (*RunResult, error) {
		return &RunResult{State: domain.RunStateSucceeded}, nil
	})

	// Below threshold — not halted.
	assert.False(t, loop.recordConsecutiveFailure())
	assert.False(t, loop.recordConsecutiveFailure())

	// Third failure reaches threshold.
	assert.True(t, loop.recordConsecutiveFailure())

	// Reset clears the counter.
	loop.resetConsecutiveFailures()
	assert.False(t, loop.recordConsecutiveFailure()) // back to 1
}

func TestLoop_ConsecutiveFailures_HaltsLoopOnThreshold(t *testing.T) {
	store := &fakeStore{runs: map[string]*domain.Run{}}
	var called atomic.Int32

	runFn := func(ctx context.Context, projectDir string, taskID string, attemptID string) (*RunResult, error) {
		n := called.Add(1)
		time.Sleep(20 * time.Millisecond)
		if n == 1 {
			return &RunResult{State: domain.RunStateFailed}, nil
		}
		return &RunResult{State: domain.RunStateSucceeded}, nil
	}

	// With MaxConsecutiveFailures=1, behaves like stop_on_error.
	loop := NewLoop(LoopConfig{
		ProjectDir:             "/tmp/test-project",
		MaxConcurrent:          1,
		MaxConsecutiveFailures: 1,
	}, store, runFn)

	loop.Start()
	time.Sleep(300 * time.Millisecond)

	halted, haltErr := loop.Halted()
	assert.True(t, halted, "loop should be halted after 1 consecutive failure")
	assert.Contains(t, haltErr, "consecutive failures")
	assert.Equal(t, int32(1), called.Load())

	loop.Stop()
}

func TestLoop_ConsecutiveFailures_ResumeResetsCounter(t *testing.T) {
	store := &fakeStore{runs: map[string]*domain.Run{}}
	var called atomic.Int32

	runFn := func(ctx context.Context, projectDir string, taskID string, attemptID string) (*RunResult, error) {
		called.Add(1)
		time.Sleep(20 * time.Millisecond)
		return &RunResult{State: domain.RunStateFailed}, nil
	}

	loop := NewLoop(LoopConfig{
		ProjectDir:             "/tmp/test-project",
		MaxConcurrent:          1,
		MaxConsecutiveFailures: 1,
	}, store, runFn)

	loop.Start()
	time.Sleep(300 * time.Millisecond)

	halted, _ := loop.Halted()
	assert.True(t, halted, "loop should halt after 1 consecutive failure")
	assert.Equal(t, int32(1), called.Load())

	// Resume clears the counter so it takes another failure to halt.
	loop.Resume()
	time.Sleep(300 * time.Millisecond)

	halted, _ = loop.Halted()
	assert.True(t, halted, "loop should halt again after resume")
	assert.Equal(t, int32(2), called.Load())

	loop.Stop()
}

func TestLoop_ConsecutiveFailures_DefaultThreshold(t *testing.T) {
	store := &fakeStore{runs: map[string]*domain.Run{}}

	// MaxConsecutiveFailures=0 should default to 3.
	loop := NewLoop(LoopConfig{
		ProjectDir:             "/tmp/test-project",
		MaxConcurrent:          1,
		MaxConsecutiveFailures: 0,
	}, store, func(ctx context.Context, projectDir string, taskID string, attemptID string) (*RunResult, error) {
		return &RunResult{State: domain.RunStateSucceeded}, nil
	})

	assert.Equal(t, defaultMaxConsecutiveFailures, loop.config.MaxConsecutiveFailures)
}

func TestLoop_ConsecutiveFailures_SuccessResetsInLoop(t *testing.T) {
	store := &fakeStore{runs: map[string]*domain.Run{}}
	var called atomic.Int32

	runFn := func(ctx context.Context, projectDir string, taskID string, attemptID string) (*RunResult, error) {
		n := called.Add(1)
		time.Sleep(20 * time.Millisecond)
		// First succeeds (resets counter), second fails and halts (threshold=1).
		if n == 1 {
			return &RunResult{State: domain.RunStateSucceeded}, nil
		}
		return &RunResult{State: domain.RunStateFailed}, nil
	}

	loop := NewLoop(LoopConfig{
		ProjectDir:             "/tmp/test-project",
		MaxConcurrent:          1,
		MaxConsecutiveFailures: 1,
	}, store, runFn)

	loop.Start()
	time.Sleep(300 * time.Millisecond)

	halted, _ := loop.Halted()
	assert.True(t, halted, "loop should halt on second run (first failure after success)")
	assert.Equal(t, int32(2), called.Load(), "two runs: one success, one failure")

	loop.Stop()
}

func TestPhaseLoop_ConsecutiveFailures_HaltsAfterThreshold(t *testing.T) {
	store := &fakeStore{runs: map[string]*domain.Run{}}
	var mainCalls atomic.Int32

	listTasksFn := func(ctx context.Context, projectDir string) ([]Task, error) {
		return []Task{{ID: fmt.Sprintf("task-%d", mainCalls.Load()+1), Status: "open"}}, nil
	}

	mainFn := func(ctx context.Context, projectDir string, taskID string, _ string, attemptID string) (*RunResult, error) {
		mainCalls.Add(1)
		time.Sleep(20 * time.Millisecond)
		return &RunResult{RunID: "run-" + taskID, State: domain.RunStateFailed}, nil
	}

	loop := NewPhaseLoop(LoopConfig{
		ProjectDir:             "/tmp/test-project",
		MaxConcurrent:          1,
		DedupTimeout:           50 * time.Millisecond,
		MaxConsecutiveFailures: 1,
	}, store, listTasksFn, mainFn)

	loop.Start()
	time.Sleep(300 * time.Millisecond)

	halted, haltErr := loop.Halted()
	assert.True(t, halted, "phased loop should be halted after consecutive failures")
	assert.Contains(t, haltErr, "consecutive failures")
	assert.Equal(t, int32(1), mainCalls.Load())

	loop.Stop()
}

func TestLoop_Halted_DefaultFalse(t *testing.T) {
	store := &fakeStore{runs: map[string]*domain.Run{}}
	runFn := func(ctx context.Context, projectDir string, taskID string, attemptID string) (*RunResult, error) {
		return &RunResult{State: domain.RunStateSucceeded}, nil
	}

	loop := NewLoop(LoopConfig{
		ProjectDir:    "/tmp/test-project",
		MaxConcurrent: 1,
	}, store, runFn)

	halted, haltErr := loop.Halted()
	assert.False(t, halted)
	assert.Empty(t, haltErr)
}

func TestLoop_Resume_NopWhenNotHalted(t *testing.T) {
	store := &fakeStore{runs: map[string]*domain.Run{}}
	runFn := func(ctx context.Context, projectDir string, taskID string, attemptID string) (*RunResult, error) {
		return &RunResult{State: domain.RunStateSucceeded}, nil
	}

	loop := NewLoop(LoopConfig{
		ProjectDir:    "/tmp/test-project",
		MaxConcurrent: 1,
	}, store, runFn)

	// Resume when not halted should be a no-op (no panic).
	loop.Resume()
	halted, _ := loop.Halted()
	assert.False(t, halted)
}

// --- Attempt tracking tests ---

func TestPhaseLoop_CreatesAttemptOnTaskPick(t *testing.T) {
	store := &fakeStore{runs: map[string]*domain.Run{}}
	taskStore := newFakeTaskStore()

	listTasksFn := func(ctx context.Context, projectDir string) ([]Task, error) {
		return []Task{{ID: "task-1", Status: "open", Title: "Fix bug"}}, nil
	}

	var receivedAttemptID string
	var mu sync.Mutex

	mainFn := func(ctx context.Context, projectDir string, taskID string, _ string, attemptID string) (*RunResult, error) {
		mu.Lock()
		receivedAttemptID = attemptID
		mu.Unlock()
		time.Sleep(20 * time.Millisecond)
		return &RunResult{RunID: "run-1", State: domain.RunStateSucceeded}, nil
	}

	loop := NewPhaseLoop(LoopConfig{
		ProjectDir:    "/tmp/test-project",
		MaxConcurrent: 1,
		DedupTimeout:  2 * time.Second,
	}, store, listTasksFn, mainFn)
	loop.SetTaskStore(taskStore)

	loop.Start()
	time.Sleep(300 * time.Millisecond)
	loop.Stop()

	// An attempt should have been created.
	require.Equal(t, 1, store.countAttempts(), "expected one attempt to be created")

	mu.Lock()
	aid := receivedAttemptID
	mu.Unlock()

	assert.NotEmpty(t, aid, "attempt ID should be passed to mainFn")

	// The attempt should be completed (succeeded).
	attempts := store.allAttempts()
	require.Len(t, attempts, 1)
	assert.Equal(t, domain.AttemptResultSucceeded, attempts[0].Result)
	assert.Equal(t, "task-1", attempts[0].TaskID)

	// The task record should exist.
	task, err := taskStore.GetTask(context.Background(), "task-1")
	require.NoError(t, err)
	assert.Equal(t, "Fix bug", task.Title)
}

func TestPhaseLoop_AttemptCompletedAsFailed(t *testing.T) {
	store := &fakeStore{runs: map[string]*domain.Run{}}
	taskStore := newFakeTaskStore()

	listTasksFn := func(ctx context.Context, projectDir string) ([]Task, error) {
		return []Task{{ID: "task-1", Status: "open"}}, nil
	}

	mainFn := func(ctx context.Context, projectDir string, taskID string, _ string, attemptID string) (*RunResult, error) {
		return &RunResult{RunID: "run-1", State: domain.RunStateFailed}, nil
	}

	loop := NewPhaseLoop(LoopConfig{
		ProjectDir:    "/tmp/test-project",
		MaxConcurrent: 1,
		DedupTimeout:  2 * time.Second,
	}, store, listTasksFn, mainFn)
	loop.SetTaskStore(taskStore)

	loop.Start()
	time.Sleep(200 * time.Millisecond)
	loop.Stop()

	attempts := store.allAttempts()
	require.Len(t, attempts, 1)
	assert.Equal(t, domain.AttemptResultFailed, attempts[0].Result)
}

func TestLegacyLoop_CreatesAttemptWhenTaskAssignerSet(t *testing.T) {
	store := &fakeStore{runs: map[string]*domain.Run{}}
	taskStore := newFakeTaskStore()

	assigner := &fakeTaskAssigner{
		tasks: []Task{{ID: "task-1", Title: "Fix bug"}},
	}

	var receivedAttemptID string
	var mu sync.Mutex

	runFn := func(ctx context.Context, projectDir string, taskID string, attemptID string) (*RunResult, error) {
		mu.Lock()
		receivedAttemptID = attemptID
		mu.Unlock()
		time.Sleep(20 * time.Millisecond)
		return &RunResult{State: domain.RunStateSucceeded}, nil
	}

	loop := NewLoop(LoopConfig{
		ProjectDir:    "/tmp/test-project",
		MaxConcurrent: 1,
		DedupTimeout:  2 * time.Second,
	}, store, runFn)
	loop.SetTaskAssigner(assigner)
	loop.SetTaskStore(taskStore)

	loop.Start()
	time.Sleep(200 * time.Millisecond)
	loop.Stop()

	mu.Lock()
	aid := receivedAttemptID
	mu.Unlock()

	assert.NotEmpty(t, aid, "attempt ID should be passed to runFn when task assigner is set")
	assert.Equal(t, 1, store.countAttempts(), "one attempt should be created")

	attempts := store.allAttempts()
	require.Len(t, attempts, 1)
	assert.Equal(t, "task-1", attempts[0].TaskID)
	assert.Equal(t, domain.AttemptResultSucceeded, attempts[0].Result)
}

func TestLegacyLoop_NoAttemptWhenNoTaskAssigner(t *testing.T) {
	store := &fakeStore{runs: map[string]*domain.Run{}}

	var receivedAttemptID string
	runFn := func(ctx context.Context, projectDir string, taskID string, attemptID string) (*RunResult, error) {
		receivedAttemptID = attemptID
		return &RunResult{State: domain.RunStateFailed}, nil
	}

	loop := NewLoop(LoopConfig{
		ProjectDir:    "/tmp/test-project",
		MaxConcurrent: 1,
	}, store, runFn)
	// No task assigner — task ID is empty. A transient attempt ID is still
	// generated for pool key uniqueness, but no record is persisted.

	loop.Start()
	time.Sleep(200 * time.Millisecond)
	loop.Stop()

	assert.NotEmpty(t, receivedAttemptID, "attempt ID should be generated even for sentinel tasks")
	assert.Equal(t, 0, store.countAttempts(), "no attempt records should be created without task assigner")
}

func TestLegacyLoop_AttemptCompletedOnPanic(t *testing.T) {
	// Regression test: if runner panics, the attempt must still be completed
	// (not left permanently stuck as 'running') and the loop must not deadlock.
	store := &fakeStore{runs: map[string]*domain.Run{}}
	taskStore := newFakeTaskStore()

	assigner := &fakeTaskAssigner{
		tasks: []Task{{ID: "task-panic", Title: "Panic task"}},
	}

	var callCount atomic.Int32
	runFn := func(ctx context.Context, projectDir string, taskID string, attemptID string) (*RunResult, error) {
		n := callCount.Add(1)
		if n == 1 {
			panic("simulated runner panic")
		}
		return &RunResult{State: domain.RunStateSucceeded}, nil
	}

	loop := NewLoop(LoopConfig{
		ProjectDir:    "/tmp/test-project",
		MaxConcurrent: 1,
		DedupTimeout:  50 * time.Millisecond, // short so retry happens quickly
		StopOnError:   true,                  // halts after first failure, allowing Resume to wake it
	}, store, runFn)
	loop.SetTaskAssigner(assigner)
	loop.SetTaskStore(taskStore)

	loop.Start()
	// Wait for the first (panicking) run to complete and the loop to halt.
	time.Sleep(200 * time.Millisecond)
	require.Equal(t, int32(1), callCount.Load(), "first run should have completed (panicked)")

	// The first attempt must be completed even though runner panicked.
	attempts := store.allAttempts()
	require.Len(t, attempts, 1, "one attempt should have been created")
	assert.NotZero(t, attempts[0].EndedAt, "attempt must be completed, not stuck as running after panic")
	assert.Equal(t, domain.AttemptResultFailed, attempts[0].Result, "panicked run should map to failed")

	loop.Stop()
}

func TestLegacyLoop_AttemptCompletedOnRetry(t *testing.T) {
	// Regression test: when a task is retried after dedup expiry, all attempts
	// (including the retry) must be completed.
	store := &fakeStore{runs: map[string]*domain.Run{}}
	taskStore := newFakeTaskStore()

	assigner := &fakeTaskAssigner{
		tasks: []Task{{ID: "task-retry", Title: "Retry task"}},
	}

	var callCount atomic.Int32
	runFn := func(ctx context.Context, projectDir string, taskID string, attemptID string) (*RunResult, error) {
		n := callCount.Add(1)
		if n == 1 {
			return &RunResult{State: domain.RunStateFailed}, nil
		}
		return &RunResult{State: domain.RunStateSucceeded}, nil
	}

	loop := NewLoop(LoopConfig{
		ProjectDir:    "/tmp/test-project",
		MaxConcurrent: 1,
		DedupTimeout:  50 * time.Millisecond, // short so retry happens after Resume
		StopOnError:   true,                  // halts after first failure, allowing Resume to wake it
	}, store, runFn)
	loop.SetTaskAssigner(assigner)
	loop.SetTaskStore(taskStore)

	loop.Start()
	// Wait for the first run to fail and the loop to halt.
	time.Sleep(200 * time.Millisecond)
	require.Equal(t, int32(1), callCount.Load(), "first run should have completed")

	// Resume wakes the loop so the retry is picked up once the dedup has expired.
	loop.Resume()
	time.Sleep(200 * time.Millisecond)
	loop.Stop()

	require.GreaterOrEqual(t, int(callCount.Load()), 2, "task should have been attempted at least twice")

	attempts := store.allAttempts()
	require.GreaterOrEqual(t, len(attempts), 2, "should have at least 2 attempt records")
	for _, a := range attempts {
		assert.NotZero(t, a.EndedAt, "every attempt must be completed, not stuck as running")
	}
}

func TestPhaseLoop_AttemptCompletedWhenMainReturnsNilResult(t *testing.T) {
	// Regression test: mainFn returning (nil, nil) must not leave the attempt
	// stuck as 'running'. The goroutine used to dereference mainResult without
	// guarding for nil, causing a panic that skipped completeAttempt.
	store := &fakeStore{runs: map[string]*domain.Run{}}
	taskStore := newFakeTaskStore()

	listTasksFn := func(ctx context.Context, projectDir string) ([]Task, error) {
		return []Task{{ID: "task-nil", Status: "open"}}, nil
	}

	mainFn := func(ctx context.Context, projectDir string, taskID string, _ string, attemptID string) (*RunResult, error) {
		// Return (nil, nil) — valid but unusual; must not orphan the attempt.
		return nil, nil
	}

	loop := NewPhaseLoop(LoopConfig{
		ProjectDir:    "/tmp/test-project",
		MaxConcurrent: 1,
		DedupTimeout:  2 * time.Second,
	}, store, listTasksFn, mainFn)
	loop.SetTaskStore(taskStore)

	loop.Start()
	time.Sleep(300 * time.Millisecond)
	loop.Stop()

	attempts := store.allAttempts()
	require.Len(t, attempts, 1)
	// The attempt must be completed (not left as running).
	assert.NotZero(t, attempts[0].EndedAt, "attempt must be completed, not stuck as running")
	assert.Equal(t, domain.AttemptResultFailed, attempts[0].Result, "nil result should map to failed")
}

func TestPhaseLoop_AttemptCompletedOnRetry(t *testing.T) {
	// Regression test: when a task is retried after dedup expiry, both attempts
	// (the first failed one and the retry) must be completed.
	//
	// Use StopOnError=true so the loop halts after the first failure. Then call
	// Resume() to wake it from the 2-minute backoff sleep immediately, allowing
	// the retry to happen within the test window.
	store := &fakeStore{runs: map[string]*domain.Run{}}
	taskStore := newFakeTaskStore()

	var callCount atomic.Int32
	listTasksFn := func(ctx context.Context, projectDir string) ([]Task, error) {
		return []Task{{ID: "task-retry", Status: "open"}}, nil
	}

	mainFn := func(ctx context.Context, projectDir string, taskID string, _ string, attemptID string) (*RunResult, error) {
		n := callCount.Add(1)
		if n == 1 {
			return &RunResult{State: domain.RunStateFailed}, nil
		}
		return &RunResult{State: domain.RunStateSucceeded}, nil
	}

	loop := NewPhaseLoop(LoopConfig{
		ProjectDir:    "/tmp/test-project",
		MaxConcurrent: 1,
		DedupTimeout:  50 * time.Millisecond, // short so retry happens after Resume
		StopOnError:   true,                  // halts after first failure, allowing Resume to wake it
	}, store, listTasksFn, mainFn)
	loop.SetTaskStore(taskStore)

	loop.Start()
	// Wait for the first run to fail and the loop to halt.
	time.Sleep(200 * time.Millisecond)
	require.Equal(t, int32(1), callCount.Load(), "first run should have completed")

	// Resume wakes the loop from its backoff sleep and clears halted state,
	// allowing the retry to be picked up once the dedup has expired.
	loop.Resume()
	time.Sleep(200 * time.Millisecond)
	loop.Stop()

	require.GreaterOrEqual(t, int(callCount.Load()), 2, "task should have been attempted at least twice")

	attempts := store.allAttempts()
	require.GreaterOrEqual(t, len(attempts), 2, "should have at least 2 attempt records")
	for _, a := range attempts {
		assert.NotZero(t, a.EndedAt, "every attempt must be completed, not stuck as running")
	}
}

// TestCreateAttemptForTask_AlwaysNonEmpty verifies that createAttemptForTask
// always returns a non-empty attempt ID so concurrent tasks each get a unique
// container pool key and KV namespace.
func TestCreateAttemptForTask_AlwaysNonEmpty(t *testing.T) {
	store := &fakeStore{runs: map[string]*domain.Run{}}

	loop := NewPhaseLoop(LoopConfig{ProjectDir: "/tmp/test"}, store,
		func(_ context.Context, _ string) ([]Task, error) { return nil, nil },
		func(_ context.Context, _ string, _ string, _ string, _ string) (*RunResult, error) {
			return &RunResult{State: domain.RunStateSucceeded}, nil
		},
	)

	// Create two attempts for different tasks.
	id1 := loop.createAttemptForTask("task-A", "Task A", "/tmp/test")
	id2 := loop.createAttemptForTask("task-B", "Task B", "/tmp/test")

	assert.NotEmpty(t, id1, "attempt ID must not be empty")
	assert.NotEmpty(t, id2, "attempt ID must not be empty")
	assert.NotEqual(t, id1, id2, "concurrent tasks must receive distinct attempt IDs")
}

// TestPhaseLoop_ConcurrentTasks_UniqueAttemptIDs verifies that when the loop
// runs multiple tasks concurrently, each task receives a distinct attempt ID.
// This prevents them from sharing the same container pool key.
func TestPhaseLoop_ConcurrentTasks_UniqueAttemptIDs(t *testing.T) {
	store := &fakeStore{runs: map[string]*domain.Run{}}

	var (
		mu      sync.Mutex
		seenIDs = make(map[string]string) // taskID → attemptID
	)

	tasks := []Task{
		{ID: "task-1", Status: "open"},
		{ID: "task-2", Status: "open"},
	}

	// Always return the same two tasks; dedup prevents re-picking.
	listTasksFn := func(ctx context.Context, projectDir string) ([]Task, error) {
		return tasks, nil
	}

	ready := make(chan struct{})
	var readyOnce sync.Once

	mainFn := func(ctx context.Context, projectDir string, taskID string, _ string, attemptID string) (*RunResult, error) {
		mu.Lock()
		seenIDs[taskID] = attemptID
		if len(seenIDs) == 2 {
			readyOnce.Do(func() { close(ready) })
		}
		mu.Unlock()
		// Hold the slot until both tasks have started.
		select {
		case <-ready:
		case <-time.After(2 * time.Second):
		}
		return &RunResult{State: domain.RunStateSucceeded}, nil
	}

	loop := NewPhaseLoop(LoopConfig{
		ProjectDir:    "/tmp/test-conc",
		MaxConcurrent: 2,
		DedupTimeout:  5 * time.Second,
	}, store, listTasksFn, mainFn)
	// Intentionally no SetAttemptStore — exercises the no-store path.

	loop.Start()
	select {
	case <-ready:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for both tasks to start")
	}
	loop.Stop()

	mu.Lock()
	defer mu.Unlock()

	require.Len(t, seenIDs, 2, "both tasks should have run concurrently")

	id1, id2 := seenIDs["task-1"], seenIDs["task-2"]
	assert.NotEmpty(t, id1, "task-1 must have a non-empty attempt ID")
	assert.NotEmpty(t, id2, "task-2 must have a non-empty attempt ID")
	assert.NotEqual(t, id1, id2, "concurrent tasks must receive distinct attempt IDs")
}

func TestLoop_Halt_SetsHaltedState(t *testing.T) {
	store := &fakeStore{runs: map[string]*domain.Run{}}
	loop := NewLoop(LoopConfig{ProjectDir: "/tmp/test"}, store, func(_ context.Context, _ string, _ string, _ string) (*RunResult, error) {
		return &RunResult{State: domain.RunStateSucceeded}, nil
	})

	halted, msg := loop.Halted()
	assert.False(t, halted, "should not be halted initially")
	assert.Empty(t, msg)

	loop.Halt("container crashed")

	halted, msg = loop.Halted()
	assert.True(t, halted, "should be halted after Halt()")
	assert.Equal(t, "container crashed", msg)
}

func TestLoop_Halt_PreventsNewWork(t *testing.T) {
	store := &fakeStore{runs: map[string]*domain.Run{}}
	var called atomic.Int32
	loop := NewLoop(LoopConfig{
		ProjectDir:    "/tmp/test",
		MaxConcurrent: 1,
	}, store, func(_ context.Context, _ string, _ string, _ string) (*RunResult, error) {
		called.Add(1)
		time.Sleep(20 * time.Millisecond)
		return &RunResult{State: domain.RunStateSucceeded}, nil
	})

	loop.Halt("pre-halted")
	loop.Start()

	// Give the loop time to attempt launching work.
	time.Sleep(100 * time.Millisecond)
	loop.Stop()

	// No work should have been picked up because the loop was halted before starting.
	assert.Equal(t, int32(0), called.Load(), "halted loop should not launch any runs")
}
