package sqlite_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swordsmanluke/cloche/internal/activitylog"
	"github.com/swordsmanluke/cloche/internal/adapters/sqlite"
)

func TestActivityStore_AppendAndRead(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	projectDir := "/proj/test"

	now := time.Now().UTC().Truncate(time.Second)

	entries := []activitylog.Entry{
		{Timestamp: now, Kind: activitylog.KindAttemptStarted, TaskID: "TASK-1", AttemptID: "a1b2"},
		{Timestamp: now.Add(time.Second), Kind: activitylog.KindStepStarted, TaskID: "TASK-1", AttemptID: "a1b2", WorkflowName: "main", StepName: "implement"},
		{Timestamp: now.Add(2 * time.Second), Kind: activitylog.KindStepCompleted, TaskID: "TASK-1", AttemptID: "a1b2", WorkflowName: "main", StepName: "implement", Result: "success"},
		{Timestamp: now.Add(3 * time.Second), Kind: activitylog.KindAttemptEnded, TaskID: "TASK-1", AttemptID: "a1b2", State: "succeeded"},
	}

	for _, e := range entries {
		require.NoError(t, store.AppendActivityEntry(ctx, projectDir, e))
	}

	got, err := store.ReadActivityEntries(ctx, projectDir, activitylog.ReadOptions{})
	require.NoError(t, err)
	require.Len(t, got, len(entries))

	for i, e := range entries {
		assert.Equal(t, e.Kind, got[i].Kind)
		assert.Equal(t, e.TaskID, got[i].TaskID)
		assert.Equal(t, e.AttemptID, got[i].AttemptID)
		assert.Equal(t, e.WorkflowName, got[i].WorkflowName)
		assert.Equal(t, e.StepName, got[i].StepName)
		assert.Equal(t, e.Result, got[i].Result)
		assert.Equal(t, e.State, got[i].State)
		assert.WithinDuration(t, e.Timestamp, got[i].Timestamp, time.Second)
	}
}

func TestActivityStore_ReadEmptyProject(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	got, err := store.ReadActivityEntries(context.Background(), "/no/such/project", activitylog.ReadOptions{})
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestActivityStore_ReadTimeFilter(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	projectDir := "/proj/filter"
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	all := []activitylog.Entry{
		{Timestamp: base, Kind: activitylog.KindAttemptStarted, AttemptID: "a1"},
		{Timestamp: base.Add(10 * time.Minute), Kind: activitylog.KindStepStarted, AttemptID: "a1", StepName: "s1"},
		{Timestamp: base.Add(20 * time.Minute), Kind: activitylog.KindStepCompleted, AttemptID: "a1", StepName: "s1"},
		{Timestamp: base.Add(30 * time.Minute), Kind: activitylog.KindAttemptEnded, AttemptID: "a1"},
	}
	for _, e := range all {
		require.NoError(t, store.AppendActivityEntry(ctx, projectDir, e))
	}

	// Since filter: exclude entries before base+5m
	since := base.Add(5 * time.Minute)
	got, err := store.ReadActivityEntries(ctx, projectDir, activitylog.ReadOptions{Since: since})
	require.NoError(t, err)
	assert.Len(t, got, 3) // entries at +10m, +20m, +30m

	// Until filter: exclude entries after base+15m
	until := base.Add(15 * time.Minute)
	got, err = store.ReadActivityEntries(ctx, projectDir, activitylog.ReadOptions{Until: until})
	require.NoError(t, err)
	assert.Len(t, got, 2) // entries at 0m and +10m

	// Both filters
	got, err = store.ReadActivityEntries(ctx, projectDir, activitylog.ReadOptions{Since: since, Until: until})
	require.NoError(t, err)
	assert.Len(t, got, 1) // only +10m
	assert.Equal(t, activitylog.KindStepStarted, got[0].Kind)
}

func TestActivityStore_ProjectIsolation(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	require.NoError(t, store.AppendActivityEntry(ctx, "/proj/a", activitylog.Entry{
		Kind: activitylog.KindAttemptStarted, AttemptID: "a1",
		Timestamp: time.Now(),
	}))
	require.NoError(t, store.AppendActivityEntry(ctx, "/proj/b", activitylog.Entry{
		Kind: activitylog.KindAttemptStarted, AttemptID: "b1",
		Timestamp: time.Now(),
	}))

	gotA, err := store.ReadActivityEntries(ctx, "/proj/a", activitylog.ReadOptions{})
	require.NoError(t, err)
	require.Len(t, gotA, 1)
	assert.Equal(t, "a1", gotA[0].AttemptID)

	gotB, err := store.ReadActivityEntries(ctx, "/proj/b", activitylog.ReadOptions{})
	require.NoError(t, err)
	require.Len(t, gotB, 1)
	assert.Equal(t, "b1", gotB[0].AttemptID)
}

func TestActivityStore_AllProjects(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	require.NoError(t, store.AppendActivityEntry(ctx, "/proj/a", activitylog.Entry{
		Kind: activitylog.KindAttemptStarted, AttemptID: "a1", Timestamp: time.Now(),
	}))
	require.NoError(t, store.AppendActivityEntry(ctx, "/proj/b", activitylog.Entry{
		Kind: activitylog.KindAttemptStarted, AttemptID: "b1", Timestamp: time.Now(),
	}))

	got, err := store.ReadActivityEntries(ctx, "", activitylog.ReadOptions{})
	require.NoError(t, err)
	require.Len(t, got, 2)
	dirs := map[string]bool{got[0].ProjectDir: true, got[1].ProjectDir: true}
	assert.True(t, dirs["/proj/a"])
	assert.True(t, dirs["/proj/b"])
}

func TestActivityStore_LimitAndBeforeIDCursor(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	projectDir := "/proj/tail"
	base := time.Now().UTC().Truncate(time.Second)
	for i := 0; i < 5; i++ {
		require.NoError(t, store.AppendActivityEntry(ctx, projectDir, activitylog.Entry{
			Timestamp: base.Add(time.Duration(i) * time.Second),
			Kind:      activitylog.KindAttemptStarted,
			AttemptID: fmt.Sprintf("a%d", i),
		}))
	}

	// First page: most recent 2, still oldest-first within the page.
	page1, err := store.ReadActivityEntries(ctx, projectDir, activitylog.ReadOptions{Limit: 2})
	require.NoError(t, err)
	require.Len(t, page1, 2)
	assert.Equal(t, "a3", page1[0].AttemptID)
	assert.Equal(t, "a4", page1[1].AttemptID)
	require.NotZero(t, page1[0].ID)

	// Page backwards from the oldest entry seen so far.
	page2, err := store.ReadActivityEntries(ctx, projectDir, activitylog.ReadOptions{Limit: 2, BeforeID: page1[0].ID})
	require.NoError(t, err)
	require.Len(t, page2, 2)
	assert.Equal(t, "a1", page2[0].AttemptID)
	assert.Equal(t, "a2", page2[1].AttemptID)

	page3, err := store.ReadActivityEntries(ctx, projectDir, activitylog.ReadOptions{Limit: 2, BeforeID: page2[0].ID})
	require.NoError(t, err)
	require.Len(t, page3, 1)
	assert.Equal(t, "a0", page3[0].AttemptID)
}

func TestActivityStore_FailuresOnly(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	projectDir := "/proj/failures"
	now := time.Now()

	require.NoError(t, store.AppendActivityEntry(ctx, projectDir, activitylog.Entry{
		Timestamp: now, Kind: activitylog.KindAttemptEnded, State: "succeeded",
	}))
	require.NoError(t, store.AppendActivityEntry(ctx, projectDir, activitylog.Entry{
		Timestamp: now.Add(time.Second), Kind: activitylog.KindAttemptEnded, State: "failed",
	}))
	require.NoError(t, store.AppendActivityEntry(ctx, projectDir, activitylog.Entry{
		Timestamp: now.Add(2 * time.Second), Kind: activitylog.KindStepCompleted, StepName: "s1", Result: "fail",
	}))
	require.NoError(t, store.AppendActivityEntry(ctx, projectDir, activitylog.Entry{
		Timestamp: now.Add(3 * time.Second), Kind: activitylog.KindStepCompleted, StepName: "s1", Result: "success",
	}))

	got, err := store.ReadActivityEntries(ctx, projectDir, activitylog.ReadOptions{FailuresOnly: true})
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.True(t, got[0].IsFailure())
	assert.True(t, got[1].IsFailure())
}

func TestActivityStore_ImplementsActivityStore(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	// Verify sqlite.Store satisfies the activitylog.Appender interface.
	var _ activitylog.Appender = store
}
