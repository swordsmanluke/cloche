package activitylog_test

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/cloche-dev/cloche/internal/activitylog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newProjectDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	return dir
}

func TestAppendAndRead(t *testing.T) {
	projectDir := newProjectDir(t)
	logger := activitylog.NewLogger(projectDir)

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

	got, err := activitylog.Read(projectDir, activitylog.ReadOptions{})
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

func TestReadMissingFile(t *testing.T) {
	projectDir := newProjectDir(t)
	// No activity.log created yet.
	entries, err := activitylog.Read(projectDir, activitylog.ReadOptions{})
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestReadTimeFilter(t *testing.T) {
	projectDir := newProjectDir(t)
	logger := activitylog.NewLogger(projectDir)

	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	all := []activitylog.Entry{
		{Timestamp: base, Kind: activitylog.KindAttemptStarted, AttemptID: "a1"},
		{Timestamp: base.Add(10 * time.Minute), Kind: activitylog.KindStepStarted, AttemptID: "a1", StepName: "s1"},
		{Timestamp: base.Add(20 * time.Minute), Kind: activitylog.KindStepCompleted, AttemptID: "a1", StepName: "s1"},
		{Timestamp: base.Add(30 * time.Minute), Kind: activitylog.KindAttemptEnded, AttemptID: "a1"},
	}
	for _, e := range all {
		require.NoError(t, logger.Append(e))
	}

	// Since filter: exclude entries before base+5m
	since := base.Add(5 * time.Minute)
	got, err := activitylog.Read(projectDir, activitylog.ReadOptions{Since: since})
	require.NoError(t, err)
	assert.Len(t, got, 3) // entries at +10m, +20m, +30m

	// Until filter: exclude entries after base+15m
	until := base.Add(15 * time.Minute)
	got, err = activitylog.Read(projectDir, activitylog.ReadOptions{Until: until})
	require.NoError(t, err)
	assert.Len(t, got, 2) // entries at 0m and +10m

	// Both filters
	got, err = activitylog.Read(projectDir, activitylog.ReadOptions{Since: since, Until: until})
	require.NoError(t, err)
	assert.Len(t, got, 1) // only +10m
	assert.Equal(t, activitylog.KindStepStarted, got[0].Kind)
}

func TestAppendCreatesDir(t *testing.T) {
	// The .cloche/ directory does not exist yet.
	projectDir := t.TempDir()
	logger := activitylog.NewLogger(projectDir)
	err := logger.Append(activitylog.Entry{Kind: activitylog.KindAttemptStarted, AttemptID: "x"})
	require.NoError(t, err)

	logPath := filepath.Join(projectDir, ".cloche", "activity.log")
	_, err = os.Stat(logPath)
	require.NoError(t, err, "activity.log should have been created")
}

func TestAppendSetsTimestamp(t *testing.T) {
	projectDir := newProjectDir(t)
	logger := activitylog.NewLogger(projectDir)

	before := time.Now()
	err := logger.Append(activitylog.Entry{Kind: activitylog.KindStepStarted, StepName: "x"})
	require.NoError(t, err)
	after := time.Now()

	entries, err := activitylog.Read(projectDir, activitylog.ReadOptions{})
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.False(t, entries[0].Timestamp.IsZero())
	assert.True(t, !entries[0].Timestamp.Before(before) && !entries[0].Timestamp.After(after.Add(time.Second)))
}

func TestConcurrentAppend(t *testing.T) {
	projectDir := newProjectDir(t)
	logger := activitylog.NewLogger(projectDir)

	const n = 50
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			err := logger.Append(activitylog.Entry{
				Kind:      activitylog.KindStepStarted,
				StepName:  "step",
				AttemptID: "a",
			})
			assert.NoError(t, err)
		}(i)
	}
	wg.Wait()

	entries, err := activitylog.Read(projectDir, activitylog.ReadOptions{})
	require.NoError(t, err)
	assert.Len(t, entries, n, "all concurrent writes should produce valid entries")
}

func TestReadSkipsMalformedLines(t *testing.T) {
	projectDir := newProjectDir(t)
	logPath := filepath.Join(projectDir, ".cloche", "activity.log")
	require.NoError(t, os.MkdirAll(filepath.Dir(logPath), 0755))

	// Write one valid entry, one malformed line, one valid entry.
	content := `{"ts":"2026-01-01T00:00:00Z","kind":"attempt_started","attempt_id":"a1"}
not valid json {{{
{"ts":"2026-01-01T00:01:00Z","kind":"attempt_ended","attempt_id":"a1","state":"succeeded"}
`
	require.NoError(t, os.WriteFile(logPath, []byte(content), 0644))

	entries, err := activitylog.Read(projectDir, activitylog.ReadOptions{})
	require.NoError(t, err)
	assert.Len(t, entries, 2)
	assert.Equal(t, activitylog.KindAttemptStarted, entries[0].Kind)
	assert.Equal(t, activitylog.KindAttemptEnded, entries[1].Kind)
}
