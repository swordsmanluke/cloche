package sqlite

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cloche-dev/cloche/internal/domain"
)

// migrateV2Schema creates the tasks, attempts, and _migrations tables and
// adds the attempt_id column to runs. This is a one-shot global migration
// that only touches the database schema.
func migrateV2Schema(db *sql.DB) error {
	db.Exec(`CREATE TABLE IF NOT EXISTS _migrations (
		id TEXT PRIMARY KEY,
		applied_at TEXT NOT NULL
	)`)

	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS tasks (
		id TEXT PRIMARY KEY,
		title TEXT NOT NULL DEFAULT '',
		source TEXT NOT NULL DEFAULT 'external',
		project_dir TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		return fmt.Errorf("creating tasks table: %w", err)
	}

	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS attempts (
		id TEXT PRIMARY KEY,
		task_id TEXT NOT NULL REFERENCES tasks(id),
		started_at TEXT NOT NULL DEFAULT '',
		ended_at TEXT,
		result TEXT NOT NULL DEFAULT 'running',
		project_dir TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		return fmt.Errorf("creating attempts table: %w", err)
	}

	db.Exec(`ALTER TABLE runs ADD COLUMN attempt_id TEXT NOT NULL DEFAULT ''`)

	return nil
}

// migrateRunsCompositeKey recreates the runs table with a pk auto-increment
// primary key and a UNIQUE(attempt_id, id) constraint. This allows run IDs
// to be just the workflow name (attempt-scoped) rather than globally unique.
// Safe to run multiple times — checks for the pk column first.
func migrateRunsCompositeKey(db *sql.DB) error {
	// Check if migration already applied by inspecting the pk column.
	rows, err := db.Query(`PRAGMA table_info(runs)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	hasPK := false
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull int
		var dfltVal sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dfltVal, &pk); err != nil {
			continue
		}
		if name == "pk" {
			hasPK = true
			break
		}
	}
	rows.Close()
	if hasPK {
		return nil // already migrated
	}

	stmts := []string{
		`CREATE TABLE IF NOT EXISTS runs_new (
			pk           INTEGER PRIMARY KEY AUTOINCREMENT,
			id           TEXT NOT NULL,
			workflow_name TEXT NOT NULL,
			state        TEXT NOT NULL,
			active_steps  TEXT,
			started_at   TEXT,
			completed_at  TEXT,
			project_dir  TEXT NOT NULL DEFAULT '',
			error_message TEXT,
			container_id  TEXT,
			base_sha     TEXT,
			container_kept INTEGER NOT NULL DEFAULT 0,
			title        TEXT NOT NULL DEFAULT '',
			is_host      INTEGER NOT NULL DEFAULT 0,
			parent_run_id TEXT NOT NULL DEFAULT '',
			task_id      TEXT NOT NULL DEFAULT '',
			task_title   TEXT NOT NULL DEFAULT '',
			attempt_id   TEXT NOT NULL DEFAULT '',
			UNIQUE(attempt_id, id)
		)`,
		`INSERT OR IGNORE INTO runs_new
			(id, workflow_name, state, active_steps, started_at, completed_at,
			 project_dir, error_message, container_id, base_sha, container_kept,
			 title, is_host, parent_run_id, task_id, task_title, attempt_id)
		 SELECT id, workflow_name, state, active_steps, started_at, completed_at,
			project_dir, COALESCE(error_message,''), COALESCE(container_id,''),
			COALESCE(base_sha,''), COALESCE(container_kept,0), COALESCE(title,''),
			COALESCE(is_host,0), COALESCE(parent_run_id,''), COALESCE(task_id,''),
			COALESCE(task_title,''), COALESCE(attempt_id,'')
		 FROM runs`,
		`DROP TABLE runs`,
		`ALTER TABLE runs_new RENAME TO runs`,
	}

	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("v3 migration step failed: %w", err)
		}
	}
	return nil
}

// migratedProjects tracks which projects have been migrated in this process
// lifetime, avoiding repeated DB checks for the common case.
var (
	migratedProjects   = map[string]bool{}
	migratedProjectsMu sync.Mutex
)

// MigrateProjectLogs migrates run data and log files for a single project.
// It creates task/attempt records for runs that lack them and moves log files
// from .cloche/<run-id>/output/ to .cloche/logs/<task-id>/<attempt-id>/.
// Safe to call repeatedly — skips projects that are already migrated.
func (s *Store) MigrateProjectLogs(projectDir string) error {
	if projectDir == "" {
		return nil
	}

	// Fast path: already migrated in this process lifetime.
	migratedProjectsMu.Lock()
	if migratedProjects[projectDir] {
		migratedProjectsMu.Unlock()
		return nil
	}
	migratedProjectsMu.Unlock()

	migrationID := "v2-logs:" + projectDir
	var count int
	row := s.db.QueryRow(`SELECT COUNT(*) FROM _migrations WHERE id = ?`, migrationID)
	if err := row.Scan(&count); err == nil && count > 0 {
		migratedProjectsMu.Lock()
		migratedProjects[projectDir] = true
		migratedProjectsMu.Unlock()
		return nil
	}

	if err := migrateProjectRuns(s.db, projectDir); err != nil {
		return err
	}

	// Clean up old .cloche/<run-id>/ directories that may still contain
	// orphaned runtime state (prompt.txt, context.json) from before the
	// move to .cloche/runs/<task-id>/.
	cleanupOldRunDirs(s.db, projectDir)

	s.db.Exec(`INSERT OR IGNORE INTO _migrations (id, applied_at) VALUES (?, ?)`,
		migrationID, time.Now().UTC().Format(time.RFC3339))

	migratedProjectsMu.Lock()
	migratedProjects[projectDir] = true
	migratedProjectsMu.Unlock()

	return nil
}

// migrateProjectRuns creates task/attempt records and moves log files for
// all runs belonging to the given project that haven't been migrated yet
// (i.e., runs with an empty attempt_id).
func migrateProjectRuns(db *sql.DB, projectDir string) error {
	rows, err := db.Query(`SELECT id, workflow_name, state, started_at, completed_at,
		project_dir, task_id, task_title, is_host, parent_run_id
		FROM runs WHERE project_dir = ? AND attempt_id = ''
		ORDER BY started_at ASC`, projectDir)
	if err != nil {
		return fmt.Errorf("querying unmigrated runs: %w", err)
	}
	defer rows.Close()

	type runRecord struct {
		id          string
		workflow    string
		state       string
		startedAt   string
		completedAt string
		projectDir  string
		taskID      string
		taskTitle   string
		isHost      bool
		parentRunID string
	}

	var allRuns []runRecord
	for rows.Next() {
		var r runRecord
		var isHost int
		var completedAt sql.NullString
		if err := rows.Scan(&r.id, &r.workflow, &r.state, &r.startedAt,
			&completedAt, &r.projectDir, &r.taskID, &r.taskTitle,
			&isHost, &r.parentRunID); err != nil {
			log.Printf("v2 migration [%s]: skipping run (scan error): %v", projectDir, err)
			continue
		}
		r.isHost = isHost == 1
		if completedAt.Valid {
			r.completedAt = completedAt.String
		}
		allRuns = append(allRuns, r)
	}

	if len(allRuns) == 0 {
		return nil
	}

	// Build parent→child mapping
	childrenOf := map[string][]string{}
	runByID := map[string]*runRecord{}
	for i := range allRuns {
		runByID[allRuns[i].id] = &allRuns[i]
		if allRuns[i].parentRunID != "" {
			childrenOf[allRuns[i].parentRunID] = append(childrenOf[allRuns[i].parentRunID], allRuns[i].id)
		}
	}

	// Identify top-level runs (no parent, or parent not in this batch)
	var topLevel []runRecord
	for _, r := range allRuns {
		if r.parentRunID == "" || runByID[r.parentRunID] == nil {
			topLevel = append(topLevel, r)
		}
	}

	sort.SliceStable(topLevel, func(i, j int) bool {
		return topLevel[i].startedAt < topLevel[j].startedAt
	})

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("starting transaction: %w", err)
	}
	defer tx.Rollback()

	taskCreated := map[string]bool{}

	for _, r := range topLevel {
		taskID := r.taskID
		taskTitle := r.taskTitle
		taskSource := "external"

		if taskID == "" {
			taskID = fmt.Sprintf("user-%s", domain.GenerateAttemptID())
			taskSource = "user-initiated"
			if taskTitle == "" {
				taskTitle = r.workflow + " (user-initiated)"
			}
		}

		if !taskCreated[taskID] {
			_, err := tx.Exec(`INSERT OR IGNORE INTO tasks (id, title, source, project_dir, created_at)
				VALUES (?, ?, ?, ?, ?)`,
				taskID, taskTitle, taskSource, r.projectDir, r.startedAt)
			if err != nil {
				log.Printf("v2 migration [%s]: failed to create task %s: %v", projectDir, taskID, err)
				continue
			}
			taskCreated[taskID] = true
		}

		attemptID := domain.GenerateAttemptID()
		attemptResult := mapRunStateToAttemptResult(r.state)

		_, err := tx.Exec(`INSERT INTO attempts (id, task_id, started_at, ended_at, result, project_dir)
			VALUES (?, ?, ?, ?, ?, ?)`,
			attemptID, taskID, r.startedAt, nullIfEmpty(r.completedAt), attemptResult, r.projectDir)
		if err != nil {
			log.Printf("v2 migration [%s]: failed to create attempt for run %s: %v", projectDir, r.id, err)
			continue
		}

		runIDs := []string{r.id}
		for _, childID := range childrenOf[r.id] {
			runIDs = append(runIDs, childID)
		}

		for _, runID := range runIDs {
			tx.Exec(`UPDATE runs SET attempt_id = ?, task_id = ? WHERE id = ?`, attemptID, taskID, runID)
		}

		moveRunLogs(r.projectDir, r.id, taskID, attemptID, r.workflow)
		for _, childID := range childrenOf[r.id] {
			childRun := runByID[childID]
			if childRun != nil {
				moveRunLogs(childRun.projectDir, childID, taskID, attemptID, childRun.workflow)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing migration: %w", err)
	}

	if len(topLevel) > 0 {
		log.Printf("v2 migration [%s]: migrated %d runs into tasks and attempts", projectDir, len(topLevel))
	}
	return nil
}

// cleanupOldRunDirs scans .cloche/ for directories that look like old
// run-ID directories and contain only orphaned runtime state files
// (prompt.txt, context.json). These were created by the old runtime state
// layout before the move to .cloche/runs/<task-id>/.
func cleanupOldRunDirs(db *sql.DB, projectDir string) {
	clocheDir := filepath.Join(projectDir, ".cloche")
	entries, err := os.ReadDir(clocheDir)
	if err != nil {
		log.Printf("v2 migration [%s]: failed to read .cloche dir: %v", projectDir, err)
		return
	}

	// Known non-run directories to skip.
	skip := map[string]bool{
		"logs": true, "runs": true, "prompts": true, "scripts": true,
		"overrides": true, "output": true, "attempt_count": true,
		"evolution": true, "version": true, "config": true,
	}

	removed := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		// Skip known config/data directories and hidden dirs.
		if skip[name] || strings.HasPrefix(name, ".") {
			continue
		}
		// Skip directories that don't look like run IDs (must contain a dash).
		if !strings.Contains(name, "-") {
			continue
		}

		dirPath := filepath.Join(clocheDir, name)
		children, err := os.ReadDir(dirPath)
		if err != nil {
			continue
		}
		// Only remove if it doesn't contain an output/ directory
		// (which would have unmigrated log files).
		hasOutput := false
		for _, c := range children {
			if c.IsDir() && c.Name() == "output" {
				hasOutput = true
				break
			}
		}
		if !hasOutput {
			os.RemoveAll(dirPath)
			removed++
		}
	}
	if removed > 0 {
		log.Printf("v2 migration [%s]: cleaned up %d old run directories", projectDir, removed)
	}
}

// moveRunLogs moves log files from .cloche/<run-id>/output/ to
// .cloche/logs/<task-id>/<attempt-id>/. Files are renamed from
// <step>.log to <workflow>-<step>.log.
func moveRunLogs(projectDir, runID, taskID, attemptID, workflow string) {
	srcDir := filepath.Join(projectDir, ".cloche", runID, "output")
	if _, err := os.Stat(srcDir); err != nil {
		return // no output directory
	}

	dstDir := filepath.Join(projectDir, ".cloche", "logs", taskID, attemptID)
	if err := os.MkdirAll(dstDir, 0755); err != nil {
		log.Printf("v2 migration: failed to create log dir %s: %v", dstDir, err)
		return
	}

	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		srcPath := filepath.Join(srcDir, entry.Name())
		// Rename: full.log stays as full.log, others get workflow prefix
		dstName := entry.Name()
		if dstName != "full.log" && dstName != "container.log" {
			// step.log → workflow-step.log
			dstName = workflow + "-" + dstName
		}
		dstPath := filepath.Join(dstDir, dstName)

		// Copy (not rename, to handle cross-device) then remove
		data, err := os.ReadFile(srcPath)
		if err != nil {
			continue
		}
		if err := os.WriteFile(dstPath, data, 0644); err != nil {
			log.Printf("v2 migration: failed to write %s: %v", dstPath, err)
			continue
		}
	}

	// Remove old output directory after successful copy
	os.RemoveAll(srcDir)

	// Remove old run directory if now empty
	runDir := filepath.Join(projectDir, ".cloche", runID)
	removeIfEmpty(runDir)
}

// removeIfEmpty removes a directory if it contains no files.
func removeIfEmpty(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	if len(entries) == 0 {
		os.Remove(dir)
	}
}

func mapRunStateToAttemptResult(state string) string {
	switch state {
	case "succeeded":
		return "succeeded"
	case "failed":
		return "failed"
	case "cancelled":
		return "cancelled"
	case "running", "pending":
		return "running"
	default:
		return "failed"
	}
}

func nullIfEmpty(s string) interface{} {
	if s == "" {
		return nil
	}
	// Also treat zero time as null
	if strings.HasPrefix(s, "0001-01-01") {
		return nil
	}
	return s
}
