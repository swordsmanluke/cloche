package scan_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swordsmanluke/cloche/internal/builtin"
	"github.com/swordsmanluke/cloche/internal/domain"
	"github.com/swordsmanluke/cloche/internal/engine"
	"github.com/swordsmanluke/cloche/internal/intent/scan"
)

func TestBuiltinWorkflow_Validates(t *testing.T) {
	wf := scan.BuiltinWorkflow()

	require.NoError(t, wf.Validate())
	assert.Empty(t, wf.ValidateConfig())
}

func TestBuiltinWorkflow_Shape(t *testing.T) {
	wf := scan.BuiltinWorkflow()

	assert.Equal(t, "intent-scan", wf.Name)
	assert.True(t, wf.Builtin)
	assert.Equal(t, "discover-domains", wf.EntryStep)
	assert.Len(t, wf.Steps, 8)
	assert.Equal(t, domain.StepTypeWorkflow, wf.Steps["scan-pass"].Type)
	assert.Equal(t, scan.RepoWorkflowName, wf.Steps["scan-pass"].Config["workflow_name"])

	wantWires := map[string]string{
		"discover-domains:success": "check-domains",
		"discover-domains:none":    "collect-sources",
		"discover-domains:fail":    "abort-cleanup",
		"check-domains:success":    "collect-sources",
		"check-domains:fail":       "repair-domains",
		"repair-domains:success":   "check-domains",
		"repair-domains:fail":      "abort-cleanup",
		"repair-domains:give-up":   "abort-cleanup",
		"collect-sources:success":  "next-pass",
		"collect-sources:none":     "commit",
		"collect-sources:fail":     "abort-cleanup",
		"next-pass:pass":           "scan-pass",
		"next-pass:done":           "commit",
		"next-pass:fail":           "abort-cleanup",
		"scan-pass:success":        "next-pass",
		"scan-pass:fail":           "abort-cleanup",
		"scan-pass:timeout":        "abort-cleanup",
		"commit:success":           "done",
		"commit:fail":              "abort",
		"abort-cleanup:success":    "abort",
		"abort-cleanup:fail":       "abort",
	}
	assertWires(t, wf, wantWires)
}

// The per-pass sub-workflow holds the agent steps; each pass is a fresh
// engine run of it, so its max_attempts budgets are per pass.
func TestBuiltinRepoWorkflow_Shape(t *testing.T) {
	wf := scan.BuiltinRepoWorkflow()
	require.NoError(t, wf.Validate())
	assert.Empty(t, wf.ValidateConfig())

	assert.Equal(t, scan.RepoWorkflowName, wf.Name)
	assert.True(t, wf.Builtin)
	assert.Equal(t, domain.LocationHost, wf.Location)
	assert.Equal(t, "extract", wf.EntryStep)
	assert.Len(t, wf.Steps, 7)

	wantWires := map[string]string{
		"extract:success":           "check-candidates",
		"extract:fail":              "abort",
		"extract:give-up":           "abort",
		"check-candidates:success":  "reconcile",
		"check-candidates:missing":  "extract",
		"check-candidates:fail":     "repair-candidates",
		"repair-candidates:success": "check-candidates",
		"repair-candidates:fail":    "abort",
		"repair-candidates:give-up": "abort",
		"reconcile:success":         "check-reconcile",
		"reconcile:none":            "done",
		"reconcile:fail":            "abort",
		"reconcile:give-up":         "abort",
		"check-reconcile:success":   "apply-reconcile",
		"check-reconcile:missing":   "reconcile",
		"check-reconcile:fail":      "repair-reconcile",
		"repair-reconcile:success":  "check-reconcile",
		"repair-reconcile:fail":     "abort",
		"repair-reconcile:give-up":  "abort",
		"apply-reconcile:success":   "done",
		"apply-reconcile:fail":      "abort",
	}
	assertWires(t, wf, wantWires)
}

func assertWires(t *testing.T, wf *domain.Workflow, wantWires map[string]string) {
	t.Helper()
	assert.Len(t, wf.Wiring, len(wantWires))
	for _, wire := range wf.Wiring {
		key := wire.From + ":" + wire.Result
		want, ok := wantWires[key]
		if assert.True(t, ok, "unexpected wire %s", key) {
			assert.Equal(t, want, wire.To, "wire %s", key)
		}
	}
}

// TestBuiltinWorkflow_ScriptsAreDashCompatible guards against a regression
// where the collect-sources / apply-reconcile scripts used the bashism
// `set -o pipefail`, which fails instantly under Ubuntu's default `/bin/sh`
// (dash), which lacks it — the host executor always runs step "run" scripts
// via `sh -c`, not bash. See cloche-la93 x cloche-ulid integration bug.
func TestBuiltinWorkflow_ScriptsAreDashCompatible(t *testing.T) {
	steps := map[string]*domain.Step{}
	for _, wf := range []*domain.Workflow{scan.BuiltinWorkflow(), scan.BuiltinRepoWorkflow()} {
		for name, step := range wf.Steps {
			steps[name] = step
		}
	}

	for _, name := range []string{"check-domains", "collect-sources", "next-pass", "check-candidates", "check-reconcile", "apply-reconcile", "commit", "abort-cleanup"} {
		step, ok := steps[name]
		require.True(t, ok, "step %s should exist", name)
		script := step.Config["run"]
		require.NotEmpty(t, script, "step %s should have a run script", name)

		firstLine, _, _ := strings.Cut(script, "\n")
		cmd := exec.Command("sh", "-c", firstLine)
		out, err := cmd.CombinedOutput()
		assert.NoError(t, err, "step %s: %q failed under sh: %s", name, firstLine, out)
	}
}

func runGitCommit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return string(out)
}

// runCommitScript executes the intent-scan workflow's "commit" step script
// exactly as the host executor would: via `sh -c`, cwd at the project dir,
// CLOCHE_PROJECT_DIR pointing at it, and (optionally) CLOCHE_PREV_OUTPUT
// pointing at a file standing in for apply-reconcile's captured stdout.
func runCommitScript(t *testing.T, dir, prevOutputFile string) (string, error) {
	t.Helper()
	return runIntentDirScript(t, "commit", dir, prevOutputFile)
}

// stubClocheBin returns a directory holding a stub `cloche` whose
// `intent validate` reports success (the fixtures here are valid) and whose
// `get temp_file_dir` prints `temp`; everything else exits 0 silently. The
// scripts under test must not depend on whichever real cloche is installed.
func stubClocheBin(t *testing.T, temp string) string {
	t.Helper()
	bin := t.TempDir()
	stub := "#!/bin/sh\n" +
		"if [ \"$1\" = get ] && [ \"$2\" = temp_file_dir ]; then echo '" + temp + "'; exit 0; fi\n" +
		"if [ \"$1\" = intent ] && [ \"$2\" = validate ]; then echo 'intent store OK (stub)'; exit 0; fi\n" +
		"exit 0\n"
	require.NoError(t, os.WriteFile(filepath.Join(bin, "cloche"), []byte(stub), 0755))
	return bin
}

func runIntentDirScript(t *testing.T, step, dir, prevOutputFile string) (string, error) {
	t.Helper()
	script := scan.BuiltinWorkflow().Steps[step].Config["run"]
	cmd := exec.Command("sh", "-c", script)
	cmd.Dir = dir
	env := append(os.Environ(), "CLOCHE_PROJECT_DIR="+dir,
		"PATH="+stubClocheBin(t, t.TempDir())+":"+os.Getenv("PATH"),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com")
	if prevOutputFile != "" {
		env = append(env, "CLOCHE_PREV_OUTPUT="+prevOutputFile)
	}
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestBuiltinWorkflow_CommitScript_NoOpWhenClean(t *testing.T) {
	dir := t.TempDir()
	runGitCommit(t, dir, "init", "-q")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".cloche", "intent"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".cloche", "intent", "domains.yaml"), []byte("version: 1\n"), 0644))
	runGitCommit(t, dir, "add", ".")
	runGitCommit(t, dir, "commit", "-q", "-m", "init")

	before := runGitCommit(t, dir, "rev-parse", "HEAD")
	out, err := runCommitScript(t, dir, "")
	require.NoError(t, err, out)
	assert.Contains(t, out, "nothing to commit")

	after := runGitCommit(t, dir, "rev-parse", "HEAD")
	assert.Equal(t, before, after, "a clean .cloche/intent/ must not produce a commit")
}

func TestBuiltinWorkflow_CommitScript_ScopedToIntentDir(t *testing.T) {
	dir := t.TempDir()
	runGitCommit(t, dir, "init", "-q")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".cloche", "intent"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".cloche", "intent", "domains.yaml"), []byte("version: 1\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0644))
	runGitCommit(t, dir, "add", ".")
	runGitCommit(t, dir, "commit", "-q", "-m", "init")

	// Simulate what apply-reconcile leaves behind: a modified domains.yaml
	// and a brand-new (untracked) requirement file.
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".cloche", "intent", "domains.yaml"), []byte("version: 2\n"), 0644))
	reqDir := filepath.Join(dir, ".cloche", "intent", "requirements")
	require.NoError(t, os.MkdirAll(reqDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(reqDir, "req-abcd.md"), []byte("# req-abcd\n"), 0644))

	// Unrelated dirty file that must survive the commit untouched.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello again\n"), 0644))

	prevOutputFile := filepath.Join(t.TempDir(), "apply-reconcile.log")
	require.NoError(t, os.WriteFile(prevOutputFile, []byte("created 1, superseded 0, merged 0, dropped 0\n"), 0644))

	out, err := runCommitScript(t, dir, prevOutputFile)
	require.NoError(t, err, out)
	assert.Contains(t, out, "committed")

	status := runGitCommit(t, dir, "status", "--porcelain")
	assert.Contains(t, status, "README.md", "unrelated dirty file must remain uncommitted")
	assert.NotContains(t, status, ".cloche/intent", "all .cloche/intent/ changes should have been committed")

	msg := strings.TrimSpace(runGitCommit(t, dir, "log", "-1", "--format=%s"))
	assert.Equal(t, "intent scan: created 1, superseded 0, merged 0, dropped 0; domains.yaml updated", msg)

	stat := runGitCommit(t, dir, "show", "--stat", "-1")
	assert.Contains(t, stat, "req-abcd.md")
	assert.Contains(t, stat, "domains.yaml")
	assert.NotContains(t, stat, "README.md")
}

func TestBuiltinWorkflow_IndependentInstances(t *testing.T) {
	a := scan.BuiltinWorkflow()
	b := scan.BuiltinWorkflow()

	mapPtr := func(m map[string]string) uintptr { return reflect.ValueOf(m).Pointer() }
	assert.NotEqual(t, mapPtr(a.Config), mapPtr(b.Config))
	for name := range a.Steps {
		assert.NotEqualf(t, mapPtr(a.Steps[name].Config), mapPtr(b.Steps[name].Config), "step %s Config", name)
	}

	a.Steps["discover-domains"].Config["timeout"] = "mutated"
	assert.NotEqual(t, "mutated", b.Steps["discover-domains"].Config["timeout"])
}

// scriptedExecutor is a fake engine.StepExecutor driven by a fixed
// stepName -> result map, recording which steps actually ran so tests can
// assert on the workflow's wiring behavior without invoking real agents or
// scripts.
type scriptedExecutor struct {
	results  map[string]string
	executed []string
}

func (e *scriptedExecutor) Execute(ctx context.Context, step *domain.Step) (domain.StepResult, error) {
	if step.Type == domain.StepTypeWorkflow {
		return runSubWorkflow(ctx, e, step)
	}
	e.executed = append(e.executed, step.Name)
	result, ok := e.results[step.Name]
	if !ok {
		return domain.StepResult{}, nil
	}
	return domain.StepResult{Result: result}, nil
}

// runSubWorkflow stands in for the daemon executor's handling of a
// workflow-type step: run the named built-in as a nested engine run with
// the same executor (so the inner steps are recorded in order) and map its
// final state to success/fail, exactly as the daemon does.
func runSubWorkflow(ctx context.Context, exec engine.StepExecutor, step *domain.Step) (domain.StepResult, error) {
	sub, ok := builtin.Lookup(step.Config["workflow_name"])
	if !ok {
		return domain.StepResult{}, fmt.Errorf("unknown sub-workflow %q", step.Config["workflow_name"])
	}
	run, err := engine.New(exec).Run(ctx, sub)
	if err != nil || run.State != domain.RunStateSucceeded {
		return domain.StepResult{Result: "fail"}, nil
	}
	return domain.StepResult{Result: "success"}, nil
}

// TestBuiltinWorkflow_ReconcileNone_SkipsApplyReconcile reproduces the "zero
// candidates" case from cloche-26029ae7feb8: when reconcile has nothing to
// act on, it must route via its `none` result straight to done, the same way
// collect-sources does, rather than falling through to apply-reconcile with
// no reconcile.json to apply.
func TestBuiltinWorkflow_ReconcileNone_SkipsApplyReconcile(t *testing.T) {
	calls := 0
	exec := &sequencedExecutor{results: map[string][]string{
		"discover-domains": {"success"},
		"check-domains":    {"success"},
		"collect-sources":  {"success"},
		"next-pass":        {"pass", "done"},
		"extract":          {"success"},
		"check-candidates": {"success"},
		"reconcile":        {"none"},
		"commit":           {"success"},
	}, calls: &calls}

	eng := engine.New(exec)
	run, err := eng.Run(context.Background(), scan.BuiltinWorkflow())
	require.NoError(t, err)

	assert.Equal(t, domain.RunStateSucceeded, run.State)
	assert.NotContains(t, exec.executed, "apply-reconcile", "apply-reconcile must not run when reconcile reports no candidates")
	assert.Contains(t, exec.executed, "commit", "commit must still run so scan-state.yaml/domains.yaml don't stay dirty")
}

// TestBuiltinWorkflow_ApplyReconcileFail_FailsRun reproduces the observed bug
// at the wiring level: a reconcile step that reports "success" but whose
// apply-reconcile step then fails (e.g. because reconcile.json was never
// written) must fail the whole run via the declared fail -> abort wire.
func TestBuiltinWorkflow_ApplyReconcileFail_FailsRun(t *testing.T) {
	calls := 0
	exec := &sequencedExecutor{results: map[string][]string{
		"discover-domains": {"success"},
		"check-domains":    {"success"},
		"collect-sources":  {"success"},
		"next-pass":        {"pass", "done"},
		"extract":          {"success"},
		"check-candidates": {"success"},
		"reconcile":        {"success"},
		"check-reconcile":  {"success"},
		"apply-reconcile":  {"fail"},
		"abort-cleanup":    {"success"},
	}, calls: &calls}

	eng := engine.New(exec)
	run, err := eng.Run(context.Background(), scan.BuiltinWorkflow())
	require.NoError(t, err)

	assert.Equal(t, domain.RunStateFailed, run.State)
	assert.Equal(t, "scan-pass", run.FindFirstFailedStep(), "the failed pass is what the outer run reports")
	assert.Contains(t, exec.executed, "apply-reconcile")
	assert.NotContains(t, exec.executed, "commit", "commit must not run after apply-reconcile fails")
	assert.Contains(t, exec.executed, "abort-cleanup", "abort-cleanup must leave the worktree clean")
}

// TestBuiltinWorkflow_ReconcileClaimsSuccessWithoutOutput_RetriesThenAborts
// covers a reconcile agent that keeps reporting success without writing
// reconcile.json: check-reconcile routes it back to reconcile, and
// reconcile's max_attempts bounds the loop so the run aborts on give-up
// instead of spinning or reaching apply-reconcile.
func TestBuiltinWorkflow_ReconcileClaimsSuccessWithoutOutput_RetriesThenAborts(t *testing.T) {
	calls := 0
	exec := &sequencedExecutor{results: map[string][]string{
		"discover-domains": {"success"},
		"check-domains":    {"success"},
		"collect-sources":  {"success"},
		"next-pass":        {"pass", "done"},
		"extract":          {"success"},
		"check-candidates": {"success"},
		"reconcile":        {"success"},
		"check-reconcile":  {"missing"},
		"abort-cleanup":    {"success"},
	}, calls: &calls}

	eng := engine.New(exec)
	run, err := eng.Run(context.Background(), scan.BuiltinWorkflow())
	require.NoError(t, err)

	assert.Equal(t, domain.RunStateFailed, run.State)
	reconciles := 0
	for _, name := range exec.executed {
		if name == "reconcile" {
			reconciles++
		}
	}
	assert.Equal(t, 3, reconciles, "reconcile should run exactly max_attempts times")
	assert.NotContains(t, exec.executed, "apply-reconcile")
}

// TestCheckScriptsDelegateToCLI: the check steps are thin wrappers around
// `cloche intent check-*` (which validate the agents' CSV hand-offs and
// write the JSON the apply step reads) — the logic lives in Go, not shell.
func TestCheckScriptsDelegateToCLI(t *testing.T) {
	wf := scan.BuiltinWorkflow()
	repo := scan.BuiltinRepoWorkflow()
	assert.Contains(t, wf.Steps["check-domains"].Config["run"], `cloche intent apply-domains --project "$PROJECT_DIR" --csv "$TEMP/domains.csv"`)
	assert.Contains(t, wf.Steps["next-pass"].Config["run"], `cloche intent next-pass --sources "$OUT" --cursor "$TEMP/intent-scan-pass" --temp "$TEMP"`)
	// The per-pass steps hand the pass's repo to the CLI, which forces it
	// onto every candidate and gates reconcile targets by it.
	assert.Contains(t, repo.Steps["check-candidates"].Config["run"], `cloche intent check-candidates --project "$PROJECT_DIR" --repo "$REPO" --csv "$TEMP/candidates.csv" --out "$TEMP/candidates.json"`)
	assert.Contains(t, repo.Steps["check-reconcile"].Config["run"], `cloche intent check-reconcile --project "$PROJECT_DIR" --repo "$REPO" --csv "$TEMP/reconcile.csv" --candidates-file "$TEMP/candidates.json" --out "$TEMP/reconcile.json"`)
	assert.Contains(t, repo.Steps["apply-reconcile"].Config["run"], `--repo "$REPO" --reconcile-file "$TEMP/reconcile.json" --candidates-file "$TEMP/candidates.json" --summary-file "$TEMP/scan-summary.txt"`)
	assert.Contains(t, wf.Steps["commit"].Config["run"], `cloche intent validate --project "$PROJECT_DIR"`)
}

// Every path out of the scan must leave .cloche/intent/ committed: the
// `none` exits skip apply-reconcile but collect-sources has already advanced
// scan-state.yaml (cloche's own history has hand-made "update
// scan-state.yaml" commits from exactly this leak).
func TestBuiltinWorkflow_CollectSourcesNone_StillCommits(t *testing.T) {
	exec := &scriptedExecutor{results: map[string]string{
		"discover-domains": "success",
		"check-domains":    "success",
		"collect-sources":  "none",
		"commit":           "success",
	}}

	eng := engine.New(exec)
	run, err := eng.Run(context.Background(), scan.BuiltinWorkflow())
	require.NoError(t, err)

	assert.Equal(t, domain.RunStateSucceeded, run.State)
	assert.Equal(t, []string{"discover-domains", "check-domains", "collect-sources", "commit"}, exec.executed)
}

func TestBuiltinWorkflow_CommitScript_IgnoresNonSummaryPrevOutput(t *testing.T) {
	dir := t.TempDir()
	runGitCommit(t, dir, "init", "-q")
	intentDir := filepath.Join(dir, ".cloche", "intent")
	require.NoError(t, os.MkdirAll(intentDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(intentDir, "scan-state.yaml"), []byte("cursor: 1\n"), 0644))
	runGitCommit(t, dir, "add", ".")
	runGitCommit(t, dir, "commit", "-q", "-m", "init")

	// collect-sources advanced the cursor, then reconcile reported none: the
	// predecessor log is an agent transcript, not an apply-reconcile summary.
	require.NoError(t, os.WriteFile(filepath.Join(intentDir, "scan-state.yaml"), []byte("cursor: 2\n"), 0644))
	prevOutputFile := filepath.Join(t.TempDir(), "reconcile.log")
	require.NoError(t, os.WriteFile(prevOutputFile, []byte(`{"type":"result","result":"nothing to do"}`+"\n"), 0644))

	out, err := runCommitScript(t, dir, prevOutputFile)
	require.NoError(t, err, out)

	msg := strings.TrimSpace(runGitCommit(t, dir, "log", "-1", "--format=%s"))
	assert.Equal(t, "intent scan: scan state updated", msg)
	assert.Empty(t, runGitCommit(t, dir, "status", "--porcelain"))
}

// runAbortCleanupScript mirrors runCommitScript for the abort-cleanup step.
func runAbortCleanupScript(t *testing.T, dir string) (string, error) {
	t.Helper()
	return runIntentDirScript(t, "abort-cleanup", dir, "")
}

func TestBuiltinWorkflow_AbortCleanupScript_RevertsCursorCommitsRest(t *testing.T) {
	dir := t.TempDir()
	runGitCommit(t, dir, "init", "-q")
	intentDir := filepath.Join(dir, ".cloche", "intent")
	require.NoError(t, os.MkdirAll(intentDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(intentDir, "scan-state.yaml"), []byte("cursor: 1\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(intentDir, "domains.yaml"), []byte("version: 1\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0644))
	runGitCommit(t, dir, "add", ".")
	runGitCommit(t, dir, "commit", "-q", "-m", "init")

	// discover-domains rewrote domains.yaml, collect-sources advanced the
	// cursor, then extract failed. An unrelated dirty file must survive.
	require.NoError(t, os.WriteFile(filepath.Join(intentDir, "scan-state.yaml"), []byte("cursor: 2\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(intentDir, "domains.yaml"), []byte("version: 2\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello again\n"), 0644))

	out, err := runAbortCleanupScript(t, dir)
	require.NoError(t, err, out)
	assert.Contains(t, out, "reverted")
	assert.Contains(t, out, "committed")

	state, err := os.ReadFile(filepath.Join(intentDir, "scan-state.yaml"))
	require.NoError(t, err)
	assert.Equal(t, "cursor: 1\n", string(state), "cursor must be rewound so the failed window is rescanned")

	status := runGitCommit(t, dir, "status", "--porcelain")
	assert.Contains(t, status, "README.md")
	assert.NotContains(t, status, ".cloche/intent")
	msg := strings.TrimSpace(runGitCommit(t, dir, "log", "-1", "--format=%s"))
	assert.Equal(t, "intent scan: partial results (scan aborted); domains.yaml updated", msg)
}

func TestBuiltinWorkflow_AbortCleanupScript_NoOpWhenClean(t *testing.T) {
	dir := t.TempDir()
	runGitCommit(t, dir, "init", "-q")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".cloche", "intent"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".cloche", "intent", "domains.yaml"), []byte("version: 1\n"), 0644))
	runGitCommit(t, dir, "add", ".")
	runGitCommit(t, dir, "commit", "-q", "-m", "init")

	before := runGitCommit(t, dir, "rev-parse", "HEAD")
	out, err := runAbortCleanupScript(t, dir)
	require.NoError(t, err, out)
	assert.Equal(t, before, runGitCommit(t, dir, "rev-parse", "HEAD"))
}

// TestBuiltinWorkflow_DomainsRepairLoop: a domains.csv that fails validation
// goes to repair-domains and back to check-domains, never straight to abort.
func TestBuiltinWorkflow_DomainsRepairLoop(t *testing.T) {
	calls := 0
	exec := &sequencedExecutor{results: map[string][]string{
		"discover-domains": {"success"},
		"check-domains":    {"fail", "success"},
		"repair-domains":   {"success"},
		"collect-sources":  {"none"},
		"commit":           {"success"},
	}, calls: &calls}

	eng := engine.New(exec)
	run, err := eng.Run(context.Background(), scan.BuiltinWorkflow())
	require.NoError(t, err)

	assert.Equal(t, domain.RunStateSucceeded, run.State)
	assert.Equal(t, []string{"discover-domains", "check-domains", "repair-domains", "check-domains", "collect-sources", "commit"}, exec.executed)
}

// sequencedExecutor is scriptedExecutor with a per-step result sequence, for
// steps that are expected to run more than once.
type sequencedExecutor struct {
	results  map[string][]string
	executed []string
	calls    *int
}

func (e *sequencedExecutor) Execute(ctx context.Context, step *domain.Step) (domain.StepResult, error) {
	if step.Type == domain.StepTypeWorkflow {
		return runSubWorkflow(ctx, e, step)
	}
	e.executed = append(e.executed, step.Name)
	seq := e.results[step.Name]
	if len(seq) == 0 {
		return domain.StepResult{}, nil
	}
	r := seq[0]
	if len(seq) > 1 {
		e.results[step.Name] = seq[1:]
	}
	return domain.StepResult{Result: r}, nil
}

// TestBuiltinWorkflow_ReconcileRepairLoop: a malformed reconcile.csv goes to
// repair-reconcile (format-only) and back through check-reconcile to apply —
// the whole reconcile judgment is not re-run for a formatting problem.
func TestBuiltinWorkflow_ReconcileRepairLoop(t *testing.T) {
	calls := 0
	exec := &sequencedExecutor{results: map[string][]string{
		"discover-domains": {"success"},
		"check-domains":    {"success"},
		"collect-sources":  {"success"},
		"next-pass":        {"pass", "done"},
		"extract":          {"success"},
		"check-candidates": {"success"},
		"reconcile":        {"success"},
		"check-reconcile":  {"fail", "success"},
		"repair-reconcile": {"success"},
		"apply-reconcile":  {"success"},
		"commit":           {"success"},
	}, calls: &calls}

	eng := engine.New(exec)
	run, err := eng.Run(context.Background(), scan.BuiltinWorkflow())
	require.NoError(t, err)

	assert.Equal(t, domain.RunStateSucceeded, run.State)
	assert.Equal(t, []string{"discover-domains", "check-domains", "collect-sources", "next-pass", "extract", "check-candidates",
		"reconcile", "check-reconcile", "repair-reconcile", "check-reconcile", "apply-reconcile", "next-pass", "commit"}, exec.executed)
}

// A project that committed .cloche/intent-index/vectors.json before the
// index became self-ignoring sees it modified after every task run — the
// daemon rewrites it on injection and .gitignore has no effect on a tracked
// file. The commit step is where cloche already commits intent state, so it
// untracks the index there, even when nothing under .cloche/intent/ changed.
func TestBuiltinWorkflow_CommitScript_UntracksCommittedIndex(t *testing.T) {
	dir := t.TempDir()
	runGitCommit(t, dir, "init", "-q")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".cloche", "intent"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".cloche", "intent", "domains.yaml"), []byte("version: 1\n"), 0644))
	indexDir := filepath.Join(dir, ".cloche", "intent-index")
	require.NoError(t, os.MkdirAll(indexDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(indexDir, "vectors.json"), []byte(`{"version":1}`), 0644))
	runGitCommit(t, dir, "add", ".")
	runGitCommit(t, dir, "commit", "-q", "-m", "init")

	// What a task run leaves behind: the daemon re-saved the index (and made
	// the directory self-ignoring), nothing under .cloche/intent/ changed.
	require.NoError(t, os.WriteFile(filepath.Join(indexDir, "vectors.json"), []byte(`{"version":1,"entries":[]}`), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(indexDir, ".gitignore"), []byte("*\n"), 0644))

	out, err := runCommitScript(t, dir, "")
	require.NoError(t, err, out)
	assert.Contains(t, out, "untracked derived index")
	assert.Contains(t, out, "nothing to commit", "the intent dir itself was clean")

	assert.Empty(t, runGitCommit(t, dir, "ls-files", "--", ".cloche/intent-index"), "index must no longer be tracked")
	assert.Empty(t, runGitCommit(t, dir, "status", "--porcelain"), "working tree must be clean afterwards")
	assert.FileExists(t, filepath.Join(indexDir, "vectors.json"), "the on-disk cache must survive")
	assert.Contains(t, runGitCommit(t, dir, "log", "-1", "--format=%s"), "stop tracking")
	assert.Contains(t, runGitCommit(t, dir, "show", "--stat", "--format=", "HEAD"), "vectors.json")
}

// Two passes, each of which needs a reconcile retry: the per-pass
// sub-workflow gives every pass its own max_attempts budget, which an
// in-workflow loop back to extract would not — the second repo would hit
// give-up on the attempts the first one used up.
func TestBuiltinWorkflow_EachPassGetsFreshAttempts(t *testing.T) {
	calls := 0
	exec := &sequencedExecutor{results: map[string][]string{
		"discover-domains": {"success"},
		"check-domains":    {"success"},
		"collect-sources":  {"success"},
		"next-pass":        {"pass", "pass", "done"},
		"extract":          {"success"},
		"check-candidates": {"success"},
		"reconcile":        {"success"},
		"check-reconcile":  {"missing", "missing", "success", "missing", "missing", "success"},
		"apply-reconcile":  {"success"},
		"commit":           {"success"},
	}, calls: &calls}

	run, err := engine.New(exec).Run(context.Background(), scan.BuiltinWorkflow())
	require.NoError(t, err)
	assert.Equal(t, domain.RunStateSucceeded, run.State)

	reconciles, applies := 0, 0
	for _, name := range exec.executed {
		switch name {
		case "reconcile":
			reconciles++
		case "apply-reconcile":
			applies++
		}
	}
	assert.Equal(t, 6, reconciles, "three reconcile attempts per pass, twice")
	assert.Equal(t, 2, applies)
	assert.Equal(t, "commit", exec.executed[len(exec.executed)-1])
}
