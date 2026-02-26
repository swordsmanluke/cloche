package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/ports"
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

	// Enable WAL mode for concurrent read/write access
	db.Exec("PRAGMA journal_mode=WAL")

	// Wait up to 5 seconds when the database is locked instead of failing immediately
	db.Exec("PRAGMA busy_timeout=5000")

	// Serialize all Go-side access through a single connection so SQLite
	// never sees concurrent writers (WAL + busy_timeout as defense-in-depth).
	db.SetMaxOpenConns(1)

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
			active_steps TEXT,
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
	if err != nil {
		return err
	}

	// Evolution schema additions (idempotent)
	alterStmts := []string{
		`ALTER TABLE runs ADD COLUMN project_dir TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE step_executions ADD COLUMN prompt_text TEXT`,
		`ALTER TABLE step_executions ADD COLUMN agent_output TEXT`,
		`ALTER TABLE step_executions ADD COLUMN attempt_number INTEGER DEFAULT 0`,
	}
	for _, stmt := range alterStmts {
		db.Exec(stmt) // ignore "duplicate column" errors
	}

	_, err2 := db.Exec(`CREATE TABLE IF NOT EXISTS evolution_log (
		id TEXT PRIMARY KEY,
		project_dir TEXT NOT NULL,
		workflow_name TEXT NOT NULL,
		trigger_run_id TEXT NOT NULL,
		created_at TEXT NOT NULL,
		classification TEXT,
		changes_json TEXT NOT NULL,
		knowledge_delta TEXT
	)`)
	if err2 != nil {
		return err2
	}

	return nil
}

func (s *Store) CreateRun(ctx context.Context, run *domain.Run) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO runs (id, workflow_name, state, active_steps, started_at, completed_at, project_dir)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		run.ID, run.WorkflowName, string(run.State), run.ActiveStepsString(),
		formatTime(run.StartedAt), formatTime(run.CompletedAt), run.ProjectDir,
	)
	return err
}

func (s *Store) GetRun(ctx context.Context, id string) (*domain.Run, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, workflow_name, state, active_steps, started_at, completed_at, project_dir FROM runs WHERE id = ?`, id)

	run := &domain.Run{}
	var activeSteps, startedAt, completedAt string
	err := row.Scan(&run.ID, &run.WorkflowName, &run.State, &activeSteps, &startedAt, &completedAt, &run.ProjectDir)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("run %q not found", id)
	}
	if err != nil {
		return nil, err
	}

	run.SetActiveStepsFromString(activeSteps)
	run.StartedAt = parseTime(startedAt)
	run.CompletedAt = parseTime(completedAt)
	return run, nil
}

func (s *Store) UpdateRun(ctx context.Context, run *domain.Run) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE runs SET state = ?, active_steps = ?, started_at = ?, completed_at = ? WHERE id = ?`,
		string(run.State), run.ActiveStepsString(),
		formatTime(run.StartedAt), formatTime(run.CompletedAt),
		run.ID,
	)
	return err
}

func (s *Store) DeleteRun(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM step_executions WHERE run_id = ?`, id)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM runs WHERE id = ?`, id)
	return err
}

func (s *Store) ListRuns(ctx context.Context) ([]*domain.Run, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, workflow_name, state, active_steps, started_at, completed_at, project_dir FROM runs ORDER BY started_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var runs []*domain.Run
	for rows.Next() {
		run := &domain.Run{}
		var activeSteps, startedAt, completedAt string
		if err := rows.Scan(&run.ID, &run.WorkflowName, &run.State, &activeSteps, &startedAt, &completedAt, &run.ProjectDir); err != nil {
			return nil, err
		}
		run.SetActiveStepsFromString(activeSteps)
		run.StartedAt = parseTime(startedAt)
		run.CompletedAt = parseTime(completedAt)
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

func (s *Store) SaveCapture(ctx context.Context, runID string, exec *domain.StepExecution) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO step_executions (run_id, step_name, result, started_at, completed_at, logs, git_ref, prompt_text, agent_output, attempt_number)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		runID, exec.StepName, exec.Result,
		formatTime(exec.StartedAt), formatTime(exec.CompletedAt),
		exec.Logs, exec.GitRef, exec.PromptText, exec.AgentOutput, exec.AttemptNumber,
	)
	return err
}

func (s *Store) GetCaptures(ctx context.Context, runID string) ([]*domain.StepExecution, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT step_name, result, started_at, completed_at, COALESCE(logs,''), COALESCE(git_ref,''), COALESCE(prompt_text,''), COALESCE(agent_output,''), COALESCE(attempt_number,0)
		 FROM step_executions WHERE run_id = ? ORDER BY id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var execs []*domain.StepExecution
	for rows.Next() {
		e := &domain.StepExecution{}
		var startedAt, completedAt string
		if err := rows.Scan(&e.StepName, &e.Result, &startedAt, &completedAt, &e.Logs, &e.GitRef, &e.PromptText, &e.AgentOutput, &e.AttemptNumber); err != nil {
			return nil, err
		}
		e.StartedAt = parseTime(startedAt)
		e.CompletedAt = parseTime(completedAt)
		execs = append(execs, e)
	}
	return execs, rows.Err()
}

func (s *Store) SaveEvolution(ctx context.Context, entry *ports.EvolutionEntry) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO evolution_log (id, project_dir, workflow_name, trigger_run_id, created_at, classification, changes_json, knowledge_delta)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		entry.ID, entry.ProjectDir, entry.WorkflowName, entry.TriggerRunID,
		formatTime(entry.CreatedAt), entry.Classification, entry.ChangesJSON, entry.KnowledgeDelta,
	)
	return err
}

func (s *Store) GetLastEvolution(ctx context.Context, projectDir, workflowName string) (*ports.EvolutionEntry, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, project_dir, workflow_name, trigger_run_id, created_at, COALESCE(classification,''), changes_json, COALESCE(knowledge_delta,'')
		 FROM evolution_log WHERE project_dir = ? AND workflow_name = ? ORDER BY created_at DESC LIMIT 1`,
		projectDir, workflowName)

	entry := &ports.EvolutionEntry{}
	var createdAt string
	err := row.Scan(&entry.ID, &entry.ProjectDir, &entry.WorkflowName, &entry.TriggerRunID,
		&createdAt, &entry.Classification, &entry.ChangesJSON, &entry.KnowledgeDelta)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	entry.CreatedAt = parseTime(createdAt)
	return entry, nil
}

func (s *Store) ListRunsSince(ctx context.Context, projectDir, workflowName, sinceRunID string) ([]*domain.Run, error) {
	var rows *sql.Rows
	var err error

	if sinceRunID == "" {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, workflow_name, state, active_steps, started_at, completed_at, project_dir
			 FROM runs WHERE project_dir = ? AND workflow_name = ? ORDER BY started_at ASC`,
			projectDir, workflowName)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, workflow_name, state, active_steps, started_at, completed_at, project_dir
			 FROM runs WHERE project_dir = ? AND workflow_name = ? AND started_at > (SELECT started_at FROM runs WHERE id = ?)
			 ORDER BY started_at ASC`,
			projectDir, workflowName, sinceRunID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var runs []*domain.Run
	for rows.Next() {
		run := &domain.Run{}
		var activeSteps, startedAt, completedAt string
		if err := rows.Scan(&run.ID, &run.WorkflowName, &run.State, &activeSteps, &startedAt, &completedAt, &run.ProjectDir); err != nil {
			return nil, err
		}
		run.SetActiveStepsFromString(activeSteps)
		run.StartedAt = parseTime(startedAt)
		run.CompletedAt = parseTime(completedAt)
		runs = append(runs, run)
	}
	return runs, rows.Err()
}


func (s *Store) FailPendingRuns(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE runs SET state = 'failed', completed_at = ? WHERE state = 'pending'`,
		formatTime(time.Now()),
	)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
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
