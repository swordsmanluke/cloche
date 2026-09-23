package sqlite

import (
	"context"
	"database/sql"
	"fmt"
)

// PurgeProject implements ports.ProjectPurger. Everything keyed on the
// project directory, or on a run/task/attempt that belongs to it, goes in one
// transaction so a failure part-way never leaves a project half-present.
// Callers are responsible for making sure nothing is running for the project
// first (see ClocheServer.PurgeProject).
func (s *Store) PurgeProject(ctx context.Context, projectDir string) (int64, error) {
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	// Dependents first, each scoped to this project through its run or task,
	// so nothing belonging to another project is ever swept up; runs and
	// tasks themselves go last.
	const projRuns = `(SELECT id FROM runs WHERE project_dir = ?)`
	const projTasks = `(SELECT id FROM tasks WHERE project_dir = ?)`
	const projThreads = `(SELECT id FROM help_threads WHERE run_id IN ` + projRuns + ` OR task_id IN ` + projTasks + `)`
	stmts := []struct{ name, sql string }{
		{"step_executions", `DELETE FROM step_executions WHERE run_id IN ` + projRuns},
		{"log_files", `DELETE FROM log_files WHERE run_id IN ` + projRuns},
		{"step_polls", `DELETE FROM step_polls WHERE run_id IN ` + projRuns},
		{"merge_queue", `DELETE FROM merge_queue WHERE project = ? OR run_id IN ` + projRuns},
		{"context_kv", `DELETE FROM context_kv WHERE run_id IN ` + projRuns + ` OR task_id IN ` + projTasks},
		{"help_messages", `DELETE FROM help_messages WHERE thread_id IN ` + projThreads},
		{"help_bindings", `DELETE FROM help_bindings WHERE thread_id IN ` + projThreads},
		{"help_threads", `DELETE FROM help_threads WHERE run_id IN ` + projRuns + ` OR task_id IN ` + projTasks},
		{"attempt_logs", `DELETE FROM attempt_logs WHERE task_id IN ` + projTasks},
		{"attempts", `DELETE FROM attempts WHERE project_dir = ? OR task_id IN ` + projTasks},
		{"activity_log", `DELETE FROM activity_log WHERE project_dir = ?`},
		{"evolution_log", `DELETE FROM evolution_log WHERE project_dir = ?`},
		{"repositories", `DELETE FROM repositories WHERE project_dir = ?`},
		{"tasks", `DELETE FROM tasks WHERE project_dir = ?`},
	}
	for _, st := range stmts {
		// Legacy tables (evolution_log from the removed evolution system)
		// exist only in databases created before their feature went away.
		if exists, err := tableExists(ctx, tx, st.name); err != nil {
			return 0, err
		} else if !exists {
			continue
		}
		args := make([]any, countPlaceholders(st.sql))
		for i := range args {
			args[i] = projectDir
		}
		if _, err := tx.ExecContext(ctx, st.sql, args...); err != nil {
			return 0, fmt.Errorf("deleting %s: %w", st.name, err)
		}
	}

	res, err := tx.ExecContext(ctx, `DELETE FROM runs WHERE project_dir = ?`, projectDir)
	if err != nil {
		return 0, fmt.Errorf("deleting runs: %w", err)
	}
	deleted, _ := res.RowsAffected()

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return deleted, nil
}

func tableExists(ctx context.Context, tx *sql.Tx, name string) (bool, error) {
	var n int
	err := tx.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&n)
	return n > 0, err
}

func countPlaceholders(sql string) int {
	n := 0
	for _, c := range sql {
		if c == '?' {
			n++
		}
	}
	return n
}
