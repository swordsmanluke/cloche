package sqlite

import (
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/swordsmanluke/cloche/internal/config"
	"github.com/swordsmanluke/cloche/internal/domain"
	"github.com/swordsmanluke/cloche/internal/host"
)

// backfillRunRepositories populates runs.repositories for rows created
// before the column existed (or before a run's workflow declared more than
// one repo), using domain.ResolveRunRepositories with whatever signals are
// still recoverable from disk: the run's own repository column (rule a),
// and — where a run's project_dir turns out to be a [[repositories]] path of
// some other known project, the "cloche run from inside a sub-repo" bug
// (see docs bug report) — the matched repository (rule b), plus the
// project's *current* workflow definitions (rule d; historical workflow
// definitions aren't recoverable, so a workflow that has since dropped or
// gained repos won't backfill accurately for old runs).
//
// Runs whose project_dir was a sub-repo path also get project_dir rewritten
// to the parent project, folding them into the parent's task stack instead
// of leaving them invisible outside "all repos" (see handler_task_stack.go).
// Rule (c) (extraction/step-pin touched repos) isn't recoverable at all for
// historical runs — only newly dispatched/executed runs record it live.
//
// Gated by _migrations so it only runs once, like backfillRunOrigin.
func backfillRunRepositories(db *sql.DB) error {
	const migrationID = "run-repositories-backfill-v1"
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM _migrations WHERE id = ?`, migrationID).Scan(&count); err == nil && count > 0 {
		return nil
	}

	projectDirs, err := listProjectDirs(db)
	if err != nil {
		return fmt.Errorf("listing project dirs: %w", err)
	}

	// subRepoParent maps a directory that is itself a [[repositories]] path
	// of some other known project to that project's dir and repo name.
	type parentRepo struct{ dir, repo string }
	subRepoParent := map[string]parentRepo{}
	for _, dir := range projectDirs {
		cfg, err := config.Load(dir)
		if err != nil || len(cfg.Repositories) == 0 {
			continue
		}
		for _, r := range cfg.ResolveRepositories(dir) {
			if r.Name == "" || r.Path == dir {
				continue
			}
			subRepoParent[r.Path] = parentRepo{dir: dir, repo: r.Name}
		}
	}

	rows, err := db.Query(`SELECT id, attempt_id, project_dir, workflow_name, COALESCE(repository,'') FROM runs`)
	if err != nil {
		return fmt.Errorf("listing runs: %w", err)
	}
	type runRow struct{ id, attemptID, projectDir, workflowName, repository string }
	var runs []runRow
	for rows.Next() {
		var r runRow
		if err := rows.Scan(&r.id, &r.attemptID, &r.projectDir, &r.workflowName, &r.repository); err != nil {
			rows.Close()
			return fmt.Errorf("scanning run: %w", err)
		}
		runs = append(runs, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	wfReposCache := map[string]map[string][]string{} // projectDir -> workflowName -> repos
	wfReposFor := func(projectDir, workflowName string) []string {
		byName, ok := wfReposCache[projectDir]
		if !ok {
			byName = map[string][]string{}
			if all, err := host.FindAllWorkflows(projectDir); err == nil {
				for name, wf := range all {
					byName[name] = wf.Repos
				}
			}
			wfReposCache[projectDir] = byName
		}
		return byName[workflowName]
	}

	for _, r := range runs {
		effectiveProjectDir := r.projectDir
		projectRepo := ""
		if pr, ok := subRepoParent[r.projectDir]; ok {
			effectiveProjectDir = pr.dir
			projectRepo = pr.repo
		}

		repos := domain.ResolveRunRepositories(r.repository, projectRepo, nil, wfReposFor(effectiveProjectDir, r.workflowName))
		reposStr := strings.Join(repos, ",")

		if effectiveProjectDir != r.projectDir {
			if _, err := db.Exec(`UPDATE runs SET project_dir = ?, repositories = ? WHERE id = ? AND attempt_id = ?`,
				effectiveProjectDir, reposStr, r.id, r.attemptID); err != nil {
				log.Printf("run-repositories backfill: folding run %s into parent project %s: %v", r.id, effectiveProjectDir, err)
			}
			continue
		}
		if reposStr == "" {
			continue
		}
		if _, err := db.Exec(`UPDATE runs SET repositories = ? WHERE id = ? AND attempt_id = ?`, reposStr, r.id, r.attemptID); err != nil {
			log.Printf("run-repositories backfill: updating run %s: %v", r.id, err)
		}
	}

	_, err = db.Exec(`INSERT OR IGNORE INTO _migrations (id, applied_at) VALUES (?, ?)`,
		migrationID, time.Now().UTC().Format(time.RFC3339))
	return err
}

// listProjectDirs mirrors Store.ListProjects, usable during migrate() before
// a *Store exists.
func listProjectDirs(db *sql.DB) ([]string, error) {
	rows, err := db.Query(`SELECT DISTINCT project_dir FROM runs WHERE project_dir != '' AND project_dir NOT LIKE '%/.gitworktrees/%' ORDER BY project_dir`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var dirs []string
	for rows.Next() {
		var dir string
		if err := rows.Scan(&dir); err != nil {
			return nil, err
		}
		dirs = append(dirs, dir)
	}
	return dirs, rows.Err()
}
