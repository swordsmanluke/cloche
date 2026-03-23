package activitylog_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/cloche-dev/cloche/internal/activitylog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeStore is a simple in-memory Appender for testing.
type fakeStore struct {
	mu      sync.Mutex
	entries map[string][]activitylog.Entry // projectDir -> entries
}

func (f *fakeStore) AppendActivityEntry(_ context.Context, projectDir string, entry activitylog.Entry) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.entries == nil {
		f.entries = make(map[string][]activitylog.Entry)
	}
	f.entries[projectDir] = append(f.entries[projectDir], entry)
	return nil
}

func (f *fakeStore) list(projectDir string) []activitylog.Entry {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]activitylog.Entry(nil), f.entries[projectDir]...)
}

func TestAppendDelegatesToStore(t *testing.T) {
	store := &fakeStore{}
	logger := activitylog.NewLogger("/proj", store)

	now := time.Now().UTC().Truncate(time.Second)

	entries := []activitylog.Entry{
		{Timestamp: now, Kind: activitylog.KindAttemptStarted, TaskID: "TASK-1", AttemptID: "a1b2"},
		{Timestamp: now.Add(time.Second), Kind: activitylog.KindStepStarted, TaskID: "TASK-1", AttemptID: "a1b2", WorkflowName: "main", StepName: "implement"},
		{Timestamp: now.Add(2 * time.Second), Kind: activitylog.KindStepCompleted, TaskID: "TASK-1", AttemptID: "a1b2", WorkflowName: "main", StepName: "implement", Result: "success"},
		{Timestamp: now.Add(3 * time.Second), Kind: activitylog.KindAttemptEnded, TaskID: "TASK-1", AttemptID: "a1b2", State: "succeeded"},
	}

	for _, e := range entries {
		require.NoError(t, logger.Append(e))
	}

	got := store.list("/proj")
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

func TestAppendSetsTimestamp(t *testing.T) {
	store := &fakeStore{}
	logger := activitylog.NewLogger("/proj", store)

	before := time.Now()
	require.NoError(t, logger.Append(activitylog.Entry{Kind: activitylog.KindStepStarted, StepName: "x"}))
	after := time.Now()

	got := store.list("/proj")
	require.Len(t, got, 1)
	assert.False(t, got[0].Timestamp.IsZero())
	assert.True(t, !got[0].Timestamp.Before(before) && !got[0].Timestamp.After(after.Add(time.Second)))
}

func TestAppendProjectIsolation(t *testing.T) {
	store := &fakeStore{}
	loggerA := activitylog.NewLogger("/proj/a", store)
	loggerB := activitylog.NewLogger("/proj/b", store)

	require.NoError(t, loggerA.Append(activitylog.Entry{Kind: activitylog.KindAttemptStarted, AttemptID: "a1"}))
	require.NoError(t, loggerB.Append(activitylog.Entry{Kind: activitylog.KindAttemptStarted, AttemptID: "b1"}))

	gotA := store.list("/proj/a")
	gotB := store.list("/proj/b")
	require.Len(t, gotA, 1)
	require.Len(t, gotB, 1)
	assert.Equal(t, "a1", gotA[0].AttemptID)
	assert.Equal(t, "b1", gotB[0].AttemptID)
}

func TestConcurrentAppend(t *testing.T) {
	store := &fakeStore{}
	logger := activitylog.NewLogger("/proj", store)

	const n = 50
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			err := logger.Append(activitylog.Entry{
				Kind:      activitylog.KindStepStarted,
				StepName:  "step",
				AttemptID: "a",
			})
			assert.NoError(t, err)
		}()
	}
	wg.Wait()

	got := store.list("/proj")
	assert.Len(t, got, n, "all concurrent writes should be recorded")
}
