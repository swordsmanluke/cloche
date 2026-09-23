package sqlite_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swordsmanluke/cloche/internal/adapters/sqlite"
	"github.com/swordsmanluke/cloche/internal/domain"
	"github.com/swordsmanluke/cloche/internal/ports"
)

// A project is nothing but its records, so purging one must take every
// dependent row with it — and nothing belonging to a neighbouring project.
func TestPurgeProject_RemovesEverythingForProjectOnly(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()
	ctx := context.Background()

	seed := func(suffix, dir string) {
		task := &domain.Task{ID: "task-" + suffix, Title: "t", ProjectDir: dir, CreatedAt: time.Now()}
		require.NoError(t, store.SaveTask(ctx, task))
		att := &domain.Attempt{ID: "att-" + suffix, TaskID: task.ID, StartedAt: time.Now()}
		require.NoError(t, store.SaveAttempt(ctx, att))
		run := domain.NewRun("run-"+suffix, "develop")
		run.ProjectDir = dir
		run.TaskID = task.ID
		run.AttemptID = att.ID
		run.State = domain.RunStateSucceeded
		require.NoError(t, store.CreateRun(ctx, run))
		require.NoError(t, store.SaveCapture(ctx, run.ID, &domain.StepExecution{StepName: "implement", Result: "success", StartedAt: time.Now(), CompletedAt: time.Now()}))
		require.NoError(t, store.SetContextKey(ctx, task.ID, att.ID, run.ID, "k", "v"))
	}
	seed("a", "/proj/alpha")
	seed("b", "/proj/beta")

	var purger ports.ProjectPurger = store
	deleted, err := purger.PurgeProject(ctx, "/proj/alpha")
	require.NoError(t, err)
	assert.EqualValues(t, 1, deleted)

	projects, err := store.ListProjects(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"/proj/beta"}, projects)

	_, err = store.GetRun(ctx, "run-a")
	assert.Error(t, err, "alpha's run must be gone")
	caps, err := store.GetCaptures(ctx, "run-a")
	require.NoError(t, err)
	assert.Empty(t, caps, "alpha's step executions must be gone")
	_, err = store.GetTask(ctx, "task-a")
	assert.Error(t, err, "alpha's task must be gone")
	_, err = store.GetAttempt(ctx, "att-a")
	assert.Error(t, err, "alpha's attempt must be gone")
	_, found, err := store.GetContextKey(ctx, "task-a", "att-a", "run-a", "k")
	require.NoError(t, err)
	assert.False(t, found, "alpha's KV must be gone")

	// Beta is intact, dependents included.
	_, err = store.GetRun(ctx, "run-b")
	require.NoError(t, err)
	caps, err = store.GetCaptures(ctx, "run-b")
	require.NoError(t, err)
	assert.Len(t, caps, 1, "beta's step executions must survive")
	_, err = store.GetTask(ctx, "task-b")
	require.NoError(t, err)
	_, found, err = store.GetContextKey(ctx, "task-b", "att-b", "run-b", "k")
	require.NoError(t, err)
	assert.True(t, found)
}

func TestPurgeProject_UnknownProjectIsNoOp(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	deleted, err := store.PurgeProject(context.Background(), "/proj/nope")
	require.NoError(t, err)
	assert.Zero(t, deleted)
}
