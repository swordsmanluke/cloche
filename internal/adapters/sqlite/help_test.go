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

func newHelpThread(id, channel, name, taskID, runID string) *domain.HelpThread {
	now := time.Now().Truncate(time.Second)
	return &domain.HelpThread{
		ID:        id,
		Channel:   channel,
		Name:      name,
		TaskID:    taskID,
		AttemptID: "att-1",
		RunID:     runID,
		StepName:  "ask-step",
		Title:     "Which schema?",
		State:     domain.ThreadStateAwaitingUser,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func TestHelpStore_CreateAndGetThread(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	thread := newHelpThread("thread-1", "cloche", "schema-choice-1", "task-a", "run-1")
	require.NoError(t, store.CreateThread(ctx, thread))

	msg := &domain.HelpMessage{
		ID:        "msg-1",
		ThreadID:  thread.ID,
		Author:    domain.MessageAuthorAgent,
		Body:      "Which schema should I use?",
		Options:   []string{"A", "B"},
		CreatedAt: time.Now(),
	}
	require.NoError(t, store.AppendMessage(ctx, msg))

	got, msgs, err := store.GetThread(ctx, "thread-1")
	require.NoError(t, err)
	assert.Equal(t, "cloche", got.Channel)
	assert.Equal(t, "schema-choice-1", got.Name)
	assert.Equal(t, domain.ThreadStateAwaitingUser, got.State)
	require.Len(t, msgs, 1)
	assert.Equal(t, "Which schema should I use?", msgs[0].Body)
	assert.Equal(t, []string{"A", "B"}, msgs[0].Options)
	assert.Equal(t, domain.MessageAuthorAgent, msgs[0].Author)
}

func TestHelpStore_GetThread_NotFound(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	_, _, err = store.GetThread(context.Background(), "thread-missing")
	assert.Error(t, err)
}

func TestHelpStore_ResolveThread(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	require.NoError(t, store.CreateThread(ctx, newHelpThread("thread-1", "cloche", "schema-choice-1", "task-a", "run-1")))
	require.NoError(t, store.CreateThread(ctx, newHelpThread("thread-2", "mazd", "schema-choice-1", "task-b", "run-2")))
	require.NoError(t, store.CreateThread(ctx, newHelpThread("thread-3", "cloche", "unique-name-1", "task-c", "run-3")))

	t.Run("by address", func(t *testing.T) {
		got, err := store.ResolveThread(ctx, "cloche/schema-choice-1")
		require.NoError(t, err)
		assert.Equal(t, "thread-1", got.ID)
	})

	t.Run("by raw id", func(t *testing.T) {
		got, err := store.ResolveThread(ctx, "thread-2")
		require.NoError(t, err)
		assert.Equal(t, "thread-2", got.ID)
	})

	t.Run("by unambiguous bare name", func(t *testing.T) {
		got, err := store.ResolveThread(ctx, "unique-name-1")
		require.NoError(t, err)
		assert.Equal(t, "thread-3", got.ID)
	})

	t.Run("ambiguous bare name errors", func(t *testing.T) {
		_, err := store.ResolveThread(ctx, "schema-choice-1")
		assert.Error(t, err)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := store.ResolveThread(ctx, "cloche/does-not-exist")
		assert.Error(t, err)
	})
}

func TestHelpStore_ListThreads(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	require.NoError(t, store.CreateThread(ctx, newHelpThread("thread-1", "cloche", "a-1", "task-a", "run-1")))
	require.NoError(t, store.CreateThread(ctx, newHelpThread("thread-2", "mazd", "b-1", "task-b", "run-2")))
	closedThread := newHelpThread("thread-3", "cloche", "c-1", "task-c", "run-3")
	closedThread.State = domain.ThreadStateClosed
	require.NoError(t, store.CreateThread(ctx, closedThread))

	open, err := store.ListThreads(ctx, ports.HelpThreadFilter{})
	require.NoError(t, err)
	assert.Len(t, open, 2)

	all, err := store.ListThreads(ctx, ports.HelpThreadFilter{All: true})
	require.NoError(t, err)
	assert.Len(t, all, 3)

	clocheOnly, err := store.ListThreads(ctx, ports.HelpThreadFilter{Channel: "cloche", All: true})
	require.NoError(t, err)
	assert.Len(t, clocheOnly, 2)
}

func TestHelpStore_ListOpenThreadsByRun(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	require.NoError(t, store.CreateThread(ctx, newHelpThread("thread-1", "cloche", "a-1", "task-a", "run-1")))

	open, err := store.ListOpenThreadsByRun(ctx, "run-1")
	require.NoError(t, err)
	require.Len(t, open, 1)

	require.NoError(t, store.SetThreadState(ctx, "thread-1", domain.ThreadStateAwaitingAgent))
	open, err = store.ListOpenThreadsByRun(ctx, "run-1")
	require.NoError(t, err)
	assert.Len(t, open, 0)
}

func TestHelpStore_CountThreadsWithNamePrefix(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	require.NoError(t, store.CreateThread(ctx, newHelpThread("thread-1", "cloche", "schema-choice-1", "task-a", "run-1")))
	require.NoError(t, store.CreateThread(ctx, newHelpThread("thread-2", "cloche", "schema-choice-2", "task-a", "run-2")))
	require.NoError(t, store.CreateThread(ctx, newHelpThread("thread-3", "mazd", "schema-choice-1", "task-b", "run-3")))

	count, err := store.CountThreadsWithNamePrefix(ctx, "cloche", "schema-choice")
	require.NoError(t, err)
	assert.Equal(t, 2, count)

	count, err = store.CountThreadsWithNamePrefix(ctx, "mazd", "schema-choice")
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

func TestHelpStore_ArchiveThreadsByTask(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	require.NoError(t, store.CreateThread(ctx, newHelpThread("thread-1", "cloche", "a-1", "task-a", "run-1")))
	require.NoError(t, store.CreateThread(ctx, newHelpThread("thread-2", "cloche", "a-2", "task-a", "run-2")))
	require.NoError(t, store.CreateThread(ctx, newHelpThread("thread-3", "cloche", "a-3", "task-other", "run-3")))

	n, err := store.ArchiveThreadsByTask(ctx, "task-a")
	require.NoError(t, err)
	assert.EqualValues(t, 2, n)

	got, _, err := store.GetThread(ctx, "thread-1")
	require.NoError(t, err)
	assert.Equal(t, domain.ThreadStateArchived, got.State)
	require.NotNil(t, got.ArchivedAt)

	untouched, _, err := store.GetThread(ctx, "thread-3")
	require.NoError(t, err)
	assert.Equal(t, domain.ThreadStateAwaitingUser, untouched.State)
}

func TestHelpStore_DeleteArchivedThreadsOlderThan(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	old := newHelpThread("thread-old", "cloche", "old-1", "task-a", "run-1")
	oldArchived := time.Now().Add(-48 * time.Hour)
	old.State = domain.ThreadStateArchived
	old.ArchivedAt = &oldArchived
	require.NoError(t, store.CreateThread(ctx, old))
	require.NoError(t, store.AppendMessage(ctx, &domain.HelpMessage{
		ID: "msg-old", ThreadID: "thread-old", Author: domain.MessageAuthorAgent, Body: "hi", CreatedAt: time.Now(),
	}))

	recent := newHelpThread("thread-recent", "cloche", "recent-1", "task-a", "run-2")
	recentArchived := time.Now().Add(-1 * time.Hour)
	recent.State = domain.ThreadStateArchived
	recent.ArchivedAt = &recentArchived
	require.NoError(t, store.CreateThread(ctx, recent))

	n, err := store.DeleteArchivedThreadsOlderThan(ctx, time.Now().Add(-24*time.Hour))
	require.NoError(t, err)
	assert.EqualValues(t, 1, n)

	_, _, err = store.GetThread(ctx, "thread-old")
	assert.Error(t, err)

	got, _, err := store.GetThread(ctx, "thread-recent")
	require.NoError(t, err)
	assert.Equal(t, "thread-recent", got.ID)
}

func TestHelpStore_BindAndResolveExternal(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	require.NoError(t, store.CreateThread(ctx, newHelpThread("thread-1", "cloche", "a-1", "task-a", "run-1")))
	require.NoError(t, store.BindExternal(ctx, "thread-1", "slack", "1234.5678"))

	got, err := store.ResolveExternal(ctx, "slack", "1234.5678")
	require.NoError(t, err)
	assert.Equal(t, "thread-1", got)

	missing, err := store.ResolveExternal(ctx, "slack", "nope")
	require.NoError(t, err)
	assert.Equal(t, "", missing)
}
