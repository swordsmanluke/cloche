package sqlite

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swordsmanluke/cloche/internal/domain"
)

func runGitBackfill(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return string(out)
}

// seedLedgerRepo creates a project directory with a git-tracked prompt file
// and a workflow referencing it, returning the commit's revision. Call once
// per project dir; seedLegacyLedgerAttempt seeds the (possibly several)
// attempts against it.
func seedLedgerRepo(t *testing.T, dir string) (rev string) {
	t.Helper()

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
	runGitBackfill(t, dir, "init")
	runGitBackfill(t, dir, "add", ".")
	runGitBackfill(t, dir, "commit", "-m", "v1")
	rev = strings.TrimSpace(runGitBackfill(t, dir, "rev-parse", "HEAD"))

	// GitRevisionAt filters commits with --until=<attempt start>, which is
	// inclusive only to the second, so any attempt seeded against this repo
	// must start strictly after the commit above for its revision to
	// resolve.
	time.Sleep(1100 * time.Millisecond)
	return rev
}

// seedLegacyLedgerAttempt creates one terminal attempt/run/capture in dir
// (already set up via seedLedgerRepo) with no prompt_file/prompt_rev
// context_kv recorded — i.e. an attempt that predates dispatch-time
// recording and needs the backfill job to attribute it.
func seedLegacyLedgerAttempt(t *testing.T, store *Store, dir, taskID, attemptID string) {
	t.Helper()
	ctx := context.Background()

	run := domain.NewRun("run-"+attemptID, "develop")
	run.ProjectDir = dir
	run.AttemptID = attemptID
	run.TaskID = taskID
	require.NoError(t, store.CreateRun(ctx, run))
	require.NoError(t, store.SaveCapture(ctx, run.ID, &domain.StepExecution{
		StepName: "implement",
		Result:   "success",
	}))

	require.NoError(t, store.SaveTask(ctx, &domain.Task{ID: taskID, Title: "Legacy task", ProjectDir: dir}))
	att := &domain.Attempt{ID: attemptID, TaskID: taskID, StartedAt: time.Now(), Result: domain.AttemptResultSucceeded}
	require.NoError(t, store.SaveAttempt(ctx, att))
}

func TestRunLedgerPromptRevisionBackfill_PersistsAndMarksDone(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	dir := t.TempDir()
	rev := seedLedgerRepo(t, dir)
	seedLegacyLedgerAttempt(t, store, dir, "task1", "att1")

	pending, err := store.LedgerBackfillPending(ctx, dir)
	require.NoError(t, err)
	assert.True(t, pending, "backfill should be pending before the sweep runs")

	store.RunLedgerPromptRevisionBackfill(ctx, 2)

	rows, err := store.ListContextKVForProject(ctx, dir)
	require.NoError(t, err)
	var gotFile, gotRev string
	for _, r := range rows {
		switch r.Key {
		case "develop:implement:prompt_file":
			gotFile = r.Value
		case "develop:implement:prompt_rev":
			gotRev = r.Value
		}
	}
	assert.Equal(t, ".cloche/prompts/implement.md", gotFile)
	assert.Equal(t, rev, gotRev)

	pending, err = store.LedgerBackfillPending(ctx, dir)
	require.NoError(t, err)
	assert.False(t, pending, "backfill should be marked done once the sweep finishes")
}

func TestRunLedgerPromptRevisionBackfill_SkipsAttemptsAlreadyRecorded(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	dir := t.TempDir()
	seedLedgerRepo(t, dir)
	seedLegacyLedgerAttempt(t, store, dir, "task1", "att1")

	// Simulate a live-recorded attempt (dispatch-time recording already ran
	// for it) with a value the backfill must never overwrite.
	require.NoError(t, store.SetContextKey(ctx, "task1", "att1", "run-att1", "develop:implement:prompt_rev", "sentinel-value"))

	store.RunLedgerPromptRevisionBackfill(ctx, 2)

	val, ok, err := store.GetContextKey(ctx, "task1", "att1", "run-att1", "develop:implement:prompt_rev")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "sentinel-value", val, "backfill overwrote an already-recorded prompt_rev")
}

func TestRunLedgerPromptRevisionBackfill_ResumesAcrossCalls(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	dir := t.TempDir()
	rev := seedLedgerRepo(t, dir)
	seedLegacyLedgerAttempt(t, store, dir, "task1", "att1")
	seedLegacyLedgerAttempt(t, store, dir, "task1", "att2")

	// A cancelled context stops the sweep before either attempt is
	// processed and before the project is marked done.
	cancelledCtx, cancel := context.WithCancel(ctx)
	cancel()
	store.RunLedgerPromptRevisionBackfill(cancelledCtx, 1)

	pending, err := store.LedgerBackfillPending(ctx, dir)
	require.NoError(t, err)
	assert.True(t, pending, "a cancelled sweep must not mark the project done")

	// A later, uncancelled call resumes and finishes the work.
	store.RunLedgerPromptRevisionBackfill(ctx, 2)

	pending, err = store.LedgerBackfillPending(ctx, dir)
	require.NoError(t, err)
	assert.False(t, pending)

	rows, err := store.ListContextKVForProject(ctx, dir)
	require.NoError(t, err)
	found := map[string]string{}
	for _, r := range rows {
		if r.AttemptID == "att1" && r.Key == "develop:implement:prompt_rev" {
			found["att1"] = r.Value
		}
		if r.AttemptID == "att2" && r.Key == "develop:implement:prompt_rev" {
			found["att2"] = r.Value
		}
	}
	assert.Equal(t, rev, found["att1"])
	assert.Equal(t, rev, found["att2"])
}
