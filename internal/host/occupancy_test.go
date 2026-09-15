package host

import (
	"context"
	"testing"
	"time"

	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/ports"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// occupancyStore extends fakeStore with a working ListRunsByProject, which
// fakeStore itself stubs out to (nil, nil) for tests that don't need it.
type occupancyStore struct {
	*fakeStore
	projectRuns map[string][]*domain.Run
}

func (s *occupancyStore) ListRunsByProject(_ context.Context, projectDir string, _ time.Time) ([]*domain.Run, error) {
	return s.projectRuns[projectDir], nil
}

// fakePollStore is a minimal in-memory ports.PollStore for tests.
type fakePollStore struct {
	records map[string][]*ports.PollRecord // run ID -> records
}

func (f *fakePollStore) UpsertPoll(_ context.Context, record *ports.PollRecord) error {
	return nil
}
func (f *fakePollStore) GetPoll(_ context.Context, runID, stepName string) (*ports.PollRecord, error) {
	return nil, nil
}
func (f *fakePollStore) DeletePoll(_ context.Context, runID, stepName string) error {
	return nil
}
func (f *fakePollStore) ListPolls(_ context.Context, runID string) ([]*ports.PollRecord, error) {
	return f.records[runID], nil
}

func noopListTasks(ctx context.Context, projectDir string) ([]Task, error) { return nil, nil }
func noopMain(ctx context.Context, projectDir, taskID, taskTitle, attemptID string) (*RunResult, error) {
	return nil, nil
}

// TestLoop_Occupancy verifies that a loop reports busy slots (from in-flight
// host runs), capacity-queued tasks (open but not yet assigned), and
// asynchronously-driven polls (parked runs, not holding a slot) — the data
// backing the GetLoopOccupancy RPC / GET .../loop/occupancy endpoint.
func TestLoop_Occupancy(t *testing.T) {
	now := time.Now()
	runningRun := &domain.Run{
		ID: "run-1", ProjectDir: "/proj", IsHost: true, State: domain.RunStateRunning,
		TaskID: "task-1", AttemptID: "attempt-1", ActiveSteps: []string{"build"},
		StartedAt: now.Add(-time.Minute),
	}
	waitingRun := &domain.Run{
		ID: "run-2", ProjectDir: "/proj", IsHost: true, State: domain.RunStateWaiting,
		TaskID: "task-2", StartedAt: now.Add(-2 * time.Minute),
	}

	store := &occupancyStore{
		fakeStore: &fakeStore{runs: map[string]*domain.Run{
			runningRun.ID: runningRun,
			waitingRun.ID: waitingRun,
		}},
		projectRuns: map[string][]*domain.Run{
			"/proj": {runningRun, waitingRun},
		},
	}

	pollStore := &fakePollStore{records: map[string][]*ports.PollRecord{
		waitingRun.ID: {{RunID: waitingRun.ID, StepName: "check", LastPollAt: now, PollCount: 3}},
	}}

	loop := NewPhaseLoop(LoopConfig{
		ProjectDir:    "/proj",
		MaxConcurrent: 3,
		DedupTimeout:  time.Minute,
	}, store, noopListTasks, noopMain)
	loop.SetPollStore(pollStore)

	// Simulate an open, unassigned task discovered by the loop but not yet
	// launched because the loop hasn't gotten to it (capacity-queued).
	loop.tasksMu.Lock()
	loop.lastTasks = []Task{{ID: "task-3", Status: "open"}}
	loop.tasksMu.Unlock()

	occ := loop.Occupancy(context.Background())

	require.Equal(t, 3, occ.MaxConcurrency)

	require.Len(t, occ.Slots, 1)
	assert.Equal(t, 0, occ.Slots[0].Index)
	assert.Equal(t, "run-1", occ.Slots[0].RunID)
	assert.Equal(t, "task-1", occ.Slots[0].TaskID)
	assert.Equal(t, "attempt-1", occ.Slots[0].AttemptID)
	assert.Equal(t, "build", occ.Slots[0].CurrentStep)
	assert.WithinDuration(t, runningRun.StartedAt, occ.Slots[0].StartedAt, time.Second)

	require.Len(t, occ.Queued, 1)
	assert.Equal(t, "task-3", occ.Queued[0].TaskID)
	assert.Equal(t, "capacity", occ.Queued[0].Reason)
	assert.False(t, occ.Queued[0].Since.IsZero())

	require.Len(t, occ.Polls, 1)
	assert.Equal(t, "run-2", occ.Polls[0].RunID)
	assert.Equal(t, "check", occ.Polls[0].Step)
	assert.Equal(t, 3, occ.Polls[0].PollCount)
}

// TestLoop_QueuedTasks_ResumingPoll verifies that a run parked at a poll step
// which has finished polling and is waiting to reacquire its concurrency
// slot shows up as "queued" (reason "resuming"), scoped to its own project.
func TestLoop_QueuedTasks_ResumingPoll(t *testing.T) {
	store := &fakeStore{runs: map[string]*domain.Run{
		"run-1": {ID: "run-1", ProjectDir: "/proj"},
		"run-2": {ID: "run-2", ProjectDir: "/other"},
	}}
	coord := NewPollCoordinator()

	loop := NewPhaseLoop(LoopConfig{
		ProjectDir:    "/proj",
		MaxConcurrent: 1,
		DedupTimeout:  time.Minute,
	}, store, noopListTasks, noopMain)
	loop.SetPollCoordinator(coord)

	done1 := make(chan struct{})
	done2 := make(chan struct{})
	go func() { coord.ReacquireSlot("run-1", "poll-step"); close(done1) }()
	go func() { coord.ReacquireSlot("run-2", "poll-step"); close(done2) }()

	require.Eventually(t, func() bool { return coord.PendingResumeCount() == 2 }, time.Second, 10*time.Millisecond)

	queued := loop.QueuedTasks(context.Background())
	require.Len(t, queued, 1, "only the resume for this loop's own project should be reported")
	assert.Equal(t, "run-1", queued[0].RunID)
	assert.Equal(t, "resuming", queued[0].Reason)

	// Drain the queue so the goroutines above don't leak past the test.
	require.True(t, coord.grantNextResume())
	require.True(t, coord.grantNextResume())
	<-done1
	<-done2
}
