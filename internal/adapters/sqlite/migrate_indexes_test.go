package sqlite

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swordsmanluke/cloche/internal/domain"
	"github.com/swordsmanluke/cloche/internal/ports"
	_ "modernc.org/sqlite"
)

// TestMigrateSecondaryIndexes_Idempotent runs the migration twice against
// the same database and asserts it doesn't error the second time (the
// _migrations gate short-circuits, and each CREATE INDEX is itself
// IF NOT EXISTS).
func TestMigrateSecondaryIndexes_Idempotent(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	// NewStore already ran the migration once via migrate(); run it twice
	// more directly to exercise both the _migrations-gated fast path and
	// the underlying idempotent CREATE INDEX IF NOT EXISTS statements.
	require.NoError(t, migrateSecondaryIndexes(store.write))
	require.NoError(t, migrateSecondaryIndexes(store.write))

	for _, idx := range []string{
		"runs_project_dir_started_at",
		"runs_task_id",
		"runs_parent_run_id",
		"runs_attempt_id",
		"runs_state",
		"step_executions_run_id",
		"attempts_task_id",
		"log_files_run_id",
	} {
		var name string
		err := store.write.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'index' AND name = ?`, idx).Scan(&name)
		require.NoError(t, err, "expected index %s to exist", idx)
		assert.Equal(t, idx, name)
	}
}

// TestMigrateSecondaryIndexes_QueryPlansUseIndex asserts that the queries
// behind ListRunsByProject, ListChildRuns, GetCaptures, ListAttempts and
// GetLogFilesByStep use a secondary index (SEARCH ... USING INDEX) rather
// than a full table scan (SCAN), now that the indexes exist.
func TestMigrateSecondaryIndexes_QueryPlansUseIndex(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	require.NoError(t, migrateSecondaryIndexes(store.write))
	require.NoError(t, migrateSecondaryIndexes(store.write))

	ctx := context.Background()
	projectDir := t.TempDir()

	parent := domain.NewRun("main-bold-fox", "main")
	parent.ProjectDir = projectDir
	parent.Start()
	require.NoError(t, store.CreateRun(ctx, parent))

	child := domain.NewRun("develop-calm-owl", "develop")
	child.ProjectDir = projectDir
	child.ParentRunID = "main-bold-fox"
	child.Start()
	require.NoError(t, store.CreateRun(ctx, child))

	require.NoError(t, store.SaveCapture(ctx, "main-bold-fox", &domain.StepExecution{StepName: "implement"}))

	require.NoError(t, store.SaveTask(ctx, &domain.Task{ID: "task-1", Title: "t", Source: domain.TaskSourceExternal, ProjectDir: projectDir}))
	_, err = store.write.ExecContext(ctx,
		`INSERT INTO attempts (id, task_id, started_at, result, project_dir) VALUES (?, ?, ?, ?, ?)`,
		"attempt-1", "task-1", "2026-01-01T00:00:00Z", "succeeded", projectDir)
	require.NoError(t, err)

	require.NoError(t, store.SaveLogFile(ctx, &ports.LogFileEntry{RunID: "main-bold-fox", StepName: "implement", FileType: "step", FilePath: "/tmp/x.log"}))

	cases := []struct {
		name      string
		query     string
		args      []interface{}
		wantIndex string
	}{
		{
			name:      "ListRunsByProject",
			query:     `SELECT id FROM runs WHERE project_dir = ? ORDER BY CASE WHEN state = 'running' THEN 0 ELSE 1 END, started_at DESC`,
			args:      []interface{}{projectDir},
			wantIndex: "runs_project_dir_started_at",
		},
		{
			name:      "ListChildRuns",
			query:     `SELECT id FROM runs WHERE parent_run_id = ? ORDER BY started_at ASC`,
			args:      []interface{}{"main-bold-fox"},
			wantIndex: "runs_parent_run_id",
		},
		{
			name:      "GetCaptures",
			query:     `SELECT step_name FROM step_executions WHERE run_id = ? ORDER BY id`,
			args:      []interface{}{"main-bold-fox"},
			wantIndex: "step_executions_run_id",
		},
		{
			name:      "ListAttempts",
			query:     `SELECT id FROM attempts WHERE task_id = ? ORDER BY started_at ASC`,
			args:      []interface{}{"task-1"},
			wantIndex: "attempts_task_id",
		},
		{
			name:      "GetLogFilesByStep",
			query:     `SELECT id FROM log_files WHERE run_id = ? AND step_name = ? ORDER BY id`,
			args:      []interface{}{"main-bold-fox", "implement"},
			wantIndex: "log_files_run_id",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan := explainQueryPlan(t, store.write, tc.query, tc.args...)
			assert.Contains(t, plan, "SEARCH", "plan for %s should use SEARCH, got: %s", tc.name, plan)
			assert.Contains(t, plan, "USING INDEX "+tc.wantIndex, "plan for %s should use index %s, got: %s", tc.name, tc.wantIndex, plan)
			assert.NotContains(t, plan, "SCAN", "plan for %s should not be a full scan, got: %s", tc.name, plan)
		})
	}
}

func explainQueryPlan(t *testing.T, db *sql.DB, query string, args ...interface{}) string {
	t.Helper()
	rows, err := db.Query(`EXPLAIN QUERY PLAN `+query, args...)
	require.NoError(t, err)
	defer rows.Close()

	var lines []string
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		require.NoError(t, rows.Scan(&id, &parent, &notUsed, &detail))
		lines = append(lines, detail)
	}
	require.NoError(t, rows.Err())
	return strings.Join(lines, "\n")
}
