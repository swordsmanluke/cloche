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
		`ALTER TABLE runs ADD COLUMN error_message TEXT`,
		`ALTER TABLE runs ADD COLUMN container_id TEXT`,
		`ALTER TABLE runs ADD COLUMN base_sha TEXT`,
		`ALTER TABLE runs ADD COLUMN container_kept INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE runs ADD COLUMN title TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE runs ADD COLUMN is_host INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE runs ADD COLUMN parent_run_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE runs ADD COLUMN task_id TEXT NOT NULL DEFAULT ''`,
	}
	for _, stmt := range alterStmts {
		db.Exec(stmt) // ignore "duplicate column" errors
	}

	_, errLog := db.Exec(`CREATE TABLE IF NOT EXISTS log_files (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		run_id TEXT NOT NULL,
		step_name TEXT,
		file_type TEXT NOT NULL,
		file_path TEXT NOT NULL,
		file_size INTEGER,
		created_at TEXT NOT NULL,
		FOREIGN KEY (run_id) REFERENCES runs(id)
	)`)
	if errLog != nil {
		return errLog
	}

	_, errMQ := db.Exec(`CREATE TABLE IF NOT EXISTS merge_queue (
		run_id TEXT PRIMARY KEY,
		branch TEXT NOT NULL,
		project TEXT NOT NULL,
		status TEXT NOT NULL DEFAULT 'pending',
		enqueued_at TEXT NOT NULL,
		completed_at TEXT
	)`)
	if errMQ != nil {
		return errMQ
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
		`INSERT INTO runs (id, workflow_name, state, active_steps, started_at, completed_at, project_dir, error_message, container_id, base_sha, container_kept, title, is_host, parent_run_id, task_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		run.ID, run.WorkflowName, string(run.State), run.ActiveStepsString(),
		formatTime(run.StartedAt), formatTime(run.CompletedAt), run.ProjectDir, truncateErrorMessage(run.ErrorMessage), run.ContainerID, run.BaseSHA, boolToInt(run.ContainerKept), run.Title, boolToInt(run.IsHost), run.ParentRunID, run.TaskID,
	)
	return err
}

// runSelectCols is the standard column list for scanning a Run row.
const runSelectCols = `id, workflow_name, state, active_steps, started_at, completed_at, project_dir, COALESCE(error_message,''), COALESCE(container_id,''), COALESCE(base_sha,''), COALESCE(container_kept,0), COALESCE(title,''), COALESCE(is_host,0), COALESCE(parent_run_id,''), COALESCE(task_id,'')`

// scanRun scans a single row into a *domain.Run.
func scanRun(scanner interface{ Scan(...any) error }) (*domain.Run, error) {
	run := &domain.Run{}
	var activeSteps, startedAt, completedAt string
	var containerKept, isHost int
	err := scanner.Scan(&run.ID, &run.WorkflowName, &run.State, &activeSteps, &startedAt, &completedAt, &run.ProjectDir, &run.ErrorMessage, &run.ContainerID, &run.BaseSHA, &containerKept, &run.Title, &isHost, &run.ParentRunID, &run.TaskID)
	if err != nil {
		return nil, err
	}
	run.SetActiveStepsFromString(activeSteps)
	run.StartedAt = parseTime(startedAt)
	run.CompletedAt = parseTime(completedAt)
	run.ContainerKept = containerKept != 0
	run.IsHost = isHost != 0
	return run, nil
}

func (s *Store) GetRun(ctx context.Context, id string) (*domain.Run, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+runSelectCols+` FROM runs WHERE id = ?`, id)

	run, err := scanRun(row)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("run %q not found", id)
	}
	return run, err
}

func (s *Store) UpdateRun(ctx context.Context, run *domain.Run) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE runs SET state = ?, active_steps = ?, started_at = ?, completed_at = ?, error_message = ?, container_id = ?, base_sha = ?, container_kept = ?, title = ?, is_host = ?, parent_run_id = ?, task_id = ? WHERE id = ?`,
		string(run.State), run.ActiveStepsString(),
		formatTime(run.StartedAt), formatTime(run.CompletedAt),
		truncateErrorMessage(run.ErrorMessage), run.ContainerID, run.BaseSHA, boolToInt(run.ContainerKept), run.Title, boolToInt(run.IsHost), run.ParentRunID, run.TaskID, run.ID,
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

func (s *Store) ListRuns(ctx context.Context, since time.Time) ([]*domain.Run, error) {
	var rows *sql.Rows
	var err error
	if since.IsZero() {
		rows, err = s.db.QueryContext(ctx,
			`SELECT `+runSelectCols+` FROM runs ORDER BY CASE WHEN state = 'running' THEN 0 ELSE 1 END, started_at DESC`)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT `+runSelectCols+` FROM runs WHERE started_at >= ? ORDER BY CASE WHEN state = 'running' THEN 0 ELSE 1 END, started_at DESC`,
			formatTime(since))
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRuns(rows)
}

// scanRuns scans all rows into a []*domain.Run slice.
func scanRuns(rows *sql.Rows) ([]*domain.Run, error) {
	var runs []*domain.Run
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

func (s *Store) ListRunsByProject(ctx context.Context, projectDir string, since time.Time) ([]*domain.Run, error) {
	var rows *sql.Rows
	var err error
	if since.IsZero() {
		rows, err = s.db.QueryContext(ctx,
			`SELECT `+runSelectCols+` FROM runs WHERE project_dir = ? ORDER BY CASE WHEN state = 'running' THEN 0 ELSE 1 END, started_at DESC`,
			projectDir)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT `+runSelectCols+` FROM runs WHERE project_dir = ? AND started_at >= ? ORDER BY CASE WHEN state = 'running' THEN 0 ELSE 1 END, started_at DESC`,
			projectDir, formatTime(since))
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRuns(rows)
}

func (s *Store) ListRunsFiltered(ctx context.Context, filter domain.RunListFilter) ([]*domain.Run, error) {
	query := `SELECT ` + runSelectCols + ` FROM runs WHERE 1=1`
	var args []interface{}

	if filter.ProjectDir != "" {
		query += ` AND project_dir = ?`
		args = append(args, filter.ProjectDir)
	}
	if filter.State != "" {
		query += ` AND state = ?`
		args = append(args, string(filter.State))
	}
	if filter.TaskID != "" {
		query += ` AND task_id = ?`
		args = append(args, filter.TaskID)
	}
	if !filter.Since.IsZero() {
		query += ` AND (completed_at >= ? OR state IN ('running', 'pending'))`
		args = append(args, formatTime(filter.Since))
	}

	query += ` ORDER BY CASE WHEN state = 'running' THEN 0 ELSE 1 END, started_at DESC`

	if filter.Limit > 0 {
		query += fmt.Sprintf(` LIMIT %d`, filter.Limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRuns(rows)
}

func (s *Store) ListProjects(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT project_dir FROM runs WHERE project_dir != '' ORDER BY project_dir`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var projects []string
	for rows.Next() {
		var dir string
		if err := rows.Scan(&dir); err != nil {
			return nil, err
		}
		projects = append(projects, dir)
	}
	return projects, rows.Err()
}

func (s *Store) ListChildRuns(ctx context.Context, parentRunID string) ([]*domain.Run, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+runSelectCols+` FROM runs WHERE parent_run_id = ? ORDER BY started_at ASC`,
		parentRunID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRuns(rows)
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
		`SELECT step_name, result, started_at, completed_at, COALESCE(logs,''), COALESCE(git_ref,'')
		 FROM step_executions WHERE run_id = ? ORDER BY id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var execs []*domain.StepExecution
	for rows.Next() {
		e := &domain.StepExecution{}
		var startedAt, completedAt string
		if err := rows.Scan(&e.StepName, &e.Result, &startedAt, &completedAt, &e.Logs, &e.GitRef); err != nil {
			return nil, err
		}
		e.StartedAt = parseTime(startedAt)
		e.CompletedAt = parseTime(completedAt)
		execs = append(execs, e)
	}
	return execs, rows.Err()
}

func (s *Store) SaveLogFile(ctx context.Context, entry *ports.LogFileEntry) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO log_files (run_id, step_name, file_type, file_path, file_size, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		entry.RunID, entry.StepName, entry.FileType, entry.FilePath, entry.FileSize,
		formatTime(entry.CreatedAt),
	)
	return err
}

func (s *Store) GetLogFiles(ctx context.Context, runID string) ([]*ports.LogFileEntry, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, run_id, COALESCE(step_name,''), file_type, file_path, COALESCE(file_size,0), created_at
		 FROM log_files WHERE run_id = ? ORDER BY id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanLogFiles(rows)
}

func (s *Store) GetLogFilesByStep(ctx context.Context, runID, stepName string) ([]*ports.LogFileEntry, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, run_id, COALESCE(step_name,''), file_type, file_path, COALESCE(file_size,0), created_at
		 FROM log_files WHERE run_id = ? AND step_name = ? ORDER BY id`, runID, stepName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanLogFiles(rows)
}

func (s *Store) GetLogFileByType(ctx context.Context, runID, fileType string) ([]*ports.LogFileEntry, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, run_id, COALESCE(step_name,''), file_type, file_path, COALESCE(file_size,0), created_at
		 FROM log_files WHERE run_id = ? AND file_type = ? ORDER BY id`, runID, fileType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanLogFiles(rows)
}

func scanLogFiles(rows *sql.Rows) ([]*ports.LogFileEntry, error) {
	var entries []*ports.LogFileEntry
	for rows.Next() {
		e := &ports.LogFileEntry{}
		var createdAt string
		if err := rows.Scan(&e.ID, &e.RunID, &e.StepName, &e.FileType, &e.FilePath, &e.FileSize, &createdAt); err != nil {
			return nil, err
		}
		e.CreatedAt = parseTime(createdAt)
		entries = append(entries, e)
	}
	return entries, rows.Err()
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
			`SELECT `+runSelectCols+`
			 FROM runs WHERE project_dir = ? AND workflow_name = ? ORDER BY started_at ASC`,
			projectDir, workflowName)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT `+runSelectCols+`
			 FROM runs WHERE project_dir = ? AND workflow_name = ? AND started_at > (SELECT started_at FROM runs WHERE id = ?)
			 ORDER BY started_at ASC`,
			projectDir, workflowName, sinceRunID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRuns(rows)
}


func (s *Store) FailStaleRuns(ctx context.Context) (int64, error) {
	now := formatTime(time.Now())
	res, err := s.db.ExecContext(ctx,
		`UPDATE runs SET state = 'failed', completed_at = ?, error_message = 'daemon restarted while run was active'
		 WHERE state IN ('pending', 'running')`,
		now,
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

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

const maxErrorMessageLen = 1000

// truncateErrorMessage caps s to maxErrorMessageLen characters to prevent
// oversized agent output dumps from bloating the database. Full error
// details remain available in step logs.
func truncateErrorMessage(s string) string {
	if len(s) <= maxErrorMessageLen {
		return s
	}
	return s[:maxErrorMessageLen] + "... (truncated)"
}
