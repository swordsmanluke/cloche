package sqlite_test

import (
	"context"
	"testing"

	"github.com/cloche-dev/cloche/internal/adapters/sqlite"
	"github.com/cloche-dev/cloche/internal/domain"
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
