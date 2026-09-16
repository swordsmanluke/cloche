package scan_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	assert.Len(t, wf.Steps, 6)

	wantWires := map[string]string{
		"discover-domains:success": "collect-sources",
		"discover-domains:fail":    "abort",
		"collect-sources:success":  "extract",
		"collect-sources:none":     "done",
		"collect-sources:fail":     "abort",
		"extract:success":          "reconcile",
		"extract:fail":             "abort",
		"reconcile:success":        "apply-reconcile",
		"reconcile:none":           "done",
		"reconcile:fail":           "abort",
		"apply-reconcile:success":  "commit",
		"apply-reconcile:fail":     "abort",
		"commit:success":           "done",
		"commit:fail":              "abort",
	}
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
	wf := scan.BuiltinWorkflow()

	for _, name := range []string{"collect-sources", "apply-reconcile", "commit"} {
		step, ok := wf.Steps[name]
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
	script := scan.BuiltinWorkflow().Steps["commit"].Config["run"]
	cmd := exec.Command("sh", "-c", script)
	cmd.Dir = dir
	env := append(os.Environ(), "CLOCHE_PROJECT_DIR="+dir,
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

	a.Steps["extract"].Config["timeout"] = "mutated"
	assert.NotEqual(t, "mutated", b.Steps["extract"].Config["timeout"])
}

// scriptedExecutor is a fake engine.StepExecutor driven by a fixed
// stepName -> result map, recording which steps actually ran so tests can
// assert on the workflow's wiring behavior without invoking real agents or
// scripts.
type scriptedExecutor struct {
	results  map[string]string
	executed []string
}

func (e *scriptedExecutor) Execute(_ context.Context, step *domain.Step) (domain.StepResult, error) {
	e.executed = append(e.executed, step.Name)
	result, ok := e.results[step.Name]
	if !ok {
		return domain.StepResult{}, nil
	}
	return domain.StepResult{Result: result}, nil
}

// TestBuiltinWorkflow_ReconcileNone_SkipsApplyReconcile reproduces the "zero
// candidates" case from cloche-26029ae7feb8: when reconcile has nothing to
// act on, it must route via its `none` result straight to done, the same way
// collect-sources does, rather than falling through to apply-reconcile with
// no reconcile.json to apply.
func TestBuiltinWorkflow_ReconcileNone_SkipsApplyReconcile(t *testing.T) {
	exec := &scriptedExecutor{results: map[string]string{
		"discover-domains": "success",
		"collect-sources":  "success",
		"extract":          "success",
		"reconcile":        "none",
	}}

	eng := engine.New(exec)
	run, err := eng.Run(context.Background(), scan.BuiltinWorkflow())
	require.NoError(t, err)

	assert.Equal(t, domain.RunStateSucceeded, run.State)
	assert.NotContains(t, exec.executed, "apply-reconcile", "apply-reconcile must not run when reconcile reports no candidates")
	assert.NotContains(t, exec.executed, "commit")
}

// TestBuiltinWorkflow_ApplyReconcileFail_FailsRun reproduces the observed bug
// at the wiring level: a reconcile step that reports "success" but whose
// apply-reconcile step then fails (e.g. because reconcile.json was never
// written) must fail the whole run via the declared fail -> abort wire.
func TestBuiltinWorkflow_ApplyReconcileFail_FailsRun(t *testing.T) {
	exec := &scriptedExecutor{results: map[string]string{
		"discover-domains": "success",
		"collect-sources":  "success",
		"extract":          "success",
		"reconcile":        "success",
		"apply-reconcile":  "fail",
	}}

	eng := engine.New(exec)
	run, err := eng.Run(context.Background(), scan.BuiltinWorkflow())
	require.NoError(t, err)

	assert.Equal(t, domain.RunStateFailed, run.State)
	assert.Equal(t, "apply-reconcile", run.FindFirstFailedStep())
	assert.NotContains(t, exec.executed, "commit", "commit must not run after apply-reconcile fails")
}
