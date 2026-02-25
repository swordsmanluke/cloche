package sqlite_test

import (
	"context"
	"testing"
	"time"

	"github.com/cloche-dev/cloche/internal/adapters/sqlite"
	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/ports"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunStore_CreateAndGet(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	run := domain.NewRun("run-1", "test-workflow")
	run.Start()

	err = store.CreateRun(ctx, run)
	require.NoError(t, err)

	got, err := store.GetRun(ctx, "run-1")
	require.NoError(t, err)
	assert.Equal(t, "run-1", got.ID)
	assert.Equal(t, "test-workflow", got.WorkflowName)
	assert.Equal(t, domain.RunStateRunning, got.State)
}

func TestRunStore_Update(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	run := domain.NewRun("run-1", "test-workflow")
	run.Start()
	require.NoError(t, store.CreateRun(ctx, run))

	run.Complete(domain.RunStateSucceeded)
	require.NoError(t, store.UpdateRun(ctx, run))

	got, err := store.GetRun(ctx, "run-1")
	require.NoError(t, err)
	assert.Equal(t, domain.RunStateSucceeded, got.State)
}

func TestRunStore_List(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	run1 := domain.NewRun("run-1", "wf-a")
	run2 := domain.NewRun("run-2", "wf-b")
	require.NoError(t, store.CreateRun(ctx, run1))
	require.NoError(t, store.CreateRun(ctx, run2))

	runs, err := store.ListRuns(ctx)
	require.NoError(t, err)
	assert.Len(t, runs, 2)
}

func TestRunStore_GetNotFound(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	_, err = store.GetRun(context.Background(), "nonexistent")
	assert.Error(t, err)
}

func TestStore_DeleteRun(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	// Create a run and save a capture for it
	run := domain.NewRun("del-1", "test-workflow")
	run.Start()
	require.NoError(t, store.CreateRun(ctx, run))
	require.NoError(t, store.SaveCapture(ctx, "del-1", &domain.StepExecution{
		StepName:  "step1",
		StartedAt: time.Now(),
	}))

	// Delete the run
	err = store.DeleteRun(ctx, "del-1")
	require.NoError(t, err)

	// Verify run is gone
	_, err = store.GetRun(ctx, "del-1")
	assert.Error(t, err)

	// Verify captures are gone
	caps, err := store.GetCaptures(ctx, "del-1")
	require.NoError(t, err)
	assert.Empty(t, caps)
}

func TestRunProjectDir(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	run := domain.NewRun("test-1", "develop")
	run.ProjectDir = "/home/user/project"
	run.Start()

	err = store.CreateRun(ctx, run)
	require.NoError(t, err)

	got, err := store.GetRun(ctx, "test-1")
	require.NoError(t, err)
	assert.Equal(t, "/home/user/project", got.ProjectDir)
}

func TestCaptureWithPromptAndOutput(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	run := domain.NewRun("test-1", "develop")
	run.Start()
	store.CreateRun(ctx, run)

	exec := &domain.StepExecution{
		StepName:      "implement",
		PromptText:    "Write hello world",
		AgentOutput:   "Here is the code",
		AttemptNumber: 1,
		StartedAt:     time.Now(),
	}
	err = store.SaveCapture(ctx, "test-1", exec)
	require.NoError(t, err)

	caps, err := store.GetCaptures(ctx, "test-1")
	require.NoError(t, err)
	require.Len(t, caps, 1)
	assert.Equal(t, "Write hello world", caps[0].PromptText)
	assert.Equal(t, "Here is the code", caps[0].AgentOutput)
	assert.Equal(t, 1, caps[0].AttemptNumber)
}

func TestListRunsSince(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	for i, id := range []string{"run-1", "run-2", "run-3"} {
		r := domain.NewRun(id, "develop")
		r.ProjectDir = "/project"
		r.StartedAt = time.Now().Add(time.Duration(i) * time.Minute)
		r.State = domain.RunStateRunning
		require.NoError(t, store.CreateRun(ctx, r))
	}

	runs, err := store.ListRunsSince(ctx, "/project", "develop", "run-1")
	require.NoError(t, err)
	assert.Len(t, runs, 2)
	assert.Equal(t, "run-2", runs[0].ID)
	assert.Equal(t, "run-3", runs[1].ID)
}

func TestListRunsSinceEmpty(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	for i, id := range []string{"run-1", "run-2"} {
		r := domain.NewRun(id, "develop")
		r.ProjectDir = "/project"
		r.StartedAt = time.Now().Add(time.Duration(i) * time.Minute)
		r.State = domain.RunStateRunning
		require.NoError(t, store.CreateRun(ctx, r))
	}

	// Empty sinceRunID returns all
	runs, err := store.ListRunsSince(ctx, "/project", "develop", "")
	require.NoError(t, err)
	assert.Len(t, runs, 2)
}

func TestGetLastEvolution(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	// No evolution yet
	entry, err := store.GetLastEvolution(ctx, "/project", "develop")
	require.NoError(t, err)
	assert.Nil(t, entry)

	// Save one
	require.NoError(t, store.SaveEvolution(ctx, &ports.EvolutionEntry{
		ID:           "evo-1",
		ProjectDir:   "/project",
		WorkflowName: "develop",
		TriggerRunID: "run-1",
		CreatedAt:    time.Now(),
		ChangesJSON:  "[]",
	}))

	entry, err = store.GetLastEvolution(ctx, "/project", "develop")
	require.NoError(t, err)
	require.NotNil(t, entry)
	assert.Equal(t, "evo-1", entry.ID)
}
