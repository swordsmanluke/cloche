package orchestrator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/dsl"
	"github.com/cloche-dev/cloche/internal/ports"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockTracker implements ports.TaskTracker for testing.
type mockTracker struct {
	mu       sync.Mutex
	tasks    []ports.TrackerTask
	claimed  []string
	completed []string
	failed   []string
	claimErr error
}

func (m *mockTracker) ListReady(_ context.Context, project string) ([]ports.TrackerTask, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Return a copy
	result := make([]ports.TrackerTask, len(m.tasks))
	copy(result, m.tasks)
	return result, nil
}

func (m *mockTracker) Claim(_ context.Context, taskID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.claimErr != nil {
		return m.claimErr
	}
	m.claimed = append(m.claimed, taskID)
	return nil
}

func (m *mockTracker) Complete(_ context.Context, taskID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.completed = append(m.completed, taskID)
	return nil
}

func (m *mockTracker) Fail(_ context.Context, taskID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failed = append(m.failed, taskID)
	return nil
}

// mockPromptGen implements PromptGenerator for testing.
type mockPromptGen struct {
	response string
	err      error
	calls    int
}

func (m *mockPromptGen) Generate(_ context.Context, task ports.TrackerTask, projectDir string) (string, error) {
	m.calls++
	return m.response, m.err
}

func TestOrchestratorRun_DispatchesTasks(t *testing.T) {
	tracker := &mockTracker{
		tasks: []ports.TrackerTask{
			{ID: "task-1", Title: "First task", Priority: 2},
			{ID: "task-2", Title: "Second task", Priority: 1},
		},
	}
	promptGen := &mockPromptGen{response: "generated prompt"}

	var dispatched []string
	dispatch := func(ctx context.Context, workflow, projectDir, prompt string) (string, error) {
		dispatched = append(dispatched, prompt)
		return "run-" + fmt.Sprintf("%d", len(dispatched)), nil
	}

	orch := New(promptGen, dispatch)
	orch.Register(&ProjectConfig{
		Dir:         "/test/project",
		Workflow:    "develop",
		Concurrency: 2,
		Tracker:     tracker,
	})

	n, err := orch.Run(context.Background(), "/test/project")
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	assert.Equal(t, 2, len(dispatched))
	assert.Equal(t, []string{"task-1", "task-2"}, tracker.claimed)
	assert.Equal(t, 2, promptGen.calls)
}

func TestOrchestratorRun_RespectsConncurrencyLimit(t *testing.T) {
	tracker := &mockTracker{
		tasks: []ports.TrackerTask{
			{ID: "task-1", Title: "First", Priority: 2},
			{ID: "task-2", Title: "Second", Priority: 1},
		},
	}
	promptGen := &mockPromptGen{response: "prompt"}

	dispatchCount := 0
	dispatch := func(ctx context.Context, workflow, projectDir, prompt string) (string, error) {
		dispatchCount++
		return fmt.Sprintf("run-%d", dispatchCount), nil
	}

	orch := New(promptGen, dispatch)
	orch.Register(&ProjectConfig{
		Dir:         "/test/project",
		Workflow:    "develop",
		Concurrency: 1, // only 1 slot
		Tracker:     tracker,
	})

	n, err := orch.Run(context.Background(), "/test/project")
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.Equal(t, 1, dispatchCount)
	assert.Equal(t, []string{"task-1"}, tracker.claimed) // highest priority
}

func TestOrchestratorRun_NoAvailableSlots(t *testing.T) {
	tracker := &mockTracker{
		tasks: []ports.TrackerTask{
			{ID: "task-1", Title: "Task", Priority: 1},
		},
	}
	promptGen := &mockPromptGen{response: "prompt"}
	dispatch := func(ctx context.Context, workflow, projectDir, prompt string) (string, error) {
		return "run-1", nil
	}

	orch := New(promptGen, dispatch)
	orch.Register(&ProjectConfig{
		Dir:         "/test/project",
		Workflow:    "develop",
		Concurrency: 1,
		Tracker:     tracker,
	})

	// Simulate an in-flight run
	orch.mu.Lock()
	orch.inFlight["/test/project"] = 1
	orch.mu.Unlock()

	n, err := orch.Run(context.Background(), "/test/project")
	require.NoError(t, err)
	assert.Equal(t, 0, n)
}

func TestOrchestratorRun_NoReadyTasks(t *testing.T) {
	tracker := &mockTracker{tasks: nil}
	promptGen := &mockPromptGen{response: "prompt"}
	dispatch := func(ctx context.Context, workflow, projectDir, prompt string) (string, error) {
		t.Fatal("dispatch should not be called")
		return "", nil
	}

	orch := New(promptGen, dispatch)
	orch.Register(&ProjectConfig{
		Dir:         "/test/project",
		Workflow:    "develop",
		Concurrency: 1,
		Tracker:     tracker,
	})

	n, err := orch.Run(context.Background(), "/test/project")
	require.NoError(t, err)
	assert.Equal(t, 0, n)
}

func TestOrchestratorRun_UnregisteredProject(t *testing.T) {
	orch := New(nil, nil)
	_, err := orch.Run(context.Background(), "/unknown")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not registered")
}

func TestOrchestratorRun_PromptGenerationFailure(t *testing.T) {
	tracker := &mockTracker{
		tasks: []ports.TrackerTask{
			{ID: "task-1", Title: "Task", Priority: 1},
		},
	}
	promptGen := &mockPromptGen{err: fmt.Errorf("LLM unavailable")}
	dispatch := func(ctx context.Context, workflow, projectDir, prompt string) (string, error) {
		t.Fatal("dispatch should not be called on prompt failure")
		return "", nil
	}

	orch := New(promptGen, dispatch)
	orch.Register(&ProjectConfig{
		Dir:         "/test/project",
		Workflow:    "develop",
		Concurrency: 1,
		Tracker:     tracker,
	})

	n, err := orch.Run(context.Background(), "/test/project")
	require.NoError(t, err) // errors are logged, not returned
	assert.Equal(t, 0, n)
	assert.Equal(t, []string{"task-1"}, tracker.claimed)
	assert.Equal(t, []string{"task-1"}, tracker.failed) // task should be failed back
}

func TestOrchestratorRun_DispatchFailure(t *testing.T) {
	tracker := &mockTracker{
		tasks: []ports.TrackerTask{
			{ID: "task-1", Title: "Task", Priority: 1},
		},
	}
	promptGen := &mockPromptGen{response: "prompt"}
	dispatch := func(ctx context.Context, workflow, projectDir, prompt string) (string, error) {
		return "", fmt.Errorf("container start failed")
	}

	orch := New(promptGen, dispatch)
	orch.Register(&ProjectConfig{
		Dir:         "/test/project",
		Workflow:    "develop",
		Concurrency: 1,
		Tracker:     tracker,
	})

	n, err := orch.Run(context.Background(), "/test/project")
	require.NoError(t, err)
	assert.Equal(t, 0, n)
	assert.Equal(t, []string{"task-1"}, tracker.failed)
}

func TestOrchestratorRun_ClaimFailure(t *testing.T) {
	tracker := &mockTracker{
		tasks: []ports.TrackerTask{
			{ID: "task-1", Title: "Task", Priority: 1},
		},
		claimErr: fmt.Errorf("already claimed"),
	}
	promptGen := &mockPromptGen{response: "prompt"}
	dispatch := func(ctx context.Context, workflow, projectDir, prompt string) (string, error) {
		t.Fatal("dispatch should not be called on claim failure")
		return "", nil
	}

	orch := New(promptGen, dispatch)
	orch.Register(&ProjectConfig{
		Dir:         "/test/project",
		Workflow:    "develop",
		Concurrency: 1,
		Tracker:     tracker,
	})

	n, err := orch.Run(context.Background(), "/test/project")
	require.NoError(t, err)
	assert.Equal(t, 0, n)
}

func TestOrchestratorTriggerAll(t *testing.T) {
	tracker1 := &mockTracker{
		tasks: []ports.TrackerTask{{ID: "t1", Title: "T1", Priority: 1}},
	}
	tracker2 := &mockTracker{
		tasks: []ports.TrackerTask{{ID: "t2", Title: "T2", Priority: 1}},
	}
	promptGen := &mockPromptGen{response: "prompt"}

	dispatchCount := 0
	dispatch := func(ctx context.Context, workflow, projectDir, prompt string) (string, error) {
		dispatchCount++
		return fmt.Sprintf("run-%d", dispatchCount), nil
	}

	orch := New(promptGen, dispatch)
	orch.Register(&ProjectConfig{Dir: "/proj1", Workflow: "dev", Concurrency: 1, Tracker: tracker1})
	orch.Register(&ProjectConfig{Dir: "/proj2", Workflow: "dev", Concurrency: 1, Tracker: tracker2})

	total := orch.TriggerAll(context.Background())
	assert.Equal(t, 2, total)
	assert.Equal(t, 2, dispatchCount)
}

func TestOrchestratorOnRunComplete(t *testing.T) {
	tracker := &mockTracker{
		tasks: []ports.TrackerTask{
			{ID: "task-1", Title: "Task", Priority: 1},
		},
	}
	promptGen := &mockPromptGen{response: "prompt"}

	dispatchCount := 0
	dispatch := func(ctx context.Context, workflow, projectDir, prompt string) (string, error) {
		dispatchCount++
		return fmt.Sprintf("run-%d", dispatchCount), nil
	}

	orch := New(promptGen, dispatch)
	orch.Register(&ProjectConfig{
		Dir:         "/test/project",
		Workflow:    "develop",
		Concurrency: 1,
		Tracker:     tracker,
	})

	// Simulate a run is in flight (fills the concurrency slot)
	orch.mu.Lock()
	orch.inFlight["/test/project"] = 1
	orch.trackTask("/test/project", "existing-run", "existing-task")
	orch.mu.Unlock()

	// OnRunComplete should decrement in-flight and trigger new dispatch
	orch.OnRunComplete(context.Background(), "/test/project", "existing-run", domain.RunStateSucceeded)

	// After OnRunComplete: 1 → 0 (decrement) → 1 (new run dispatched)
	assert.Equal(t, 1, orch.InFlight("/test/project"))
	assert.Equal(t, 1, dispatchCount) // should have dispatched the waiting task
}

func TestOrchestratorOnRunComplete_ClosesTask(t *testing.T) {
	tracker := &mockTracker{tasks: nil} // no more ready tasks
	promptGen := &mockPromptGen{response: "prompt"}
	dispatch := func(ctx context.Context, workflow, projectDir, prompt string) (string, error) {
		return "run-1", nil
	}

	orch := New(promptGen, dispatch)
	orch.Register(&ProjectConfig{
		Dir:         "/test/project",
		Workflow:    "develop",
		Concurrency: 1,
		Tracker:     tracker,
	})

	// Simulate a run for task-1 in flight
	orch.mu.Lock()
	orch.inFlight["/test/project"] = 1
	orch.trackTask("/test/project", "run-1", "task-1")
	orch.mu.Unlock()

	// Run succeeds → task should be completed
	orch.OnRunComplete(context.Background(), "/test/project", "run-1", domain.RunStateSucceeded)
	assert.Equal(t, []string{"task-1"}, tracker.completed)
	assert.Empty(t, tracker.failed)
}

func TestOrchestratorOnRunComplete_FailsTask(t *testing.T) {
	tracker := &mockTracker{tasks: nil}
	promptGen := &mockPromptGen{response: "prompt"}
	dispatch := func(ctx context.Context, workflow, projectDir, prompt string) (string, error) {
		return "run-1", nil
	}

	orch := New(promptGen, dispatch)
	orch.Register(&ProjectConfig{
		Dir:         "/test/project",
		Workflow:    "develop",
		Concurrency: 1,
		Tracker:     tracker,
	})

	// Simulate a run for task-1 in flight
	orch.mu.Lock()
	orch.inFlight["/test/project"] = 1
	orch.trackTask("/test/project", "run-1", "task-1")
	orch.mu.Unlock()

	// Run fails → task should be failed
	orch.OnRunComplete(context.Background(), "/test/project", "run-1", domain.RunStateFailed)
	assert.Equal(t, []string{"task-1"}, tracker.failed)
	assert.Empty(t, tracker.completed)
}

func TestOrchestratorRun_SkipsDuplicateTasks(t *testing.T) {
	tracker := &mockTracker{
		tasks: []ports.TrackerTask{
			{ID: "task-1", Title: "Already running", Priority: 2},
			{ID: "task-2", Title: "New task", Priority: 1},
		},
	}
	promptGen := &mockPromptGen{response: "prompt"}

	var dispatched []string
	dispatch := func(ctx context.Context, workflow, projectDir, prompt string) (string, error) {
		dispatched = append(dispatched, fmt.Sprintf("run-%d", len(dispatched)+1))
		return dispatched[len(dispatched)-1], nil
	}

	orch := New(promptGen, dispatch)
	orch.Register(&ProjectConfig{
		Dir:         "/test/project",
		Workflow:    "develop",
		Concurrency: 2,
		Tracker:     tracker,
	})

	// Simulate task-1 already in flight
	orch.mu.Lock()
	orch.inFlight["/test/project"] = 1
	orch.trackTask("/test/project", "existing-run", "task-1")
	orch.mu.Unlock()

	n, err := orch.Run(context.Background(), "/test/project")
	require.NoError(t, err)
	assert.Equal(t, 1, n) // only task-2 should be dispatched
	assert.Equal(t, []string{"task-2"}, tracker.claimed) // task-1 skipped
}

func TestOrchestratorInFlightTracking(t *testing.T) {
	tracker := &mockTracker{
		tasks: []ports.TrackerTask{
			{ID: "task-1", Title: "Task", Priority: 1},
		},
	}
	promptGen := &mockPromptGen{response: "prompt"}
	dispatch := func(ctx context.Context, workflow, projectDir, prompt string) (string, error) {
		return "run-1", nil
	}

	orch := New(promptGen, dispatch)
	orch.Register(&ProjectConfig{
		Dir:         "/test/project",
		Workflow:    "develop",
		Concurrency: 2,
		Tracker:     tracker,
	})

	assert.Equal(t, 0, orch.InFlight("/test/project"))

	_, err := orch.Run(context.Background(), "/test/project")
	require.NoError(t, err)

	assert.Equal(t, 1, orch.InFlight("/test/project"))
}

func TestOrchestratorRun_HostWorkflowPath(t *testing.T) {
	tmpDir := t.TempDir()
	clocheDir := filepath.Join(tmpDir, ".cloche")
	require.NoError(t, os.MkdirAll(clocheDir, 0755))

	// Write a host.cloche file
	hostCloche := `workflow "orchestrate" {
  step prep {
    run     = "echo hello"
    results = [success, fail]
  }

  prep:success -> done
  prep:fail    -> abort
}
`
	require.NoError(t, os.WriteFile(filepath.Join(clocheDir, "host.cloche"), []byte(hostCloche), 0644))

	tracker := &mockTracker{
		tasks: []ports.TrackerTask{
			{ID: "task-1", Title: "Test Task", Description: "task body"},
		},
	}
	promptGen := &mockPromptGen{response: "should not be called"}

	dispatchCalled := false
	dispatch := func(ctx context.Context, workflow, projectDir, prompt string) (string, error) {
		dispatchCalled = true
		return "run-1", nil
	}

	parseFunc := func(input string) (*domain.Workflow, error) {
		return dsl.ParseForHost(input)
	}

	waiter := &mockRunWaiter{state: domain.RunStateSucceeded}
	hostRunner := &HostRunner{
		Dispatch: dispatch,
		WaitRun:  waiter,
	}

	orch := New(promptGen, dispatch,
		WithHostRunner(hostRunner),
		WithParseHostWorkflow(parseFunc),
	)
	orch.Register(&ProjectConfig{
		Dir:         tmpDir,
		Workflow:    "develop",
		Concurrency: 1,
		Tracker:     tracker,
	})

	n, err := orch.Run(context.Background(), tmpDir)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.Equal(t, []string{"task-1"}, tracker.claimed)
	// promptGen should NOT have been called (host workflow path used instead)
	assert.Equal(t, 0, promptGen.calls)
	// dispatch should NOT have been called directly (host runner handles it)
	assert.False(t, dispatchCalled)

	// Wait for the background goroutine to finish so TempDir cleanup succeeds.
	require.Eventually(t, func() bool {
		tracker.mu.Lock()
		defer tracker.mu.Unlock()
		return len(tracker.completed) > 0
	}, time.Second, 10*time.Millisecond)
}

func TestOrchestratorRun_FallbackWithoutHostCloche(t *testing.T) {
	tmpDir := t.TempDir()
	// No host.cloche file — should fall back to promptGen → dispatch path

	tracker := &mockTracker{
		tasks: []ports.TrackerTask{
			{ID: "task-1", Title: "Test Task"},
		},
	}
	promptGen := &mockPromptGen{response: "generated prompt"}

	dispatched := false
	dispatch := func(ctx context.Context, workflow, projectDir, prompt string) (string, error) {
		dispatched = true
		return "run-1", nil
	}

	waiter := &mockRunWaiter{state: domain.RunStateSucceeded}
	hostRunner := &HostRunner{
		Dispatch: dispatch,
		WaitRun:  waiter,
	}

	parseFunc := func(input string) (*domain.Workflow, error) {
		return dsl.ParseForHost(input)
	}

	orch := New(promptGen, dispatch,
		WithHostRunner(hostRunner),
		WithParseHostWorkflow(parseFunc),
	)
	orch.Register(&ProjectConfig{
		Dir:         tmpDir,
		Workflow:    "develop",
		Concurrency: 1,
		Tracker:     tracker,
	})

	n, err := orch.Run(context.Background(), tmpDir)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.Equal(t, 1, promptGen.calls)
	assert.True(t, dispatched)
}

func TestOrchestratorRun_UsesCorrectWorkflow(t *testing.T) {
	tracker := &mockTracker{
		tasks: []ports.TrackerTask{
			{ID: "task-1", Title: "Task", Priority: 1},
		},
	}
	promptGen := &mockPromptGen{response: "prompt"}

	var capturedWorkflow string
	dispatch := func(ctx context.Context, workflow, projectDir, prompt string) (string, error) {
		capturedWorkflow = workflow
		return "run-1", nil
	}

	orch := New(promptGen, dispatch)
	orch.Register(&ProjectConfig{
		Dir:         "/test/project",
		Workflow:    "custom-workflow",
		Concurrency: 1,
		Tracker:     tracker,
	})

	_, err := orch.Run(context.Background(), "/test/project")
	require.NoError(t, err)
	assert.Equal(t, "custom-workflow", capturedWorkflow)
}
