package scan_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
