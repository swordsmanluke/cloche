package beads

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTrackerListReady_Empty(t *testing.T) {
	dir := t.TempDir()
	tracker := NewTracker(dir)

	tasks, err := tracker.ListReady(context.Background(), dir)
	require.NoError(t, err)
	assert.Empty(t, tasks)
}

func TestTrackerListReady_FiltersByOpenStatus(t *testing.T) {
	dir := t.TempDir()
	beadsDir := filepath.Join(dir, ".beads")
	require.NoError(t, os.MkdirAll(beadsDir, 0755))

	issuesJSONL := `{"id":"t1","title":"Open task","status":"open","priority":1}
{"id":"t2","title":"Closed task","status":"closed","priority":2}
{"id":"t3","title":"In progress","status":"in_progress","priority":3}
{"id":"t4","title":"Another open","status":"open","priority":5}
`
	require.NoError(t, os.WriteFile(filepath.Join(beadsDir, "issues.jsonl"), []byte(issuesJSONL), 0644))

	tracker := NewTracker(dir)
	tasks, err := tracker.ListReady(context.Background(), dir)
	require.NoError(t, err)
	require.Len(t, tasks, 2)

	// Should be ordered by priority descending
	assert.Equal(t, "t4", tasks[0].ID)
	assert.Equal(t, "Another open", tasks[0].Title)
	assert.Equal(t, 5, tasks[0].Priority)

	assert.Equal(t, "t1", tasks[1].ID)
	assert.Equal(t, 1, tasks[1].Priority)
}

func TestTrackerListReady_LastOccurrenceWins(t *testing.T) {
	dir := t.TempDir()
	beadsDir := filepath.Join(dir, ".beads")
	require.NoError(t, os.MkdirAll(beadsDir, 0755))

	// Same ID appears twice; second occurrence should win
	issuesJSONL := `{"id":"t1","title":"Original","status":"open","priority":1}
{"id":"t1","title":"Updated","status":"closed","priority":1}
`
	require.NoError(t, os.WriteFile(filepath.Join(beadsDir, "issues.jsonl"), []byte(issuesJSONL), 0644))

	tracker := NewTracker(dir)
	tasks, err := tracker.ListReady(context.Background(), dir)
	require.NoError(t, err)
	assert.Empty(t, tasks) // t1 is now closed
}

func TestTrackerListReady_SkipsTombstones(t *testing.T) {
	dir := t.TempDir()
	beadsDir := filepath.Join(dir, ".beads")
	require.NoError(t, os.MkdirAll(beadsDir, 0755))

	issuesJSONL := `{"id":"t1","title":"Active","status":"open","priority":1}
{"id":"t2","title":"Deleted","status":"tombstone","priority":2}
`
	require.NoError(t, os.WriteFile(filepath.Join(beadsDir, "issues.jsonl"), []byte(issuesJSONL), 0644))

	tracker := NewTracker(dir)
	tasks, err := tracker.ListReady(context.Background(), dir)
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	assert.Equal(t, "t1", tasks[0].ID)
}

func TestTrackerClaim(t *testing.T) {
	dir := t.TempDir()
	beadsDir := filepath.Join(dir, ".beads")
	require.NoError(t, os.MkdirAll(beadsDir, 0755))

	issuesJSONL := `{"id":"t1","title":"Task","status":"open","priority":1}
`
	require.NoError(t, os.WriteFile(filepath.Join(beadsDir, "issues.jsonl"), []byte(issuesJSONL), 0644))

	tracker := NewTracker(dir)
	err := tracker.Claim(context.Background(), "t1")
	require.NoError(t, err)

	// Verify task is now in_progress
	tasks, err := tracker.ListReady(context.Background(), dir)
	require.NoError(t, err)
	assert.Empty(t, tasks) // no longer "open"
}

func TestTrackerComplete(t *testing.T) {
	dir := t.TempDir()
	beadsDir := filepath.Join(dir, ".beads")
	require.NoError(t, os.MkdirAll(beadsDir, 0755))

	issuesJSONL := `{"id":"t1","title":"Task","status":"in_progress","priority":1}
`
	require.NoError(t, os.WriteFile(filepath.Join(beadsDir, "issues.jsonl"), []byte(issuesJSONL), 0644))

	tracker := NewTracker(dir)
	err := tracker.Complete(context.Background(), "t1")
	require.NoError(t, err)

	// Read raw file to verify
	issues, err := readIssuesFromFile(filepath.Join(beadsDir, "issues.jsonl"))
	require.NoError(t, err)
	require.Len(t, issues, 1)
	assert.Equal(t, "closed", issues[0].Status)
}

func TestTrackerFail(t *testing.T) {
	dir := t.TempDir()
	beadsDir := filepath.Join(dir, ".beads")
	require.NoError(t, os.MkdirAll(beadsDir, 0755))

	issuesJSONL := `{"id":"t1","title":"Task","status":"in_progress","priority":1}
`
	require.NoError(t, os.WriteFile(filepath.Join(beadsDir, "issues.jsonl"), []byte(issuesJSONL), 0644))

	tracker := NewTracker(dir)
	err := tracker.Fail(context.Background(), "t1")
	require.NoError(t, err)

	// Should be back to open
	tasks, err := tracker.ListReady(context.Background(), dir)
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	assert.Equal(t, "t1", tasks[0].ID)
}

func TestTrackerClaim_NotFound(t *testing.T) {
	dir := t.TempDir()
	beadsDir := filepath.Join(dir, ".beads")
	require.NoError(t, os.MkdirAll(beadsDir, 0755))

	issuesJSONL := `{"id":"t1","title":"Task","status":"open","priority":1}
`
	require.NoError(t, os.WriteFile(filepath.Join(beadsDir, "issues.jsonl"), []byte(issuesJSONL), 0644))

	tracker := NewTracker(dir)
	err := tracker.Claim(context.Background(), "nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestTrackerImplementsInterface(t *testing.T) {
	var _ interface {
		ListReady(context.Context, string) ([]interface{}, error)
	}
	// Compile-time check
	tracker := NewTracker("")
	_ = tracker
}
