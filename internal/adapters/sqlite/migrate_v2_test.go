package sqlite

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func TestMigrateProjectLogs_CreatesTasksFromRuns(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	projectDir := t.TempDir()

	// Create v1-style runs: two runs with same task_id, one without
	r1 := domain.NewRun("develop-bold-bear", "main")
	r1.ProjectDir = projectDir
	r1.TaskID = "cloche-123"
	r1.TaskTitle = "Fix the widget"
	r1.IsHost = true
	r1.Start()
	r1.Complete(domain.RunStateSucceeded)
	require.NoError(t, store.CreateRun(ctx, r1))

	r2 := domain.NewRun("develop-calm-fox", "main")
	r2.ProjectDir = projectDir
	r2.TaskID = "cloche-123"
	r2.TaskTitle = "Fix the widget"
	r2.IsHost = true
	r2.Start()
	r2.Complete(domain.RunStateFailed)
	require.NoError(t, store.CreateRun(ctx, r2))

	r3 := domain.NewRun("develop-dark-elm", "develop")
	r3.ProjectDir = projectDir
	r3.Start()
	r3.Complete(domain.RunStateSucceeded)
	require.NoError(t, store.CreateRun(ctx, r3))

	// Run per-project migration
	require.NoError(t, store.MigrateProjectLogs(projectDir))

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

func TestMigrateProjectLogs_MovesLogFiles(t *testing.T) {
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

	// Run per-project migration
	require.NoError(t, store.MigrateProjectLogs(dir))

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

func TestMigrateProjectLogs_Idempotent(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	projectDir := t.TempDir()

	r := domain.NewRun("test-run-idem", "develop")
	r.ProjectDir = projectDir
	r.TaskID = "task-idem"
	r.Start()
	require.NoError(t, store.CreateRun(ctx, r))

	// Run migration
	require.NoError(t, store.MigrateProjectLogs(projectDir))

	// Run migration again (should be a no-op)
	require.NoError(t, store.MigrateProjectLogs(projectDir))

	// Should still have exactly 1 task, 1 attempt
	var taskCount, attemptCount int
	store.db.QueryRow(`SELECT COUNT(*) FROM tasks`).Scan(&taskCount)
	store.db.QueryRow(`SELECT COUNT(*) FROM attempts`).Scan(&attemptCount)
	assert.Equal(t, 1, taskCount)
	assert.Equal(t, 1, attemptCount)
}

func TestMigrateProjectLogs_ParentChildLinking(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	projectDir := t.TempDir()

	// Parent host run
	parent := domain.NewRun("main-bold-fox", "main")
	parent.ProjectDir = projectDir
	parent.TaskID = "task-parent"
	parent.IsHost = true
	parent.Start()
	require.NoError(t, store.CreateRun(ctx, parent))

	// Child container run
	child := domain.NewRun("develop-calm-owl", "develop")
	child.ProjectDir = projectDir
	child.TaskID = "task-parent"
	child.ParentRunID = "main-bold-fox"
	child.Start()
	require.NoError(t, store.CreateRun(ctx, child))

	// Run per-project migration
	require.NoError(t, store.MigrateProjectLogs(projectDir))

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

func TestMigrateProjectLogs_PerProject(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	projectA := t.TempDir()
	projectB := t.TempDir()

	// Create runs in two projects
	rA := domain.NewRun("run-a", "develop")
	rA.ProjectDir = projectA
	rA.TaskID = "task-a"
	rA.Start()
	require.NoError(t, store.CreateRun(ctx, rA))

	rB := domain.NewRun("run-b", "develop")
	rB.ProjectDir = projectB
	rB.TaskID = "task-b"
	rB.Start()
	require.NoError(t, store.CreateRun(ctx, rB))

	// Migrate only project A
	require.NoError(t, store.MigrateProjectLogs(projectA))

	// Project A's run should be migrated
	var attemptA string
	store.db.QueryRow(`SELECT attempt_id FROM runs WHERE id = 'run-a'`).Scan(&attemptA)
	assert.NotEmpty(t, attemptA, "project A run should have attempt_id")

	// Project B's run should NOT be migrated yet
	var attemptB string
	store.db.QueryRow(`SELECT attempt_id FROM runs WHERE id = 'run-b'`).Scan(&attemptB)
	assert.Empty(t, attemptB, "project B run should not have attempt_id yet")

	// Now migrate project B
	require.NoError(t, store.MigrateProjectLogs(projectB))
	store.db.QueryRow(`SELECT attempt_id FROM runs WHERE id = 'run-b'`).Scan(&attemptB)
	assert.NotEmpty(t, attemptB, "project B run should have attempt_id after migration")
}

// TestMigrateParentStepName_ExistingRowsGetNull verifies that opening a
// pre-existing database that lacks the parent_step_name column succeeds and
// that existing rows read back with an empty ParentStepName.
func TestMigrateParentStepName_ExistingRowsGetNull(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy.db")

	// Build a database that intentionally lacks parent_step_name by using the
	// old schema (only the columns that existed before this migration).
	rawDB, err := sql.Open("sqlite", dbPath)
	require.NoError(t, err)
	_, err = rawDB.Exec(`
		CREATE TABLE runs (
			pk INTEGER PRIMARY KEY AUTOINCREMENT,
			id TEXT NOT NULL,
			workflow_name TEXT NOT NULL,
			state TEXT NOT NULL,
			active_steps TEXT,
			started_at TEXT,
			completed_at TEXT,
			project_dir TEXT NOT NULL DEFAULT '',
			error_message TEXT,
			container_id TEXT,
			base_sha TEXT,
			container_kept INTEGER NOT NULL DEFAULT 0,
			title TEXT NOT NULL DEFAULT '',
			is_host INTEGER NOT NULL DEFAULT 0,
			parent_run_id TEXT NOT NULL DEFAULT '',
			task_id TEXT NOT NULL DEFAULT '',
			task_title TEXT NOT NULL DEFAULT '',
			attempt_id TEXT NOT NULL DEFAULT '',
			UNIQUE(attempt_id, id)
		);
		CREATE TABLE step_executions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			run_id TEXT NOT NULL,
			step_name TEXT NOT NULL,
			result TEXT,
			started_at TEXT NOT NULL,
			completed_at TEXT,
			logs TEXT,
			git_ref TEXT
		);
	`)
	require.NoError(t, err)
	// Insert a legacy row without parent_step_name; provide all columns that
	// scanRun reads without COALESCE to avoid NULL scan errors.
	_, err = rawDB.Exec(`INSERT INTO runs (id, workflow_name, state, active_steps, started_at, completed_at, project_dir, attempt_id) VALUES ('legacy-1', 'develop', 'pending', '', '', '', '/proj', '')`)
	require.NoError(t, err)
	require.NoError(t, rawDB.Close())

	// Open via NewStore — this runs all migrations including adding parent_step_name
	store, err := NewStore(dbPath)
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	got, err := store.GetRun(ctx, "legacy-1")
	require.NoError(t, err)
	assert.Equal(t, "legacy-1", got.ID)
	assert.Equal(t, "", got.ParentStepName, "legacy row should have empty ParentStepName after migration")
}
