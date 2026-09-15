package sqlite

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/swordsmanluke/cloche/internal/domain"
	"github.com/swordsmanluke/cloche/internal/dsl"
)

// agentNameBackfilled tracks which projects have already had their
// step_executions.agent_name column backfilled in this process lifetime,
// mirroring the migratedProjects cache in migrate_v2.go.
var (
	agentNameBackfilled   = map[string]bool{}
	agentNameBackfilledMu sync.Mutex
)

// backfillAgentNamesOnce fills in step_executions.agent_name for rows that
// recorded token usage before the agent name made it through the pipeline
// (see cloche-bb79). Runs at most once per project per process, guarded by
// the _migrations table so it also runs at most once ever per project.
func (s *Store) backfillAgentNamesOnce(projectDir string) error {
	agentNameBackfilledMu.Lock()
	if agentNameBackfilled[projectDir] {
		agentNameBackfilledMu.Unlock()
		return nil
	}
	agentNameBackfilledMu.Unlock()

	migrationID := "agent-name-backfill:" + projectDir
	var count int
	row := s.db.QueryRow(`SELECT COUNT(*) FROM _migrations WHERE id = ?`, migrationID)
	if err := row.Scan(&count); err == nil && count > 0 {
		agentNameBackfilledMu.Lock()
		agentNameBackfilled[projectDir] = true
		agentNameBackfilledMu.Unlock()
		return nil
	}

	if err := backfillAgentNames(s.db, projectDir); err != nil {
		return err
	}

	s.db.Exec(`INSERT OR IGNORE INTO _migrations (id, applied_at) VALUES (?, ?)`,
		migrationID, time.Now().UTC().Format(time.RFC3339))

	agentNameBackfilledMu.Lock()
	agentNameBackfilled[projectDir] = true
	agentNameBackfilledMu.Unlock()

	return nil
}

// backfillAgentNames sets agent_name on step_executions rows for the given
// project that recorded token usage but have no agent attributed. Where the
// step's workflow file is still on disk, the agent command is inferred from
// its config (agent_command, or an agent = <alias> reference); otherwise the
// row is labeled domain.UnattributedAgent rather than left blank.
func backfillAgentNames(db *sql.DB, projectDir string) error {
	rows, err := db.Query(`
		SELECT se.id, r.workflow_name, se.step_name
		FROM step_executions se
		JOIN runs r ON se.run_id = r.id
		WHERE r.project_dir = ?
		  AND (se.input_tokens > 0 OR se.output_tokens > 0)
		  AND (se.agent_name IS NULL OR se.agent_name = '')`, projectDir)
	if err != nil {
		return fmt.Errorf("querying unattributed usage rows: %w", err)
	}

	type unattributedRow struct {
		id       int64
		workflow string
		step     string
	}
	var toFix []unattributedRow
	for rows.Next() {
		var r unattributedRow
		if err := rows.Scan(&r.id, &r.workflow, &r.step); err != nil {
			rows.Close()
			return fmt.Errorf("scanning unattributed usage row: %w", err)
		}
		toFix = append(toFix, r)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	if len(toFix) == 0 {
		return nil
	}

	// Cache the resolved agent command per workflow so each .cloche file is
	// only read and parsed once, however many rows reference it.
	agentByWorkflowStep := map[string]map[string]string{}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("starting transaction: %w", err)
	}
	defer tx.Rollback()

	for _, r := range toFix {
		steps, ok := agentByWorkflowStep[r.workflow]
		if !ok {
			wfPath := filepath.Join(projectDir, ".cloche", r.workflow+".cloche")
			if data, readErr := os.ReadFile(wfPath); readErr == nil {
				steps = dsl.AgentCommandsForWorkflow(data)
			} else {
				steps = map[string]string{}
			}
			agentByWorkflowStep[r.workflow] = steps
		}

		agentName := steps[r.step]
		if agentName == "" {
			// Either the workflow file is gone, the step no longer exists,
			// or it never configured an agent command — the true value was
			// never captured, so label it rather than leave it blank.
			agentName = domain.UnattributedAgent
		}
		if _, err := tx.Exec(`UPDATE step_executions SET agent_name = ? WHERE id = ?`, agentName, r.id); err != nil {
			return fmt.Errorf("backfilling agent_name for step_execution %d: %w", r.id, err)
		}
	}

	return tx.Commit()
}
