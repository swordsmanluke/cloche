package sqlite

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigrateV2_CreatesTasksFromRuns(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	// Create v1-style runs: two runs with same task_id, one without
	r1 := domain.NewRun("develop-bold-bear", "main")
	r1.ProjectDir = t.TempDir()
	r1.TaskID = "cloche-123"
	r1.TaskTitle = "Fix the widget"
	r1.IsHost = true
	r1.Start()
	r1.Complete(domain.RunStateSucceeded)
	require.NoError(t, store.CreateRun(ctx, r1))

	r2 := domain.NewRun("develop-calm-fox", "main")
	r2.ProjectDir = r1.ProjectDir
	r2.TaskID = "cloche-123"
	r2.TaskTitle = "Fix the widget"
	r2.IsHost = true
	r2.Start()
	r2.Complete(domain.RunStateFailed)
	require.NoError(t, store.CreateRun(ctx, r2))

	r3 := domain.NewRun("develop-dark-elm", "develop")
	r3.ProjectDir = r1.ProjectDir
	r3.Start()
	r3.Complete(domain.RunStateSucceeded)
	require.NoError(t, store.CreateRun(ctx, r3))

	// Run migration now that data exists
	require.NoError(t, migrateV2(store.db))

	// Verify tasks table was populated by migration
	var taskCount int
	err = store.db.QueryRow(`SELECT COUNT(*) FROM tasks`).Scan(&taskCount)
	require.NoError(t, err)
	assert.Equal(t, 2, taskCount, "should have 2 tasks: one external, one user-initiated")

	// Verify attempts were created
	var attemptCount int
	err = store.db.QueryRow(`SELECT COUNT(*) FROM attempts`).Scan(&attemptCount)
	require.NoError(t, err)
	assert.Equal(t, 3, attemptCount, "should have 3 attempts (one per top-level run)")

	// Verify the external task exists
	var title, source string
	err = store.db.QueryRow(`SELECT title, source FROM tasks WHERE id = 'cloche-123'`).Scan(&title, &source)
	require.NoError(t, err)
	assert.Equal(t, "Fix the widget", title)
	assert.Equal(t, "external", source)

	// Verify user-initiated task was created for r3
	var userSource string
	err = store.db.QueryRow(`SELECT source FROM tasks WHERE id != 'cloche-123'`).Scan(&userSource)
	require.NoError(t, err)
	assert.Equal(t, "user-initiated", userSource)
}

func TestMigrateV2_MovesLogFiles(t *testing.T) {
	dir := t.TempDir()

	// Create v1-style log directory
	outputDir := filepath.Join(dir, ".cloche", "test-run-1", "output")
	require.NoError(t, os.MkdirAll(outputDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(outputDir, "implement.log"), []byte("impl output"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(outputDir, "full.log"), []byte("full output"), 0644))

	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	r := domain.NewRun("test-run-1", "develop")
	r.ProjectDir = dir
	r.TaskID = "task-abc"
	r.Start()
	r.Complete(domain.RunStateSucceeded)
	require.NoError(t, store.CreateRun(ctx, r))

	// Run migration now that data exists
	require.NoError(t, migrateV2(store.db))

	// Find the attempt ID that was generated
	var attemptID string
	err = store.db.QueryRow(`SELECT id FROM attempts WHERE task_id = 'task-abc'`).Scan(&attemptID)
	require.NoError(t, err)
	require.NotEmpty(t, attemptID)

	// Verify files were moved to new location
	newDir := filepath.Join(dir, ".cloche", "logs", "task-abc", attemptID)
	assert.FileExists(t, filepath.Join(newDir, "develop-implement.log"))
	assert.FileExists(t, filepath.Join(newDir, "full.log"))

	// Verify old directory was cleaned up
	assert.NoDirExists(t, outputDir)
}

func TestMigrateV2_Idempotent(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	r := domain.NewRun("test-run-idem", "develop")
	r.ProjectDir = t.TempDir()
	r.TaskID = "task-idem"
	r.Start()
	require.NoError(t, store.CreateRun(ctx, r))

	// Run migration to populate
	require.NoError(t, migrateV2(store.db))

	// Run migration again (it should be a no-op)
	err = migrateV2(store.db)
	require.NoError(t, err)

	// Should still have exactly 1 task, 1 attempt
	var taskCount, attemptCount int
	store.db.QueryRow(`SELECT COUNT(*) FROM tasks`).Scan(&taskCount)
	store.db.QueryRow(`SELECT COUNT(*) FROM attempts`).Scan(&attemptCount)
	assert.Equal(t, 1, taskCount)
	assert.Equal(t, 1, attemptCount)
}

func TestMigrateV2_ParentChildLinking(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	// Parent host run
	parent := domain.NewRun("main-bold-fox", "main")
	parent.ProjectDir = t.TempDir()
	parent.TaskID = "task-parent"
	parent.IsHost = true
	parent.Start()
	require.NoError(t, store.CreateRun(ctx, parent))

	// Child container run
	child := domain.NewRun("develop-calm-owl", "develop")
	child.ProjectDir = parent.ProjectDir
	child.TaskID = "task-parent"
	child.ParentRunID = "main-bold-fox"
	child.Start()
	require.NoError(t, store.CreateRun(ctx, child))

	// Run migration now that data exists
	require.NoError(t, migrateV2(store.db))

	// Both should share the same attempt_id
	var parentAttempt, childAttempt string
	store.db.QueryRow(`SELECT attempt_id FROM runs WHERE id = 'main-bold-fox'`).Scan(&parentAttempt)
	store.db.QueryRow(`SELECT attempt_id FROM runs WHERE id = 'develop-calm-owl'`).Scan(&childAttempt)
	assert.NotEmpty(t, parentAttempt)
	assert.Equal(t, parentAttempt, childAttempt, "parent and child should share attempt ID")

	// Only 1 attempt should exist (not 2)
	var attemptCount int
	store.db.QueryRow(`SELECT COUNT(*) FROM attempts`).Scan(&attemptCount)
	assert.Equal(t, 1, attemptCount)
}
