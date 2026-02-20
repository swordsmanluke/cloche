package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/cloche-dev/cloche/internal/domain"
	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

func NewStore(dsn string) (*Store, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening sqlite: %w", err)
	}

	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrating: %w", err)
	}

	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func migrate(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS runs (
			id TEXT PRIMARY KEY,
			workflow_name TEXT NOT NULL,
			state TEXT NOT NULL,
			current_step TEXT,
			started_at TEXT,
			completed_at TEXT
		);
		CREATE TABLE IF NOT EXISTS step_executions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			run_id TEXT NOT NULL,
			step_name TEXT NOT NULL,
			result TEXT,
			started_at TEXT NOT NULL,
			completed_at TEXT,
			logs TEXT,
			git_ref TEXT,
			FOREIGN KEY (run_id) REFERENCES runs(id)
		);
	`)
	return err
}

func (s *Store) CreateRun(ctx context.Context, run *domain.Run) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO runs (id, workflow_name, state, current_step, started_at, completed_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		run.ID, run.WorkflowName, string(run.State), run.CurrentStep,
		formatTime(run.StartedAt), formatTime(run.CompletedAt),
	)
	return err
}

func (s *Store) GetRun(ctx context.Context, id string) (*domain.Run, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, workflow_name, state, current_step, started_at, completed_at FROM runs WHERE id = ?`, id)

	run := &domain.Run{}
	var startedAt, completedAt string
	err := row.Scan(&run.ID, &run.WorkflowName, &run.State, &run.CurrentStep, &startedAt, &completedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("run %q not found", id)
	}
	if err != nil {
		return nil, err
	}

	run.StartedAt = parseTime(startedAt)
	run.CompletedAt = parseTime(completedAt)
	return run, nil
}

func (s *Store) UpdateRun(ctx context.Context, run *domain.Run) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE runs SET state = ?, current_step = ?, started_at = ?, completed_at = ? WHERE id = ?`,
		string(run.State), run.CurrentStep,
		formatTime(run.StartedAt), formatTime(run.CompletedAt),
		run.ID,
	)
	return err
}

func (s *Store) ListRuns(ctx context.Context) ([]*domain.Run, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, workflow_name, state, current_step, started_at, completed_at FROM runs ORDER BY started_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var runs []*domain.Run
	for rows.Next() {
		run := &domain.Run{}
		var startedAt, completedAt string
		if err := rows.Scan(&run.ID, &run.WorkflowName, &run.State, &run.CurrentStep, &startedAt, &completedAt); err != nil {
			return nil, err
		}
		run.StartedAt = parseTime(startedAt)
		run.CompletedAt = parseTime(completedAt)
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

func (s *Store) SaveCapture(ctx context.Context, runID string, exec *domain.StepExecution) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO step_executions (run_id, step_name, result, started_at, completed_at, logs, git_ref)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		runID, exec.StepName, exec.Result,
		formatTime(exec.StartedAt), formatTime(exec.CompletedAt),
		exec.Logs, exec.GitRef,
	)
	return err
}

func (s *Store) GetCaptures(ctx context.Context, runID string) ([]*domain.StepExecution, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT step_name, result, started_at, completed_at, logs, git_ref
		 FROM step_executions WHERE run_id = ? ORDER BY id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var execs []*domain.StepExecution
	for rows.Next() {
		e := &domain.StepExecution{}
		var startedAt, completedAt, logs, gitRef string
		if err := rows.Scan(&e.StepName, &e.Result, &startedAt, &completedAt, &logs, &gitRef); err != nil {
			return nil, err
		}
		e.StartedAt = parseTime(startedAt)
		e.CompletedAt = parseTime(completedAt)
		e.Logs = logs
		e.GitRef = gitRef
		execs = append(execs, e)
	}
	return execs, rows.Err()
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339Nano)
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}
