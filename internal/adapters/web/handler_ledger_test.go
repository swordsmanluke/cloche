package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swordsmanluke/cloche/internal/adapters/sqlite"
	"github.com/swordsmanluke/cloche/internal/domain"
)

func setupLedgerHandler(t *testing.T) (*Handler, *sqlite.Store) {
	t.Helper()
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	h, err := NewHandler(store, store, WithTaskStore(store))
	require.NoError(t, err)
	return h, store
}

func runGitLedger(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return string(out)
}

// blockGitLedger replaces PATH with a directory containing a fake "git"
// that records every invocation (its argv, newline-separated) to a log file
// instead of running anything, so a test can assert that no subprocess was
// spawned during a section of code expected to hit a warm cache. Restores
// the original PATH on test cleanup.
func blockGitLedger(t *testing.T) (calls func() []string) {
	t.Helper()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "calls.log")
	script := fmt.Sprintf("#!/bin/sh\necho \"$@\" >> %s\nexit 1\n", logPath)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0755))

	origPath := os.Getenv("PATH")
	require.NoError(t, os.Setenv("PATH", dir))
	t.Cleanup(func() { os.Setenv("PATH", origPath) })

	return func() []string {
		data, err := os.ReadFile(logPath)
		if err != nil {
			return nil
		}
		var out []string
		for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
			if line != "" {
				out = append(out, line)
			}
		}
		return out
	}
}

func TestAPILedger_PromptRevisionsAndRequirements(t *testing.T) {
	h, store := setupLedgerHandler(t)
	ctx := context.Background()
	dir := t.TempDir()

	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".cloche", "prompts"), 0755))
	promptPath := filepath.Join(dir, ".cloche", "prompts", "implement.md")
	require.NoError(t, os.WriteFile(promptPath, []byte("v1"), 0644))
	runGitLedger(t, dir, "init")
	runGitLedger(t, dir, "add", ".")
	runGitLedger(t, dir, "commit", "-m", "v1")
	rev1 := strings.TrimSpace(runGitLedger(t, dir, "rev-parse", "HEAD"))

	// Guarantee the two commits land in different seconds, since git commit
	// timestamps (and --until in the backfill path) only have second
	// resolution.
	time.Sleep(1100 * time.Millisecond)
	require.NoError(t, os.WriteFile(promptPath, []byte("v2"), 0644))
	runGitLedger(t, dir, "add", ".")
	runGitLedger(t, dir, "commit", "-m", "v2")
	rev2 := strings.TrimSpace(runGitLedger(t, dir, "rev-parse", "HEAD"))

	// A run row is required for resolveProjectDir to discover the project.
	seedRunWithProject(t, store, "seed-run", "develop", domain.RunStateSucceeded, dir)

	require.NoError(t, store.SaveTask(ctx, &domain.Task{ID: "task1", Title: "Add ledger", ProjectDir: dir}))

	now := time.Now()
	att1 := &domain.Attempt{ID: "att1", TaskID: "task1", StartedAt: now.Add(-2 * time.Hour), EndedAt: now.Add(-2*time.Hour + time.Minute), Result: domain.AttemptResultSucceeded}
	require.NoError(t, store.SaveAttempt(ctx, att1))
	att2 := &domain.Attempt{ID: "att2", TaskID: "task1", StartedAt: now.Add(-1 * time.Hour), EndedAt: now.Add(-1*time.Hour + time.Minute), Result: domain.AttemptResultFailed}
	require.NoError(t, store.SaveAttempt(ctx, att2))

	require.NoError(t, store.SetContextKey(ctx, "task1", "att1", "run-a1", "develop:implement:prompt_file", ".cloche/prompts/implement.md"))
	require.NoError(t, store.SetContextKey(ctx, "task1", "att1", "run-a1", "develop:implement:prompt_rev", rev1))
	require.NoError(t, store.SetContextKey(ctx, "task1", "att1", "run-a1", "develop:implement:intent", "req-1"))

	require.NoError(t, store.SetContextKey(ctx, "task1", "att2", "run-a2", "develop:implement:prompt_file", ".cloche/prompts/implement.md"))
	require.NoError(t, store.SetContextKey(ctx, "task1", "att2", "run-a2", "develop:implement:prompt_rev", rev2))

	run1 := domain.NewRun("run-a1", "develop")
	run1.ProjectDir = dir
	run1.AttemptID = "att1"
	run1.TaskID = "task1"
	require.NoError(t, store.CreateRun(ctx, run1))
	require.NoError(t, store.SaveCapture(ctx, "run-a1", &domain.StepExecution{
		StepName: "implement",
		Usage:    &domain.TokenUsage{InputTokens: 100, OutputTokens: 50},
	}))

	slug := filepath.Base(dir)
	req := httptest.NewRequest("GET", "/api/projects/"+slug+"/ledger", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	require.Equal(t, 200, w.Code)

	var resp apiLedgerResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	require.Len(t, resp.PromptFiles, 1)
	pf := resp.PromptFiles[0]
	assert.Equal(t, ".cloche/prompts/implement.md", pf.Path)
	require.Len(t, pf.Revisions, 2)

	// Newest revision (rev2, the failed attempt) comes first.
	assert.Equal(t, shortSHA(rev2), pf.Revisions[0].Revision)
	assert.Equal(t, 1, pf.Revisions[0].Attempts)
	assert.Equal(t, 0, pf.Revisions[0].Passed)

	assert.Equal(t, shortSHA(rev1), pf.Revisions[1].Revision)
	assert.Equal(t, 1, pf.Revisions[1].Attempts)
	assert.Equal(t, 1, pf.Revisions[1].Passed)
	assert.Equal(t, float64(150), pf.Revisions[1].MeanTokens)

	require.NotNil(t, pf.LatestChange)
	assert.Equal(t, shortSHA(rev1), pf.LatestChange.Before.Revision)
	assert.Equal(t, shortSHA(rev2), pf.LatestChange.After.Revision)

	require.Len(t, resp.Requirements, 1)
	assert.Equal(t, "req-1", resp.Requirements[0].ID)
	require.Len(t, resp.Requirements[0].Tasks, 1)
	assert.Equal(t, "task1", resp.Requirements[0].Tasks[0].TaskID)
	assert.Equal(t, "Add ledger", resp.Requirements[0].Tasks[0].Title)

	require.Len(t, resp.TaskRequirements, 1)
	assert.Equal(t, "task1", resp.TaskRequirements[0].TaskID)
	assert.Equal(t, []string{"req-1"}, resp.TaskRequirements[0].Requirements)

	assert.Equal(t, float64(1), resp.MeanAttemptsToSuccess)
	assert.Equal(t, float64(150), resp.TokensPerSucceededTask)
	assert.NotEmpty(t, resp.PassRateOverTime)
}

// TestAPILedger_BackfillsLegacyAttempt verifies that an attempt predating
// live prompt-revision recording (no prompt_file/prompt_rev KV at all) is
// still attributed to a prompt revision once the background backfill job
// (store.RunLedgerPromptRevisionBackfill, run here the same way the daemon
// runs it at startup — never from the request path) has processed it, and
// that the handler reports BackfillPending until that happens.
func TestAPILedger_BackfillsLegacyAttempt(t *testing.T) {
	h, store := setupLedgerHandler(t)
	ctx := context.Background()
	dir := t.TempDir()

	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".cloche", "prompts"), 0755))
	promptPath := filepath.Join(dir, ".cloche", "prompts", "implement.md")
	require.NoError(t, os.WriteFile(promptPath, []byte("v1"), 0644))
	workflow := `workflow develop {
  step implement {
    prompt = file(".cloche/prompts/implement.md")
    results = [success, fail]
  }

  implement:success -> done
  implement:fail -> abort
}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".cloche", "develop.cloche"), []byte(workflow), 0644))
	runGitLedger(t, dir, "init")
	runGitLedger(t, dir, "add", ".")
	runGitLedger(t, dir, "commit", "-m", "v1")
	rev1 := strings.TrimSpace(runGitLedger(t, dir, "rev-parse", "HEAD"))

	time.Sleep(1100 * time.Millisecond)
	attemptStart := time.Now()
	time.Sleep(1100 * time.Millisecond)

	require.NoError(t, os.WriteFile(promptPath, []byte("v2"), 0644))
	runGitLedger(t, dir, "add", ".")
	runGitLedger(t, dir, "commit", "-m", "v2")

	seedRunWithProject(t, store, "seed-run", "develop", domain.RunStateSucceeded, dir)
	require.NoError(t, store.SaveTask(ctx, &domain.Task{ID: "task1", Title: "Legacy task", ProjectDir: dir}))
	att := &domain.Attempt{ID: "att1", TaskID: "task1", StartedAt: attemptStart, EndedAt: attemptStart.Add(time.Minute), Result: domain.AttemptResultSucceeded}
	require.NoError(t, store.SaveAttempt(ctx, att))

	run1 := domain.NewRun("run-a1", "develop")
	run1.ProjectDir = dir
	run1.AttemptID = "att1"
	run1.TaskID = "task1"
	require.NoError(t, store.CreateRun(ctx, run1))
	require.NoError(t, store.SaveCapture(ctx, "run-a1", &domain.StepExecution{
		StepName: "implement",
		Result:   "success",
	}))

	slug := filepath.Base(dir)
	req := func() *http.Request { return httptest.NewRequest("GET", "/api/projects/"+slug+"/ledger", nil) }

	// Before the background sweep runs, the handler must not compute the
	// backfill itself: it reports what's recorded so far (nothing) plus a
	// pending flag rather than blocking on it.
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req())
	require.Equal(t, 200, w.Code)
	var beforeResp apiLedgerResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &beforeResp))
	assert.True(t, beforeResp.BackfillPending)
	assert.Empty(t, beforeResp.PromptFiles)

	// Simulate the daemon-start background job (see cmd/cloched/main.go)
	// having already swept this project.
	store.RunLedgerPromptRevisionBackfill(ctx, 2)

	w = httptest.NewRecorder()
	h.ServeHTTP(w, req())
	require.Equal(t, 200, w.Code)

	var resp apiLedgerResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	assert.False(t, resp.BackfillPending)
	require.Len(t, resp.PromptFiles, 1)
	pf := resp.PromptFiles[0]
	require.Len(t, pf.Revisions, 1)
	assert.Equal(t, shortSHA(rev1), pf.Revisions[0].Revision)
	assert.Equal(t, 1, pf.Revisions[0].Attempts)
	assert.Equal(t, 1, pf.Revisions[0].Passed)
}

// TestAPILedger_NoExecCommandWhenCachedAndRecorded verifies the ledger
// handler's whole raison d'être here: once prompt revisions are recorded
// (context_kv, written by the dispatch-time path or a completed backfill
// sweep — never by this handler) and a file's git history is cached at the
// current HEAD, serving the ledger costs zero git subprocesses, however
// many attempts the project has.
func TestAPILedger_NoExecCommandWhenCachedAndRecorded(t *testing.T) {
	h, store := setupLedgerHandler(t)
	ctx := context.Background()
	dir := t.TempDir()

	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".cloche", "prompts"), 0755))
	promptPath := filepath.Join(dir, ".cloche", "prompts", "implement.md")
	require.NoError(t, os.WriteFile(promptPath, []byte("v1"), 0644))
	runGitLedger(t, dir, "init")
	runGitLedger(t, dir, "add", ".")
	runGitLedger(t, dir, "commit", "-m", "v1")
	rev1 := strings.TrimSpace(runGitLedger(t, dir, "rev-parse", "HEAD"))

	seedRunWithProject(t, store, "seed-run", "develop", domain.RunStateSucceeded, dir)
	require.NoError(t, store.SaveTask(ctx, &domain.Task{ID: "task1", Title: "Add ledger", ProjectDir: dir}))
	att := &domain.Attempt{ID: "att1", TaskID: "task1", StartedAt: time.Now(), Result: domain.AttemptResultSucceeded}
	require.NoError(t, store.SaveAttempt(ctx, att))

	// Recorded exactly as host.Executor.recordPromptRevisionKV would at
	// dispatch time — no backfill involved.
	require.NoError(t, store.SetContextKey(ctx, "task1", "att1", "run-a1", "develop:implement:prompt_file", ".cloche/prompts/implement.md"))
	require.NoError(t, store.SetContextKey(ctx, "task1", "att1", "run-a1", "develop:implement:prompt_rev", rev1))

	slug := filepath.Base(dir)
	req := func() *http.Request { return httptest.NewRequest("GET", "/api/projects/"+slug+"/ledger", nil) }

	// First request (git available) warms the per-HEAD history cache.
	w1 := httptest.NewRecorder()
	h.ServeHTTP(w1, req())
	require.Equal(t, 200, w1.Code)

	// Second request: revisions come from context_kv and the file's history
	// is cached at the unchanged HEAD, so this must not shell out at all.
	calls := blockGitLedger(t)
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, req())
	require.Equal(t, 200, w2.Code)
	assert.Empty(t, calls(), "ledger handler invoked git while history was cached and revisions were recorded")

	var resp apiLedgerResponse
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &resp))
	require.Len(t, resp.PromptFiles, 1)
	assert.Equal(t, shortSHA(rev1), resp.PromptFiles[0].Revisions[0].Revision)
}
