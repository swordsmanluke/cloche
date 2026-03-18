package sqlite

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cloche-dev/cloche/internal/domain"
)

// migrateV2 creates the tasks and attempts tables, populates them from
// existing run records, and moves log files to the new directory layout.
// It is idempotent: a _migrations table tracks whether it has already run.
func migrateV2(db *sql.DB) error {
	// Create migration tracking table
	db.Exec(`CREATE TABLE IF NOT EXISTS _migrations (
		id TEXT PRIMARY KEY,
		applied_at TEXT NOT NULL
	)`)

	// Check if already applied
	var count int
	row := db.QueryRow(`SELECT COUNT(*) FROM _migrations WHERE id = 'v2-task-oriented'`)
	if err := row.Scan(&count); err == nil && count > 0 {
		return nil // already migrated
	}

	// Create new tables
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

	// Add attempt_id column to runs (idempotent)
	db.Exec(`ALTER TABLE runs ADD COLUMN attempt_id TEXT NOT NULL DEFAULT ''`)

	// Query all existing runs, grouped by task_id
	rows, err := db.Query(`SELECT id, workflow_name, state, started_at, completed_at,
		project_dir, task_id, task_title, is_host, parent_run_id
		FROM runs ORDER BY started_at ASC`)
	if err != nil {
		return fmt.Errorf("querying runs: %w", err)
	}
	defer rows.Close()

	type runRecord struct {
		id           string
		workflow     string
		state        string
		startedAt    string
		completedAt  string
		projectDir   string
		taskID       string
		taskTitle    string
		isHost       bool
		parentRunID  string
	}

	var allRuns []runRecord
	for rows.Next() {
		var r runRecord
		var isHost int
		var completedAt sql.NullString
		if err := rows.Scan(&r.id, &r.workflow, &r.state, &r.startedAt,
			&completedAt, &r.projectDir, &r.taskID, &r.taskTitle,
			&isHost, &r.parentRunID); err != nil {
			log.Printf("v2 migration: skipping run (scan error): %v", err)
			continue
		}
		r.isHost = isHost == 1
		if completedAt.Valid {
			r.completedAt = completedAt.String
		}
		allRuns = append(allRuns, r)
	}

	if len(allRuns) == 0 {
		// No runs to migrate — skip marking as done so migration
		// re-checks on next startup (cheap, handles upgrades where
		// the daemon starts before any runs exist).
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

	// Identify top-level runs (no parent, or parent not in DB)
	var topLevel []runRecord
	for _, r := range allRuns {
		if r.parentRunID == "" || runByID[r.parentRunID] == nil {
			topLevel = append(topLevel, r)
		}
	}

	// Group top-level runs by task_id. Each top-level run becomes an attempt.
	// Runs without a task_id get a generated user-initiated task.
	taskCreated := map[string]bool{}
	userTaskCounter := 0

	// Sort by started_at for stable attempt numbering
	sort.SliceStable(topLevel, func(i, j int) bool {
		return topLevel[i].startedAt < topLevel[j].startedAt
	})

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("starting transaction: %w", err)
	}
	defer tx.Rollback()

	for _, r := range topLevel {
		taskID := r.taskID
		taskTitle := r.taskTitle
		taskSource := "external"

		// Create a user-initiated task for runs without a task ID
		if taskID == "" {
			userTaskCounter++
			taskID = fmt.Sprintf("user-%s", domain.GenerateAttemptID())
			taskSource = "user-initiated"
			if taskTitle == "" {
				taskTitle = r.workflow + " (user-initiated)"
			}
		}

		// Create task record if not yet created
		if !taskCreated[taskID] {
			_, err := tx.Exec(`INSERT OR IGNORE INTO tasks (id, title, source, project_dir, created_at)
				VALUES (?, ?, ?, ?, ?)`,
				taskID, taskTitle, taskSource, r.projectDir, r.startedAt)
			if err != nil {
				log.Printf("v2 migration: failed to create task %s: %v", taskID, err)
				continue
			}
			taskCreated[taskID] = true
		}

		// Create attempt from this top-level run
		attemptID := domain.GenerateAttemptID()
		attemptResult := mapRunStateToAttemptResult(r.state)

		_, err := tx.Exec(`INSERT INTO attempts (id, task_id, started_at, ended_at, result, project_dir)
			VALUES (?, ?, ?, ?, ?, ?)`,
			attemptID, taskID, r.startedAt, nullIfEmpty(r.completedAt), attemptResult, r.projectDir)
		if err != nil {
			log.Printf("v2 migration: failed to create attempt for run %s: %v", r.id, err)
			continue
		}

		// Link this run and its children to the attempt
		runIDs := []string{r.id}
		for _, childID := range childrenOf[r.id] {
			runIDs = append(runIDs, childID)
		}

		for _, runID := range runIDs {
			tx.Exec(`UPDATE runs SET attempt_id = ?, task_id = ? WHERE id = ?`, attemptID, taskID, runID)
		}

		// Move log files on disk
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

	// Mark migration as complete
	db.Exec(`INSERT INTO _migrations (id, applied_at) VALUES ('v2-task-oriented', ?)`,
		time.Now().UTC().Format(time.RFC3339))

	log.Printf("v2 migration: migrated %d top-level runs into tasks and attempts", len(topLevel))
	return nil
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
