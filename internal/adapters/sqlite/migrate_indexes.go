package sqlite

import (
	"database/sql"
	"fmt"
	"time"
)

// secondaryIndexStmts creates indexes on runs, step_executions, attempts and
// log_files to support the daemon's most common lookups (by project_dir,
// task_id, parent_run_id, attempt_id, state, and run_id), which previously
// fell back to full table scans on every call.
var secondaryIndexStmts = []string{
	`CREATE INDEX IF NOT EXISTS runs_project_dir_started_at ON runs(project_dir, started_at)`,
	`CREATE INDEX IF NOT EXISTS runs_task_id ON runs(task_id)`,
	`CREATE INDEX IF NOT EXISTS runs_parent_run_id ON runs(parent_run_id)`,
	`CREATE INDEX IF NOT EXISTS runs_attempt_id ON runs(attempt_id)`,
	`CREATE INDEX IF NOT EXISTS runs_state ON runs(state)`,
	`CREATE INDEX IF NOT EXISTS step_executions_run_id ON step_executions(run_id)`,
	`CREATE INDEX IF NOT EXISTS attempts_task_id ON attempts(task_id)`,
	`CREATE INDEX IF NOT EXISTS log_files_run_id ON log_files(run_id)`,
}

const migrateSecondaryIndexesID = "secondary-indexes-v1"

// migrateSecondaryIndexes creates the indexes in secondaryIndexStmts, gated
// via the _migrations table (same mechanism as migrateV2Schema and
// backfillAgentNames) so repeat daemon starts skip past it rather than
// re-running eight CREATE INDEX IF NOT EXISTS statements. Each statement is
// idempotent on its own (IF NOT EXISTS), so this is also safe to call
// directly more than once, e.g. from a test.
func migrateSecondaryIndexes(db *sql.DB) error {
	var count int
	row := db.QueryRow(`SELECT COUNT(*) FROM _migrations WHERE id = ?`, migrateSecondaryIndexesID)
	if err := row.Scan(&count); err == nil && count > 0 {
		return nil
	}

	for _, stmt := range secondaryIndexStmts {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("creating secondary index: %w", err)
		}
	}

	db.Exec(`INSERT OR IGNORE INTO _migrations (id, applied_at) VALUES (?, ?)`,
		migrateSecondaryIndexesID, time.Now().UTC().Format(time.RFC3339))

	return nil
}
