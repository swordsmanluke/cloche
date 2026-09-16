package sqlite

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/swordsmanluke/cloche/internal/domain"
	"github.com/swordsmanluke/cloche/internal/promptrev"
)

// DefaultLedgerBackfillParallelism bounds how many git subprocesses the
// backfill runs concurrently, absent an explicit value.
const DefaultLedgerBackfillParallelism = 4

const ledgerBackfillMigrationPrefix = "ledger-prompt-rev-backfill:"

// LedgerBackfillPending reports whether the one-time historical
// prompt-revision backfill (RunLedgerPromptRevisionBackfill) has not yet
// finished sweeping projectDir, so the ledger handler can surface a
// backfill_pending flag instead of blocking a request on it.
func (s *Store) LedgerBackfillPending(ctx context.Context, projectDir string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM _migrations WHERE id = ?`,
		ledgerBackfillMigrationPrefix+projectDir).Scan(&count)
	if err != nil {
		return false, err
	}
	return count == 0, nil
}

// RunLedgerPromptRevisionBackfill attributes every pre-existing terminal
// attempt that predates dispatch-time prompt-revision recording (see
// host.Executor.recordPromptRevisionKV and its DaemonExecutor counterpart)
// to the prompt file/git-revision it ran with, writing the same
// "<workflow>:<step>:prompt_file" / "prompt_rev" context_kv rows the live
// path writes so the ledger handler's aggregation needs no special-casing.
//
// Intended to run once as a background goroutine at daemon start — never on
// a request path (see internal/adapters/web/handler_ledger.go), with up to
// parallel concurrent git subprocesses. Idempotent and resumable: an
// attempt that already has a recorded "*:prompt_rev" key (whether from the
// live path or an earlier, interrupted sweep) is never reprocessed, and a
// project is marked done in _migrations only once every attempt it had at
// sweep time was visited, so a daemon restart mid-sweep simply picks up
// where it left off.
func (s *Store) RunLedgerPromptRevisionBackfill(ctx context.Context, parallel int) {
	if parallel <= 0 {
		parallel = DefaultLedgerBackfillParallelism
	}
	projects, err := s.ListProjects(ctx)
	if err != nil {
		log.Printf("ledger backfill: listing projects: %v", err)
		return
	}

	sem := make(chan struct{}, parallel)
	for _, dir := range projects {
		if ctx.Err() != nil {
			return
		}
		pending, err := s.LedgerBackfillPending(ctx, dir)
		if err != nil {
			log.Printf("ledger backfill: checking status for %s: %v", dir, err)
			continue
		}
		if !pending {
			continue
		}
		if !s.backfillProjectPromptRevisions(ctx, dir, sem) {
			return // ctx cancelled mid-sweep; leave unmarked so the next daemon start resumes here
		}
		if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO _migrations (id, applied_at) VALUES (?, ?)`,
			ledgerBackfillMigrationPrefix+dir, time.Now().UTC().Format(time.RFC3339)); err != nil {
			log.Printf("ledger backfill: marking %s done: %v", dir, err)
		}
	}
}

// pendingAttempt is one row of pendingLedgerBackfillAttempts's result: just
// enough of an attempt to resolve and record its prompt revision.
type pendingAttempt struct {
	id, taskID string
	startedAt  time.Time
}

// backfillProjectPromptRevisions processes every terminal attempt of dir
// that has no recorded prompt_rev KV yet, using up to cap(sem) concurrent
// workers shared across the whole backfill sweep. Returns false if ctx was
// cancelled before every attempt could be processed.
func (s *Store) backfillProjectPromptRevisions(ctx context.Context, dir string, sem chan struct{}) bool {
	attempts, err := s.pendingLedgerBackfillAttempts(ctx, dir)
	if err != nil {
		log.Printf("ledger backfill: listing pending attempts for %s: %v", dir, err)
		return true
	}

	var wg sync.WaitGroup
	for _, a := range attempts {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			wg.Wait()
			return false
		}
		wg.Add(1)
		go func(a pendingAttempt) {
			defer wg.Done()
			defer func() { <-sem }()
			s.backfillAttemptPromptRevision(ctx, dir, a)
		}(a)
	}
	wg.Wait()
	return true
}

// pendingLedgerBackfillAttempts returns every terminal attempt of
// projectDir with no "*:prompt_rev" context_kv row at all — i.e. attempts
// that predate live dispatch-time recording and haven't been backfilled yet.
func (s *Store) pendingLedgerBackfillAttempts(ctx context.Context, projectDir string) ([]pendingAttempt, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT a.id, a.task_id, a.started_at
		FROM attempts a
		WHERE a.project_dir = ?
		  AND a.result IN ('succeeded', 'failed', 'cancelled')
		  AND NOT EXISTS (
		    SELECT 1 FROM context_kv ck
		    WHERE ck.task_id = a.task_id AND ck.attempt_id = a.id AND ck.key LIKE '%:prompt_rev'
		  )`, projectDir)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []pendingAttempt
	for rows.Next() {
		var a pendingAttempt
		var startedAt string
		if err := rows.Scan(&a.id, &a.taskID, &startedAt); err != nil {
			return nil, err
		}
		a.startedAt = parseTime(startedAt)
		out = append(out, a)
	}
	return out, rows.Err()
}

// backfillAttemptPromptRevision attributes a's executed steps to the prompt
// file(s) their workflow references today, at the git revision effective as
// of a's start time, persisting the result exactly like the live dispatch-
// time path (host.Executor.recordPromptRevisionKV) would have. Best-effort:
// any lookup failure just leaves that attempt/file unrecorded, to be retried
// on the next backfill sweep.
func (s *Store) backfillAttemptPromptRevision(ctx context.Context, dir string, a pendingAttempt) {
	runs, err := s.ListRunsFiltered(ctx, domain.RunListFilter{ProjectDir: dir, AttemptID: a.id})
	if err != nil {
		return
	}

	seenFiles := map[string]bool{}
	for _, run := range runs {
		files := promptrev.ResolveWorkflowPromptFiles(dir, run.WorkflowName)
		if len(files) == 0 {
			continue
		}
		execs, err := s.GetCaptures(ctx, run.ID)
		if err != nil {
			continue
		}
		for _, se := range execs {
			relPath, ok := files[se.StepName]
			if !ok || seenFiles[relPath] {
				continue
			}
			rev := promptrev.GitRevisionAt(dir, relPath, a.startedAt)
			if rev == "" {
				continue
			}
			seenFiles[relPath] = true

			prefix := fmt.Sprintf("%s:%s", run.WorkflowName, se.StepName)
			if err := s.SetContextKey(ctx, a.taskID, a.id, run.ID, prefix+":prompt_file", relPath); err != nil {
				log.Printf("ledger backfill: recording prompt file for attempt %s step %q: %v", a.id, se.StepName, err)
				continue
			}
			if err := s.SetContextKey(ctx, a.taskID, a.id, run.ID, prefix+":prompt_rev", rev); err != nil {
				log.Printf("ledger backfill: recording prompt revision for attempt %s step %q: %v", a.id, se.StepName, err)
			}
		}
	}
}
