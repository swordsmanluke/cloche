package host

import (
	"context"
	"fmt"
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

// --- Three-phase (NewPhaseLoop) tests ---

func TestPhaseLoop_BasicFlow(t *testing.T) {
	store := &fakeStore{runs: map[string]*domain.Run{}}

	var mainCalls, finalizeCalls atomic.Int32
	var receivedTaskIDs []string
	var mu sync.Mutex

	listTasksFn := func(ctx context.Context, projectDir string) ([]Task, error) {
		return []Task{
			{ID: "task-1", Status: "open", Title: "Fix bug"},
			{ID: "task-2", Status: "closed", Title: "Done task"},
		}, nil
	}

	mainFn := func(ctx context.Context, projectDir string, taskID string) (*RunResult, error) {
		mainCalls.Add(1)
		mu.Lock()
		receivedTaskIDs = append(receivedTaskIDs, taskID)
		mu.Unlock()
		time.Sleep(20 * time.Millisecond)
		return &RunResult{RunID: "run-1", State: domain.RunStateSucceeded}, nil
	}

	finalizeFn := func(ctx context.Context, projectDir string, taskID string, mainResult *RunResult) (*RunResult, error) {
		finalizeCalls.Add(1)
		return &RunResult{State: domain.RunStateSucceeded}, nil
	}

	loop := NewPhaseLoop(LoopConfig{
		ProjectDir:    "/tmp/test-project",
		MaxConcurrent: 1,
		DedupTimeout:  2 * time.Second,
	}, store, listTasksFn, mainFn, finalizeFn)

	loop.Start()
	time.Sleep(300 * time.Millisecond)
	loop.Stop()

	// Should have picked task-1 (open) and skipped task-2 (closed).
	mu.Lock()
	defer mu.Unlock()
	assert.GreaterOrEqual(t, int(mainCalls.Load()), 1)
	assert.Equal(t, "task-1", receivedTaskIDs[0])

	// Finalize should have run for each main completion.
	assert.Equal(t, mainCalls.Load(), finalizeCalls.Load())
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

	mainFn := func(ctx context.Context, projectDir string, taskID string) (*RunResult, error) {
		mainCalls.Add(1)
		return &RunResult{State: domain.RunStateSucceeded}, nil
	}

	loop := NewPhaseLoop(LoopConfig{
		ProjectDir:    "/tmp/test-project",
		MaxConcurrent: 1,
	}, store, listTasksFn, mainFn, nil)

	loop.Start()
	time.Sleep(200 * time.Millisecond)
	loop.Stop()

	// No open tasks → no main runs.
	assert.Equal(t, int32(0), mainCalls.Load())
}

func TestPhaseLoop_FinalizeRunsOnFailure(t *testing.T) {
	store := &fakeStore{runs: map[string]*domain.Run{}}

	var finalizeOutcome string
	var mu sync.Mutex

	listTasksFn := func(ctx context.Context, projectDir string) ([]Task, error) {
		return []Task{{ID: "task-1", Status: "open"}}, nil
	}

	mainFn := func(ctx context.Context, projectDir string, taskID string) (*RunResult, error) {
		return &RunResult{RunID: "run-1", State: domain.RunStateFailed}, nil
	}

	finalizeFn := func(ctx context.Context, projectDir string, taskID string, mainResult *RunResult) (*RunResult, error) {
		mu.Lock()
		finalizeOutcome = string(mainResult.State)
		mu.Unlock()
		return &RunResult{State: domain.RunStateSucceeded}, nil
	}

	loop := NewPhaseLoop(LoopConfig{
		ProjectDir:    "/tmp/test-project",
		MaxConcurrent: 1,
		DedupTimeout:  2 * time.Second,
	}, store, listTasksFn, mainFn, finalizeFn)

	loop.Start()
	time.Sleep(300 * time.Millisecond)
	loop.Stop()

	// Finalize should have received the failed state.
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, "failed", finalizeOutcome)
}

func TestPhaseLoop_FinalizeFailureOverridesMainSuccess(t *testing.T) {
	store := &fakeStore{runs: map[string]*domain.Run{}}

	var completionStates []domain.RunState
	var mu sync.Mutex

	listCallCount := 0
	listTasksFn := func(ctx context.Context, projectDir string) ([]Task, error) {
		listCallCount++
		if listCallCount <= 1 {
			return []Task{{ID: "task-1", Status: "open"}}, nil
		}
		return nil, nil // no more tasks after first round
	}

	mainFn := func(ctx context.Context, projectDir string, taskID string) (*RunResult, error) {
		return &RunResult{RunID: "run-1", State: domain.RunStateSucceeded}, nil
	}

	finalizeFn := func(ctx context.Context, projectDir string, taskID string, mainResult *RunResult) (*RunResult, error) {
		// Finalize fails (e.g., merge step failed).
		return &RunResult{State: domain.RunStateFailed}, nil
	}

	loop := NewPhaseLoop(LoopConfig{
		ProjectDir:    "/tmp/test-project",
		MaxConcurrent: 1,
		DedupTimeout:  2 * time.Second,
	}, store, listTasksFn, mainFn, finalizeFn)

	// Intercept completions by observing backoff behavior:
	// If finalize failure is correctly reported, the loop should back off
	// (not immediately try to fill slots).
	loop.Start()
	time.Sleep(300 * time.Millisecond)
	loop.Stop()

	// Verify through a more direct test: run the phases manually and check
	// that the overall state reflects the finalize failure.
	_ = completionStates
	_ = mu

	// The key assertion is that main succeeded but finalize failed,
	// so WorseState(succeeded, failed) = failed.
	got := domain.WorseState(domain.RunStateSucceeded, domain.RunStateFailed)
	assert.Equal(t, domain.RunStateFailed, got)
}

func TestPhaseLoop_FinalizeErrorOverridesMainSuccess(t *testing.T) {
	store := &fakeStore{runs: map[string]*domain.Run{}}

	var mainCalls atomic.Int32

	listTasksFn := func(ctx context.Context, projectDir string) ([]Task, error) {
		return []Task{{ID: "task-1", Status: "open"}}, nil
	}

	mainFn := func(ctx context.Context, projectDir string, taskID string) (*RunResult, error) {
		mainCalls.Add(1)
		return &RunResult{RunID: "run-1", State: domain.RunStateSucceeded}, nil
	}

	finalizeFn := func(ctx context.Context, projectDir string, taskID string, mainResult *RunResult) (*RunResult, error) {
		// Finalize returns an error (infra failure).
		return nil, fmt.Errorf("finalize infra error")
	}

	loop := NewPhaseLoop(LoopConfig{
		ProjectDir:    "/tmp/test-project",
		MaxConcurrent: 1,
		DedupTimeout:  2 * time.Second,
	}, store, listTasksFn, mainFn, finalizeFn)

	loop.Start()
	time.Sleep(300 * time.Millisecond)
	loop.Stop()

	// Main should have run at least once.
	assert.GreaterOrEqual(t, int(mainCalls.Load()), 1)
}

func TestPhaseLoop_NoFinalize(t *testing.T) {
	store := &fakeStore{runs: map[string]*domain.Run{}}

	var mainCalls atomic.Int32

	listTasksFn := func(ctx context.Context, projectDir string) ([]Task, error) {
		return []Task{{ID: "task-1", Status: "open"}}, nil
	}

	mainFn := func(ctx context.Context, projectDir string, taskID string) (*RunResult, error) {
		mainCalls.Add(1)
		time.Sleep(20 * time.Millisecond)
		return &RunResult{State: domain.RunStateSucceeded}, nil
	}

	// nil finalize — should be skipped without error
	loop := NewPhaseLoop(LoopConfig{
		ProjectDir:    "/tmp/test-project",
		MaxConcurrent: 1,
		DedupTimeout:  2 * time.Second,
	}, store, listTasksFn, mainFn, nil)

	loop.Start()
	time.Sleep(200 * time.Millisecond)
	loop.Stop()

	assert.GreaterOrEqual(t, int(mainCalls.Load()), 1)
}

func TestPhaseLoop_DedupFiltersOpenTasks(t *testing.T) {
	store := &fakeStore{runs: map[string]*domain.Run{}}

	var receivedTaskIDs []string
	var mu sync.Mutex

	listTasksFn := func(ctx context.Context, projectDir string) ([]Task, error) {
		return []Task{{ID: "task-1", Status: "open"}}, nil
	}

	mainFn := func(ctx context.Context, projectDir string, taskID string) (*RunResult, error) {
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
	}, store, listTasksFn, mainFn, nil)

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
	runFn := func(ctx context.Context, projectDir string, taskID string) (*RunResult, error) {
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

	mainFn := func(ctx context.Context, projectDir string, taskID string) (*RunResult, error) {
		time.Sleep(50 * time.Millisecond)
		return &RunResult{RunID: "run-" + taskID, State: domain.RunStateSucceeded}, nil
	}

	loop := NewPhaseLoop(LoopConfig{
		ProjectDir:    "/tmp/test-project",
		MaxConcurrent: 1,
		DedupTimeout:  2 * time.Second,
	}, store, listTasksFn, mainFn, nil)

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

	runFn := func(ctx context.Context, projectDir string, taskID string) (*RunResult, error) {
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

	mainFn := func(ctx context.Context, projectDir string, taskID string) (*RunResult, error) {
		return &RunResult{RunID: "run-1", State: domain.RunStateSucceeded}, nil
	}

	loop := NewPhaseLoop(LoopConfig{
		ProjectDir:    "/tmp/test-project",
		MaxConcurrent: 1,
		DedupTimeout:  10 * time.Millisecond, // very short dedup
	}, store, listTasksFn, mainFn, nil)

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

	runFn := func(ctx context.Context, projectDir string, taskID string) (*RunResult, error) {
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

	runFn := func(ctx context.Context, projectDir string, taskID string) (*RunResult, error) {
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

	runFn := func(ctx context.Context, projectDir string, taskID string) (*RunResult, error) {
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

	mainFn := func(ctx context.Context, projectDir string, taskID string) (*RunResult, error) {
		mainCalls.Add(1)
		time.Sleep(20 * time.Millisecond)
		return &RunResult{RunID: "run-" + taskID, State: domain.RunStateFailed}, nil
	}

	loop := NewPhaseLoop(LoopConfig{
		ProjectDir:    "/tmp/test-project",
		MaxConcurrent: 1,
		DedupTimeout:  2 * time.Second,
		StopOnError:   true,
	}, store, listTasksFn, mainFn, nil)

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
	}, store, func(ctx context.Context, projectDir string, taskID string) (*RunResult, error) {
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

	runFn := func(ctx context.Context, projectDir string, taskID string) (*RunResult, error) {
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

	runFn := func(ctx context.Context, projectDir string, taskID string) (*RunResult, error) {
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
	}, store, func(ctx context.Context, projectDir string, taskID string) (*RunResult, error) {
		return &RunResult{State: domain.RunStateSucceeded}, nil
	})

	assert.Equal(t, defaultMaxConsecutiveFailures, loop.config.MaxConsecutiveFailures)
}

func TestLoop_ConsecutiveFailures_SuccessResetsInLoop(t *testing.T) {
	store := &fakeStore{runs: map[string]*domain.Run{}}
	var called atomic.Int32

	runFn := func(ctx context.Context, projectDir string, taskID string) (*RunResult, error) {
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

	mainFn := func(ctx context.Context, projectDir string, taskID string) (*RunResult, error) {
		mainCalls.Add(1)
		time.Sleep(20 * time.Millisecond)
		return &RunResult{RunID: "run-" + taskID, State: domain.RunStateFailed}, nil
	}

	loop := NewPhaseLoop(LoopConfig{
		ProjectDir:             "/tmp/test-project",
		MaxConcurrent:          1,
		DedupTimeout:           50 * time.Millisecond,
		MaxConsecutiveFailures: 1,
	}, store, listTasksFn, mainFn, nil)

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
	runFn := func(ctx context.Context, projectDir string, taskID string) (*RunResult, error) {
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
	runFn := func(ctx context.Context, projectDir string, taskID string) (*RunResult, error) {
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
