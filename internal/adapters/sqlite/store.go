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
		`ALTER TABLE runs ADD COLUMN task_title TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE step_executions ADD COLUMN input_tokens INTEGER DEFAULT 0`,
		`ALTER TABLE step_executions ADD COLUMN output_tokens INTEGER DEFAULT 0`,
		`ALTER TABLE step_executions ADD COLUMN agent_name TEXT DEFAULT ''`,
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

	// v2: Task-oriented schema migration — creates tasks/attempts tables
	// and adds attempt_id column. Data migration (creating task/attempt
	// records, moving log files) happens per-project via MigrateProjectLogs.
	if err := migrateV2Schema(db); err != nil {
		return fmt.Errorf("v2 schema migration: %w", err)
	}

	// v3: Recreate runs table with pk autoincrement + UNIQUE(attempt_id, id)
	// so run IDs are attempt-scoped (just the workflow name) rather than
	// globally unique with a random prefix.
	if err := migrateRunsCompositeKey(db); err != nil {
		return fmt.Errorf("v3 runs composite key migration: %w", err)
	}

	// v4: Add previous_attempt_id to attempts for resume lineage tracing.
	// Idempotent — ignored if column already exists.
	db.Exec(`ALTER TABLE attempts ADD COLUMN previous_attempt_id TEXT NOT NULL DEFAULT ''`)

	_, errAL := db.Exec(`CREATE TABLE IF NOT EXISTS attempt_logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		attempt_id TEXT NOT NULL,
		task_id TEXT NOT NULL,
		file_type TEXT NOT NULL,
		file_path TEXT NOT NULL,
		file_size INTEGER,
		created_at TEXT NOT NULL,
		FOREIGN KEY (attempt_id) REFERENCES attempts(id)
	)`)
	if errAL != nil {
		return errAL
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
		`INSERT INTO runs (id, workflow_name, state, active_steps, started_at, completed_at, project_dir, error_message, container_id, base_sha, container_kept, title, is_host, parent_run_id, task_id, task_title, attempt_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		run.ID, run.WorkflowName, string(run.State), run.ActiveStepsString(),
		formatTime(run.StartedAt), formatTime(run.CompletedAt), run.ProjectDir, truncateErrorMessage(run.ErrorMessage), run.ContainerID, run.BaseSHA, boolToInt(run.ContainerKept), run.Title, boolToInt(run.IsHost), run.ParentRunID, run.TaskID, run.TaskTitle, run.AttemptID,
	)
	return err
}

// runSelectCols is the standard column list for scanning a Run row.
const runSelectCols = `pk, id, workflow_name, state, active_steps, started_at, completed_at, project_dir, COALESCE(error_message,''), COALESCE(container_id,''), COALESCE(base_sha,''), COALESCE(container_kept,0), COALESCE(title,''), COALESCE(is_host,0), COALESCE(parent_run_id,''), COALESCE(task_id,''), COALESCE(task_title,''), COALESCE(attempt_id,'')`

// scanRun scans a single row into a *domain.Run.
func scanRun(scanner interface{ Scan(...any) error }) (*domain.Run, error) {
	run := &domain.Run{}
	var activeSteps, startedAt, completedAt string
	var containerKept, isHost int
	err := scanner.Scan(&run.PK, &run.ID, &run.WorkflowName, &run.State, &activeSteps, &startedAt, &completedAt, &run.ProjectDir, &run.ErrorMessage, &run.ContainerID, &run.BaseSHA, &containerKept, &run.Title, &isHost, &run.ParentRunID, &run.TaskID, &run.TaskTitle, &run.AttemptID)
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
		`SELECT `+runSelectCols+` FROM runs WHERE id = ? ORDER BY pk DESC LIMIT 1`, id)

	run, err := scanRun(row)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("run %q not found", id)
	}
	return run, err
}

// GetRunByAttempt returns the run with the given attempt ID and run ID.
// This is the preferred lookup when the attempt is known, since run IDs are
// only unique within an attempt.
func (s *Store) GetRunByAttempt(ctx context.Context, attemptID, id string) (*domain.Run, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+runSelectCols+` FROM runs WHERE attempt_id = ? AND id = ? LIMIT 1`, attemptID, id)

	run, err := scanRun(row)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("run %q for attempt %q not found", id, attemptID)
	}
	return run, err
}

func (s *Store) UpdateRun(ctx context.Context, run *domain.Run) error {
	// Use pk when available (populated after reads); fall back to
	// attempt_id+id composite which is unique by schema constraint.
	if run.PK != 0 {
		_, err := s.db.ExecContext(ctx,
			`UPDATE runs SET state = ?, active_steps = ?, started_at = ?, completed_at = ?, error_message = ?, container_id = ?, base_sha = ?, container_kept = ?, title = ?, is_host = ?, parent_run_id = ?, task_id = ?, task_title = ?, attempt_id = ? WHERE pk = ?`,
			string(run.State), run.ActiveStepsString(),
			formatTime(run.StartedAt), formatTime(run.CompletedAt),
			truncateErrorMessage(run.ErrorMessage), run.ContainerID, run.BaseSHA, boolToInt(run.ContainerKept), run.Title, boolToInt(run.IsHost), run.ParentRunID, run.TaskID, run.TaskTitle, run.AttemptID, run.PK,
		)
		return err
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE runs SET state = ?, active_steps = ?, started_at = ?, completed_at = ?, error_message = ?, container_id = ?, base_sha = ?, container_kept = ?, title = ?, is_host = ?, parent_run_id = ?, task_id = ?, task_title = ?, attempt_id = ? WHERE attempt_id = ? AND id = ?`,
		string(run.State), run.ActiveStepsString(),
		formatTime(run.StartedAt), formatTime(run.CompletedAt),
		truncateErrorMessage(run.ErrorMessage), run.ContainerID, run.BaseSHA, boolToInt(run.ContainerKept), run.Title, boolToInt(run.IsHost), run.ParentRunID, run.TaskID, run.TaskTitle, run.AttemptID,
		run.AttemptID, run.ID,
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
	if filter.AttemptID != "" {
		query += ` AND attempt_id = ?`
		args = append(args, filter.AttemptID)
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
	var inputTokens, outputTokens int64
	var agentName string
	if exec.Usage != nil {
		inputTokens = exec.Usage.InputTokens
		outputTokens = exec.Usage.OutputTokens
		agentName = exec.Usage.AgentName
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO step_executions (run_id, step_name, result, started_at, completed_at, logs, git_ref, input_tokens, output_tokens, agent_name)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		runID, exec.StepName, exec.Result,
		formatTime(exec.StartedAt), formatTime(exec.CompletedAt),
		exec.Logs, exec.GitRef, inputTokens, outputTokens, agentName,
	)
	return err
}

func (s *Store) GetCaptures(ctx context.Context, runID string) ([]*domain.StepExecution, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT step_name, result, started_at, completed_at, COALESCE(logs,''), COALESCE(git_ref,''), COALESCE(input_tokens,0), COALESCE(output_tokens,0), COALESCE(agent_name,'')
		 FROM step_executions WHERE run_id = ? ORDER BY id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var execs []*domain.StepExecution
	for rows.Next() {
		e := &domain.StepExecution{}
		var startedAt, completedAt string
		var inputTokens, outputTokens int64
		var agentName string
		if err := rows.Scan(&e.StepName, &e.Result, &startedAt, &completedAt, &e.Logs, &e.GitRef, &inputTokens, &outputTokens, &agentName); err != nil {
			return nil, err
		}
		e.StartedAt = parseTime(startedAt)
		e.CompletedAt = parseTime(completedAt)
		if inputTokens > 0 || outputTokens > 0 || agentName != "" {
			e.Usage = &domain.TokenUsage{
				InputTokens:  inputTokens,
				OutputTokens: outputTokens,
				AgentName:    agentName,
			}
		}
		execs = append(execs, e)
	}
	return execs, rows.Err()
}

func (s *Store) QueryUsage(ctx context.Context, q ports.UsageQuery) ([]domain.UsageSummary, error) {
	sinceStr := formatTime(q.Since)
	untilStr := formatTime(q.Until)

	rows, err := s.db.QueryContext(ctx,
		`SELECT
			COALESCE(se.agent_name, '') AS agent_name,
			SUM(se.input_tokens) AS input_tokens,
			SUM(se.output_tokens) AS output_tokens
		FROM step_executions se
		JOIN runs r ON se.run_id = r.id
		WHERE (? = '' OR r.project_dir = ?)
		  AND (? = '' OR se.agent_name = ?)
		  AND (? = '' OR se.completed_at >= ?)
		  AND (? = '' OR se.completed_at <= ?)
		GROUP BY COALESCE(se.agent_name, '')`,
		q.ProjectDir, q.ProjectDir,
		q.AgentName, q.AgentName,
		sinceStr, sinceStr,
		untilStr, untilStr,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Compute window seconds for burn rate calculation.
	var windowSeconds int64
	if !q.Since.IsZero() {
		until := q.Until
		if until.IsZero() {
			until = time.Now()
		}
		windowSeconds = int64(until.Sub(q.Since).Seconds())
	}

	var summaries []domain.UsageSummary
	for rows.Next() {
		var summary domain.UsageSummary
		if err := rows.Scan(&summary.AgentName, &summary.InputTokens, &summary.OutputTokens); err != nil {
			return nil, err
		}
		summary.TotalTokens = summary.InputTokens + summary.OutputTokens
		summary.WindowSeconds = windowSeconds
		if windowSeconds > 0 {
			summary.BurnRate = float64(summary.TotalTokens) / (float64(windowSeconds) / 3600.0)
		}
		summaries = append(summaries, summary)
	}
	return summaries, rows.Err()
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


func (s *Store) SaveTask(ctx context.Context, task *domain.Task) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO tasks (id, title, source, project_dir, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		task.ID, task.Title, string(task.Source), task.ProjectDir, formatTime(task.CreatedAt),
	)
	return err
}

func (s *Store) GetTask(ctx context.Context, id string) (*domain.Task, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, title, source, project_dir, created_at FROM tasks WHERE id = ?`, id)

	task := &domain.Task{}
	var createdAt string
	err := row.Scan(&task.ID, &task.Title, &task.Source, &task.ProjectDir, &createdAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("task %q not found", id)
	}
	if err != nil {
		return nil, err
	}
	task.CreatedAt = parseTime(createdAt)

	attempts, err := s.ListAttempts(ctx, id)
	if err != nil {
		return nil, err
	}
	task.Attempts = attempts
	task.Status = task.DeriveStatus()
	return task, nil
}

func (s *Store) ListTasks(ctx context.Context, projectDir string) ([]*domain.Task, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if projectDir == "" {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, title, source, project_dir, created_at FROM tasks ORDER BY created_at DESC`)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, title, source, project_dir, created_at FROM tasks WHERE project_dir = ? ORDER BY created_at DESC`,
			projectDir)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []*domain.Task
	for rows.Next() {
		task := &domain.Task{}
		var createdAt string
		if err := rows.Scan(&task.ID, &task.Title, &task.Source, &task.ProjectDir, &createdAt); err != nil {
			return nil, err
		}
		task.CreatedAt = parseTime(createdAt)
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Populate attempts for each task
	for _, task := range tasks {
		attempts, err := s.ListAttempts(ctx, task.ID)
		if err != nil {
			return nil, err
		}
		task.Attempts = attempts
		task.Status = task.DeriveStatus()
	}
	return tasks, nil
}

func (s *Store) SaveAttempt(ctx context.Context, attempt *domain.Attempt) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO attempts (id, task_id, started_at, ended_at, result, project_dir, previous_attempt_id)
		 VALUES (?, ?, ?, ?, ?, (SELECT project_dir FROM tasks WHERE id = ?), ?)`,
		attempt.ID, attempt.TaskID, formatTime(attempt.StartedAt),
		nullIfEmptyTime(attempt.EndedAt), string(attempt.Result), attempt.TaskID,
		attempt.PreviousAttemptID,
	)
	return err
}

func (s *Store) GetAttempt(ctx context.Context, id string) (*domain.Attempt, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, task_id, started_at, COALESCE(ended_at,''), result, COALESCE(previous_attempt_id,'') FROM attempts WHERE id = ?`, id)

	attempt := &domain.Attempt{}
	var startedAt, endedAt string
	err := row.Scan(&attempt.ID, &attempt.TaskID, &startedAt, &endedAt, &attempt.Result, &attempt.PreviousAttemptID)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("attempt %q not found", id)
	}
	if err != nil {
		return nil, err
	}
	attempt.StartedAt = parseTime(startedAt)
	attempt.EndedAt = parseTime(endedAt)
	return attempt, nil
}

func (s *Store) ListAttempts(ctx context.Context, taskID string) ([]*domain.Attempt, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, task_id, started_at, COALESCE(ended_at,''), result, COALESCE(previous_attempt_id,'')
		 FROM attempts WHERE task_id = ? ORDER BY started_at ASC`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var attempts []*domain.Attempt
	for rows.Next() {
		attempt := &domain.Attempt{}
		var startedAt, endedAt string
		if err := rows.Scan(&attempt.ID, &attempt.TaskID, &startedAt, &endedAt, &attempt.Result, &attempt.PreviousAttemptID); err != nil {
			return nil, err
		}
		attempt.StartedAt = parseTime(startedAt)
		attempt.EndedAt = parseTime(endedAt)
		attempts = append(attempts, attempt)
	}
	return attempts, rows.Err()
}

func (s *Store) SaveAttemptLog(ctx context.Context, entry *ports.AttemptLogEntry) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO attempt_logs (attempt_id, task_id, file_type, file_path, file_size, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		entry.AttemptID, entry.TaskID, entry.FileType, entry.FilePath, entry.FileSize,
		formatTime(entry.CreatedAt),
	)
	return err
}

func (s *Store) GetAttemptLogs(ctx context.Context, attemptID string) ([]*ports.AttemptLogEntry, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, attempt_id, task_id, file_type, file_path, COALESCE(file_size,0), created_at
		 FROM attempt_logs WHERE attempt_id = ? ORDER BY id`, attemptID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []*ports.AttemptLogEntry
	for rows.Next() {
		e := &ports.AttemptLogEntry{}
		var createdAt string
		if err := rows.Scan(&e.ID, &e.AttemptID, &e.TaskID, &e.FileType, &e.FilePath, &e.FileSize, &createdAt); err != nil {
			return nil, err
		}
		e.CreatedAt = parseTime(createdAt)
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

func (s *Store) FailStaleAttempts(ctx context.Context) (int64, error) {
	now := formatTime(time.Now())
	res, err := s.db.ExecContext(ctx,
		`UPDATE attempts SET result = 'failed', ended_at = ?
		 WHERE result = 'running'`,
		now,
	)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
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

func nullIfEmptyTime(t time.Time) interface{} {
	if t.IsZero() {
		return nil
	}
	return t.Format(time.RFC3339Nano)
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
