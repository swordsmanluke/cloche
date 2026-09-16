package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pb "github.com/swordsmanluke/cloche/api/clochepb"
	"github.com/swordsmanluke/cloche/internal/config"
	"github.com/swordsmanluke/cloche/internal/domain"
	"github.com/swordsmanluke/cloche/internal/intent"
	"github.com/swordsmanluke/cloche/internal/intent/scan"
	"google.golang.org/grpc"
)

func runGitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func TestPrintResultMarker_FramesWithNonceWhenSet(t *testing.T) {
	t.Setenv("CLOCHE_RESULT_NONCE", "abc123")

	out := captureStdout(t, func() { printResultMarker("none") })

	if strings.TrimSpace(out) != "CLOCHE_RESULT:abc123:none" {
		t.Fatalf("got %q, want nonce-framed marker", out)
	}
}

func TestPrintResultMarker_FallsBackToBareWhenNonceUnset(t *testing.T) {
	t.Setenv("CLOCHE_RESULT_NONCE", "")

	out := captureStdout(t, func() { printResultMarker("success") })

	if strings.TrimSpace(out) != "CLOCHE_RESULT:success" {
		t.Fatalf("got %q, want bare marker", out)
	}
}

func TestRunIntentCollectSources_QuietThenNew(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out")

	c, stats, err := runIntentCollectSources(dir, out, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.HasNew() {
		t.Fatalf("expected a quiet scan on an empty project")
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("expected no output dir written for a quiet scan")
	}
	if len(stats.Repos) != 1 || stats.Repos[0].Name != "" || !stats.Repos[0].Empty() {
		t.Fatalf("expected one empty root-repo RepoStats, got %+v", stats.Repos)
	}

	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	c2, stats2, err := runIntentCollectSources(dir, out, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !c2.HasNew() {
		t.Fatalf("expected new material after adding CLAUDE.md")
	}
	if _, err := os.Stat(filepath.Join(out, "docs", "CLAUDE.md")); err != nil {
		t.Fatalf("expected collected doc written to out dir: %v", err)
	}
	if len(stats2.Repos) != 1 || stats2.Repos[0].DocsNew != 1 {
		t.Fatalf("expected 1 new doc in root-repo stats, got %+v", stats2.Repos)
	}

	// Cursor should have advanced so a third run is quiet again.
	c3, stats3, err := runIntentCollectSources(dir, out, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c3.HasNew() {
		t.Fatalf("expected the scan-state cursor to have advanced past CLAUDE.md")
	}
	if len(stats3.Repos) != 1 || !stats3.Repos[0].Empty() {
		t.Fatalf("expected an empty root-repo RepoStats on the quiet re-scan, got %+v", stats3.Repos)
	}

	// LastScanAt should now be populated after these runs.
	st, err := intent.NewStore(dir).LoadScanState()
	if err != nil {
		t.Fatalf("unexpected error loading scan state: %v", err)
	}
	if st.LastScanAt.IsZero() {
		t.Fatalf("expected LastScanAt to be set after a scan")
	}
}

// TestRunIntentCollectSources_FullResetsCursors covers the ticket's
// acceptance criterion: a --full run re-mines the doc, commit, and run
// material scan-state.yaml already recorded, without leaving the cursor
// stuck re-mining forever afterward.
func TestRunIntentCollectSources_FullResetsCursors(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out")

	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
		)
		if cmdOut, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, cmdOut)
		}
	}
	runGit("init", "-q")
	runGit("add", "CLAUDE.md")
	runGit("commit", "-q", "-m", "Add CLAUDE.md")

	runDir := filepath.Join(dir, ".cloche", "runs", "run-1")
	if err := os.MkdirAll(runDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "task_prompt.md"), []byte("do the thing"), 0644); err != nil {
		t.Fatal(err)
	}

	// First (incremental) run records the doc, commit, and run into
	// scan-state.yaml's cursors.
	c1, _, err := runIntentCollectSources(dir, out, false)
	if err != nil {
		t.Fatalf("first collect-sources: %v", err)
	}
	if len(c1.Docs) != 1 || len(c1.Commits) != 1 || len(c1.Runs) != 1 {
		t.Fatalf("expected the first scan to pick up the doc, commit, and run, got docs=%d commits=%d runs=%d",
			len(c1.Docs), len(c1.Commits), len(c1.Runs))
	}

	// A normal incremental run is now quiet — everything is already
	// recorded in scan-state.yaml.
	c2, _, err := runIntentCollectSources(dir, out, false)
	if err != nil {
		t.Fatalf("second collect-sources: %v", err)
	}
	if c2.HasNew() {
		t.Fatalf("expected the second (incremental) scan to be quiet, got docs=%d commits=%d runs=%d",
			len(c2.Docs), len(c2.Commits), len(c2.Runs))
	}

	// --full re-mines the doc, commit, and run despite scan-state already
	// recording them.
	c3, _, err := runIntentCollectSources(dir, out, true)
	if err != nil {
		t.Fatalf("full collect-sources: %v", err)
	}
	if len(c3.Docs) != 1 || c3.Docs[0].Path != "CLAUDE.md" {
		t.Fatalf("expected --full to re-collect CLAUDE.md, got %+v", c3.Docs)
	}
	if len(c3.Commits) != 1 {
		t.Fatalf("expected --full to re-collect the commit, got %+v", c3.Commits)
	}
	if len(c3.Runs) != 1 || c3.Runs[0].ID != "run-1" {
		t.Fatalf("expected --full to re-collect run-1, got %+v", c3.Runs)
	}

	// After --full, cursors are back in sync with current state — a
	// following incremental run is quiet again rather than re-mining
	// forever.
	c4, _, err := runIntentCollectSources(dir, out, false)
	if err != nil {
		t.Fatalf("fourth collect-sources: %v", err)
	}
	if c4.HasNew() {
		t.Fatalf("expected the scan-state cursor to be re-synced after --full, got docs=%d commits=%d runs=%d",
			len(c4.Docs), len(c4.Commits), len(c4.Runs))
	}
}

// TestRunIntentCollectSources_MultiRepo reproduces the wrapper-project gap
// from docs/intent.md's "Multi-repo projects" section: a thin orchestration
// wrapper with a repository checked out under repos/<name>/, declared via
// [[repositories]] in .cloche/config.toml. Before this feature,
// collect-sources never descended into repos/anarkana/ at all; now it must
// pick up that repo's own doc and commit.
func TestRunIntentCollectSources_MultiRepo(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out")

	if err := os.MkdirAll(filepath.Join(dir, ".cloche"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".cloche", "config.toml"), []byte(`
[[repositories]]
name = "anarkana"
path = "./repos/anarkana"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	repoDir := filepath.Join(dir, "repos", "anarkana")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitCmd(t, repoDir, "init", "-q")
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("the real history lives here"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitCmd(t, repoDir, "add", "README.md")
	runGitCmd(t, repoDir, "commit", "-q", "-m", "Add README")

	c, _, err := runIntentCollectSources(dir, out, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !c.HasNew() {
		t.Fatalf("expected the repo's own doc and commit to be collected")
	}

	var docPaths []string
	for _, d := range c.Docs {
		docPaths = append(docPaths, d.Path)
	}
	if len(docPaths) != 1 || docPaths[0] != "repos/anarkana/README.md" {
		t.Fatalf("expected repo-qualified doc path, got %v", docPaths)
	}

	if len(c.Commits) != 1 || c.Commits[0].Ref() == "" || !strings.HasPrefix(c.Commits[0].Ref(), "repos/anarkana@") {
		t.Fatalf("expected repo-qualified commit ref, got %+v", c.Commits)
	}

	if _, err := os.Stat(filepath.Join(out, "docs", "repos", "anarkana", "README.md")); err != nil {
		t.Fatalf("expected collected doc written under out/docs/repos/anarkana/: %v", err)
	}

	// The wrapper's own project root has no docs/commits in this fixture,
	// so a second run without any wrapper-root or repo change is quiet
	// again — the per-repo cursor advanced independently for both sources.
	c2, _, err := runIntentCollectSources(dir, out, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c2.HasNew() {
		t.Fatalf("expected the per-repo cursor to have advanced past the repo's README and commit")
	}
}

func TestExcludedIntentTrackingSteps_StepLevel(t *testing.T) {
	dir := t.TempDir()
	clocheDir := filepath.Join(dir, ".cloche")
	if err := os.MkdirAll(clocheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	wfSrc := `workflow develop {
  step implement {
    run = "true"
    results = [success, fail]
  }

  step sanitize {
    run = "true"
    intent_tracking = false
    results = [success, fail]
  }

  implement:success -> sanitize
  implement:fail -> abort
  sanitize:success -> done
  sanitize:fail -> abort
}
`
	if err := os.WriteFile(filepath.Join(clocheDir, "develop.cloche"), []byte(wfSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	excluded := excludedIntentTrackingSteps(dir)
	if !excluded["sanitize"] {
		t.Fatalf("expected sanitize to be excluded, got %v", excluded)
	}
	if excluded["implement"] {
		t.Fatalf("did not expect implement to be excluded, got %v", excluded)
	}
}

func TestExcludedIntentTrackingSteps_WorkflowLevel(t *testing.T) {
	dir := t.TempDir()
	clocheDir := filepath.Join(dir, ".cloche")
	if err := os.MkdirAll(clocheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	wfSrc := `workflow develop {
  intent_tracking = false

  step implement {
    run = "true"
    results = [success, fail]
  }

  implement:success -> done
  implement:fail -> abort
}
`
	if err := os.WriteFile(filepath.Join(clocheDir, "develop.cloche"), []byte(wfSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	excluded := excludedIntentTrackingSteps(dir)
	if !excluded["implement"] {
		t.Fatalf("expected implement to be excluded via workflow-level intent_tracking = false, got %v", excluded)
	}
}

func TestRunIntentApplyReconcile(t *testing.T) {
	dir := t.TempDir()
	reconcile := []map[string]any{
		{
			"action":     "create",
			"statement":  "Never bump major without asking.",
			"confidence": "high",
			"scope":      map[string]any{"level": "project"},
			"provenance": map[string]any{"kind": "doc", "ref": "CLAUDE.md"},
		},
	}
	data, err := json.Marshal(map[string]any{"actions": reconcile})
	if err != nil {
		t.Fatal(err)
	}
	reconcilePath := filepath.Join(dir, "reconcile.json")
	if err := os.WriteFile(reconcilePath, data, 0644); err != nil {
		t.Fatal(err)
	}

	report, err := runIntentApplyReconcile(dir, reconcilePath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(report.Created) != 1 {
		t.Fatalf("expected 1 created requirement, got %d", len(report.Created))
	}

	if _, err := os.Stat(filepath.Join(dir, ".cloche", "intent", "requirements", report.Created[0]+".md")); err != nil {
		t.Fatalf("expected requirement file written: %v", err)
	}
}

func TestRunIntentApplyReconcile_HardRuleViolationAppliesNothing(t *testing.T) {
	dir := t.TempDir()
	data, err := json.Marshal(map[string]any{
		"actions": []map[string]any{{"action": "delete", "existing_id": "req-0000"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	reconcilePath := filepath.Join(dir, "reconcile.json")
	if err := os.WriteFile(reconcilePath, data, 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := runIntentApplyReconcile(dir, reconcilePath); err == nil {
		t.Fatalf("expected an error for an invalid action")
	}
}

func TestIntentHelpExists(t *testing.T) {
	text, ok := subcommandHelp["intent"]
	if !ok {
		t.Fatal("missing help text for intent subcommand")
	}
	if !strings.Contains(text, "Usage:") {
		t.Error("intent help missing Usage: section")
	}
	if !strings.Contains(text, "Examples:") {
		t.Error("intent help missing Examples: section")
	}
	for _, sub := range []string{"list", "show", "edit", "disable", "enable", "add", "preview", "scan"} {
		if !strings.Contains(text, sub) {
			t.Errorf("intent help should mention %q subcommand", sub)
		}
	}
}

func TestIntentCommand_UnknownSubcommand(t *testing.T) {
	var buf bytes.Buffer
	err := intentCommand("bogus", nil, &buf)
	if err == nil {
		t.Fatal("expected an error for an unknown subcommand")
	}
}

func TestIntentList_EmptyStoreIsDormant(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	if err := intentListCommand([]string{"--project", dir}, &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No requirements found.") {
		t.Errorf("expected dormant message, got: %s", buf.String())
	}
}

func TestIntentList_FiltersByDomainAndStatus(t *testing.T) {
	dir := t.TempDir()
	store := intent.NewStore(dir)
	mustCreate(t, store, intent.StatusActive, intent.Scope{Level: intent.ScopeLevelProject}, "Project-wide statement.")
	mustCreate(t, store, intent.StatusActive, intent.Scope{Level: intent.ScopeLevelDomain, Domains: []string{"versioning"}}, "Versioning statement.")
	mustCreate(t, store, intent.StatusDisabled, intent.Scope{Level: intent.ScopeLevelDomain, Domains: []string{"other"}}, "Disabled statement.")

	var buf bytes.Buffer
	if err := intentListCommand([]string{"--project", dir, "--domain", "versioning"}, &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Versioning statement.") {
		t.Errorf("expected versioning requirement in output, got: %s", out)
	}
	if strings.Contains(out, "Disabled statement.") {
		t.Errorf("did not expect the 'other'-domain requirement, got: %s", out)
	}

	buf.Reset()
	if err := intentListCommand([]string{"--project", dir, "--status", "disabled"}, &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out = buf.String()
	if !strings.Contains(out, "Disabled statement.") {
		t.Errorf("expected disabled requirement, got: %s", out)
	}
	if strings.Contains(out, "Project-wide statement.") {
		t.Errorf("did not expect the active requirement, got: %s", out)
	}
}

func TestIntentShow(t *testing.T) {
	dir := t.TempDir()
	store := intent.NewStore(dir)
	req := mustCreate(t, store, intent.StatusActive, intent.Scope{Level: intent.ScopeLevelProject}, "Never bump the major version unless explicitly told to.")

	var buf bytes.Buffer
	if err := intentShowCommand([]string{"--project", dir, req.ID}, &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, req.ID) {
		t.Errorf("expected id in output, got: %s", out)
	}
	if !strings.Contains(out, "Never bump the major version unless explicitly told to.") {
		t.Errorf("expected body in output, got: %s", out)
	}
	if !strings.Contains(out, "Status:      active") {
		t.Errorf("expected status line, got: %s", out)
	}
}

func TestIntentShow_UnknownID(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	if err := intentShowCommand([]string{"--project", dir, "req-0000"}, &buf); err == nil {
		t.Fatal("expected an error for an unknown requirement id")
	}
}

func TestIntentDisableEnable(t *testing.T) {
	dir := t.TempDir()
	store := intent.NewStore(dir)
	req := mustCreate(t, store, intent.StatusActive, intent.Scope{Level: intent.ScopeLevelProject}, "Some statement.")

	var buf bytes.Buffer
	if err := intentSetStatusCommand([]string{"--project", dir, req.ID}, &buf, intent.StatusDisabled); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, err := store.GetRequirement(req.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != intent.StatusDisabled {
		t.Errorf("expected status disabled, got %s", got.Status)
	}

	// Idempotent: disabling again reports the state without error.
	buf.Reset()
	if err := intentSetStatusCommand([]string{"--project", dir, req.ID}, &buf, intent.StatusDisabled); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "already disabled") {
		t.Errorf("expected 'already disabled' message, got: %s", buf.String())
	}

	if err := intentSetStatusCommand([]string{"--project", dir, req.ID}, &buf, intent.StatusActive); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, err = store.GetRequirement(req.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != intent.StatusActive {
		t.Errorf("expected status active after enable, got %s", got.Status)
	}
}

func TestIntentAdd_ProjectAndDomainScope(t *testing.T) {
	dir := t.TempDir()

	var buf bytes.Buffer
	if err := intentAddCommand([]string{"--project", dir, "Project-wide rule."}, &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "Created req-") {
		t.Errorf("expected created message, got: %s", buf.String())
	}

	buf.Reset()
	if err := intentAddCommand([]string{"--project", dir, "Domain-scoped rule.", "--domain", "versioning"}, &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	store := intent.NewStore(dir)
	reqs, err := store.ListRequirements()
	if err != nil {
		t.Fatal(err)
	}
	if len(reqs) != 2 {
		t.Fatalf("expected 2 requirements, got %d", len(reqs))
	}

	var sawProject, sawDomain bool
	for _, r := range reqs {
		if r.Provenance.Kind != intent.ProvenanceUser {
			t.Errorf("expected provenance kind=user, got %s", r.Provenance.Kind)
		}
		if !r.UserEdited {
			t.Errorf("expected user_edited=true for manually added requirement %s", r.ID)
		}
		switch r.Body {
		case "Project-wide rule.":
			sawProject = true
			if r.Scope.Level != intent.ScopeLevelProject {
				t.Errorf("expected project scope, got %v", r.Scope)
			}
		case "Domain-scoped rule.":
			sawDomain = true
			if r.Scope.Level != intent.ScopeLevelDomain || len(r.Scope.Domains) != 1 || r.Scope.Domains[0] != "versioning" {
				t.Errorf("expected domain scope [versioning], got %v", r.Scope)
			}
		}
	}
	if !sawProject || !sawDomain {
		t.Errorf("expected both requirements to be found, sawProject=%v sawDomain=%v", sawProject, sawDomain)
	}
}

func TestIntentAdd_RequiresStatement(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	if err := intentAddCommand([]string{"--project", dir}, &buf); err == nil {
		t.Fatal("expected an error when no statement is given")
	}
}

func TestIntentEdit_SetsUserEditedWithoutChangingContent(t *testing.T) {
	dir := t.TempDir()
	store := intent.NewStore(dir)
	req := mustCreate(t, store, intent.StatusActive, intent.Scope{Level: intent.ScopeLevelProject}, "Original statement.")

	// A no-op editor: exercises the edit path without requiring an
	// interactive terminal.
	t.Setenv("EDITOR", "true")

	var buf bytes.Buffer
	if err := intentEditCommand([]string{"--project", dir, req.ID}, &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "user_edited=true") {
		t.Errorf("expected confirmation message, got: %s", buf.String())
	}

	got, err := store.GetRequirement(req.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.UserEdited {
		t.Error("expected user_edited=true after edit")
	}
	if got.Body != "Original statement." {
		t.Errorf("expected body unchanged by a no-op editor, got: %s", got.Body)
	}
}

func TestIntentEdit_UnknownID(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("EDITOR", "true")
	var buf bytes.Buffer
	if err := intentEditCommand([]string{"--project", dir, "req-0000"}, &buf); err == nil {
		t.Fatal("expected an error for an unknown requirement id")
	}
}

func TestIntentPreview_ProjectLevelOnlyNoWorkflow(t *testing.T) {
	dir := t.TempDir()
	store := intent.NewStore(dir)
	mustCreate(t, store, intent.StatusActive, intent.Scope{Level: intent.ScopeLevelProject}, "Always injected statement.")
	mustCreate(t, store, intent.StatusActive, intent.Scope{Level: intent.ScopeLevelDomain, Domains: []string{"versioning"}}, "Domain-only statement.")

	var buf bytes.Buffer
	if err := intentPreviewCommand([]string{"--project", dir}, &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "## Standing project requirements") {
		t.Errorf("expected the injected block header, got: %s", out)
	}
	if !strings.Contains(out, "Always injected statement.") {
		t.Errorf("expected the project-level requirement, got: %s", out)
	}
}

func TestIntentPreview_EmptySelection(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	if err := intentPreviewCommand([]string{"--project", dir}, &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "(no requirements selected)") {
		t.Errorf("expected empty-selection message, got: %s", buf.String())
	}
}

func TestIntentPreview_DomainScopeViaWorkflowRepos(t *testing.T) {
	dir := t.TempDir()
	writeProjectFixture(t, dir)

	store := intent.NewStore(dir)
	mustCreate(t, store, intent.StatusActive, intent.Scope{Level: intent.ScopeLevelProject}, "Project-wide statement.")
	mustCreate(t, store, intent.StatusActive, intent.Scope{Level: intent.ScopeLevelDomain, Domains: []string{"versioning"}}, "Versioning statement.")
	mustCreate(t, store, intent.StatusActive, intent.Scope{Level: intent.ScopeLevelDomain, Domains: []string{"unrelated"}}, "Unrelated statement.")

	var buf bytes.Buffer
	err := intentPreviewCommand([]string{"--project", dir, "--workflow", "develop", "--step", "implement"}, &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Project-wide statement.") {
		t.Errorf("expected project-level requirement, got: %s", out)
	}
	if !strings.Contains(out, "Versioning statement.") {
		t.Errorf("expected in-scope domain requirement, got: %s", out)
	}
	if strings.Contains(out, "Unrelated statement.") {
		t.Errorf("did not expect the out-of-scope domain requirement, got: %s", out)
	}
}

func TestIntentPreview_InjectionOffForStep(t *testing.T) {
	dir := t.TempDir()
	clocheDir := filepath.Join(dir, ".cloche")
	if err := os.MkdirAll(clocheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	wfSrc := `workflow develop {
  step implement {
    prompt = file("prompts/implement.md")
    intent_tracking = false
    results = [success, fail]
  }

  implement:success -> done
  implement:fail -> abort
}
`
	if err := os.WriteFile(filepath.Join(clocheDir, "develop.cloche"), []byte(wfSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	store := intent.NewStore(dir)
	mustCreate(t, store, intent.StatusActive, intent.Scope{Level: intent.ScopeLevelProject}, "Project-wide statement.")

	var buf bytes.Buffer
	err := intentPreviewCommand([]string{"--project", dir, "--workflow", "develop", "--step", "implement"}, &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "intent injection is off") {
		t.Errorf("expected the opt-out message, got: %s", buf.String())
	}
}

func TestPromptTemplateName(t *testing.T) {
	tests := []struct {
		name string
		step *domain.Step
		want string
	}{
		{
			name: "file config yields basename without extension",
			step: &domain.Step{Name: "implement", Config: map[string]string{"prompt": `file("prompts/implement.md")`}},
			want: "implement",
		},
		{
			name: "no prompt config falls back to step name",
			step: &domain.Step{Name: "review", Config: map[string]string{}},
			want: "review",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := promptTemplateName(tt.step); got != tt.want {
				t.Errorf("promptTemplateName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveRepoPaths(t *testing.T) {
	cfg := configWithRepos(t, map[string]string{"main": "internal/version", "docs": "docs"})
	got := resolveRepoPaths(cfg, []string{"main", "unknown"})
	if len(got) != 1 || got[0] != "internal/version" {
		t.Errorf("expected [internal/version], got %v", got)
	}
}

func TestIntentScan_DispatchesWorkflow(t *testing.T) {
	mock := &intentMockClient{runResp: &pb.RunWorkflowResponse{RunId: "run-1", TaskId: "task-1"}}

	var buf bytes.Buffer
	if err := intentScanWithClient(context.Background(), mock, nil, &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.lastReq.WorkflowName != "intent-scan" {
		t.Errorf("expected workflow name intent-scan, got %q", mock.lastReq.WorkflowName)
	}
	if mock.lastReq.Prompt != "" {
		t.Errorf("expected no prompt without --full, got %q", mock.lastReq.Prompt)
	}
	if len(mock.lastReq.Env) != 0 {
		t.Errorf("expected no env without --full, got %v", mock.lastReq.Env)
	}
	if !strings.Contains(buf.String(), "run-1") {
		t.Errorf("expected run id in output, got: %s", buf.String())
	}
}

func TestIntentScan_FullFlag(t *testing.T) {
	mock := &intentMockClient{runResp: &pb.RunWorkflowResponse{RunId: "run-2"}}

	var buf bytes.Buffer
	if err := intentScanWithClient(context.Background(), mock, []string{"--full"}, &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// --full must reach collect-sources/discover-domains via an explicit env
	// var on the run, never via the prompt text (nothing reads the host
	// workflow run's prompt).
	if mock.lastReq.Prompt != "" {
		t.Errorf("expected --full not to be forwarded via prompt, got %q", mock.lastReq.Prompt)
	}
	if got := mock.lastReq.Env["CLOCHE_INTENT_FULL"]; got != "1" {
		t.Errorf("expected CLOCHE_INTENT_FULL=1 in env, got %q (env=%v)", got, mock.lastReq.Env)
	}
	if !strings.Contains(buf.String(), "re-mining") {
		t.Errorf("expected a re-mine preview to be printed, got: %s", buf.String())
	}
}

// TestIntentScan_PrintsSummaryOnSuccess exercises the "prints the same
// summary when the run finishes" behavior: once the polled run reaches
// "succeeded", intentScanWithClient reads the project's scan-state.yaml
// (written by collect-sources during the run) and prints the aggregate
// stats line, including a warning for a configured repo that contributed
// nothing.
func TestIntentScan_PrintsSummaryOnSuccess(t *testing.T) {
	dir := t.TempDir()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(oldWD) })

	store := intent.NewStore(dir)
	if err := store.SaveScanState(&intent.ScanState{
		LastScanStats: intent.ScanStats{Repos: []intent.RepoStats{
			{Name: "", DocsNew: 3, DocsChanged: 1, Commits: 5, Runs: 2},
			{Name: "docs-repo", DocsNew: 0, DocsChanged: 0, Commits: 0, Runs: 0},
		}},
	}); err != nil {
		t.Fatal(err)
	}

	mock := &intentMockClient{
		runResp:    &pb.RunWorkflowResponse{RunId: "run-3"},
		statusResp: &pb.GetStatusResponse{RunId: "run-3", State: "succeeded"},
	}

	var buf bytes.Buffer
	if err := intentScanWithClient(context.Background(), mock, nil, &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	for _, want := range []string{"4 docs", "5 commits", "2 transcripts", "across 2 repos", "docs-repo contributed nothing"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected output to contain %q, got: %s", want, out)
		}
	}
}

func TestIntentScan_NoSummaryOnFailure(t *testing.T) {
	mock := &intentMockClient{
		runResp:    &pb.RunWorkflowResponse{RunId: "run-4"},
		statusResp: &pb.GetStatusResponse{RunId: "run-4", State: "failed", ErrorMessage: "boom"},
	}

	var buf bytes.Buffer
	if err := intentScanWithClient(context.Background(), mock, nil, &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "intent scan failed") {
		t.Fatalf("expected failure message, got: %s", buf.String())
	}
}

// intentMockClient implements the gRPC methods needed by intentScanWithClient.
type intentMockClient struct {
	pb.ClocheServiceClient

	runResp *pb.RunWorkflowResponse
	runErr  error
	lastReq *pb.RunWorkflowRequest

	// statusResp is returned by GetStatus; defaults to an immediate
	// "succeeded" state for the run ID being polled when unset, so tests
	// that don't care about polling behavior aren't forced to wire it up.
	statusResp *pb.GetStatusResponse
}

func (m *intentMockClient) RunWorkflow(_ context.Context, req *pb.RunWorkflowRequest, _ ...grpc.CallOption) (*pb.RunWorkflowResponse, error) {
	m.lastReq = req
	return m.runResp, m.runErr
}

func (m *intentMockClient) GetStatus(_ context.Context, req *pb.GetStatusRequest, _ ...grpc.CallOption) (*pb.GetStatusResponse, error) {
	if m.statusResp != nil {
		return m.statusResp, nil
	}
	return &pb.GetStatusResponse{RunId: req.Id, State: "succeeded"}, nil
}

// mustCreate is a test helper that creates and saves a requirement via the
// store, failing the test on error.
func mustCreate(t *testing.T, store *intent.Store, status intent.Status, scope intent.Scope, body string) *intent.Requirement {
	t.Helper()
	req := &intent.Requirement{
		Status:     status,
		Scope:      scope,
		Confidence: intent.ConfidenceHigh,
		Provenance: intent.Provenance{Kind: intent.ProvenanceDoc, Ref: "CLAUDE.md", ExtractedAt: time.Now().UTC()},
		Body:       body,
	}
	created, err := store.CreateRequirement(req)
	if err != nil {
		t.Fatalf("creating requirement: %v", err)
	}
	return created
}

// writeProjectFixture writes a minimal project under dir: a config.toml with
// a "main" repository pointing at internal/version, a domains.yaml scoping
// "versioning" to that path, and a develop.cloche workflow whose "implement"
// step declares repos/domains matching it.
func writeProjectFixture(t *testing.T, dir string) {
	t.Helper()
	clocheDir := filepath.Join(dir, ".cloche")
	if err := os.MkdirAll(clocheDir, 0o755); err != nil {
		t.Fatal(err)
	}

	configSrc := `[[repositories]]
name = "main"
path = "internal/version"
`
	if err := os.WriteFile(filepath.Join(clocheDir, "config.toml"), []byte(configSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	wfSrc := `workflow develop {
  repos = ["main"]

  step implement {
    prompt = file("prompts/implement.md")
    domains = ["versioning"]
    results = [success, fail]
  }

  implement:success -> done
  implement:fail -> abort
}
`
	if err := os.WriteFile(filepath.Join(clocheDir, "develop.cloche"), []byte(wfSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	store := intent.NewStore(dir)
	if err := store.SaveDomains(&intent.DomainMap{
		Version: 1,
		Domains: []intent.Domain{
			{Name: "versioning", Description: "Version management.", Paths: []string{"internal/version/**"}},
			{Name: "unrelated", Description: "Something else.", Paths: []string{"internal/unrelated/**"}},
		},
	}); err != nil {
		t.Fatal(err)
	}
}

// configWithRepos builds a minimal *config.Config with the given repo
// name->path pairs, via the same Load path the CLI uses (so this stays in
// sync with the real toml decoding).
func configWithRepos(t *testing.T, repos map[string]string) *config.Config {
	t.Helper()
	dir := t.TempDir()
	clocheDir := filepath.Join(dir, ".cloche")
	if err := os.MkdirAll(clocheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for name, path := range repos {
		b.WriteString("[[repositories]]\n")
		b.WriteString("name = \"" + name + "\"\n")
		b.WriteString("path = \"" + path + "\"\n")
	}
	if err := os.WriteFile(filepath.Join(clocheDir, "config.toml"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// TestIntentScan_EndToEnd_FixtureProject drives the full collect-sources ->
// (stubbed extract/reconcile agent output) -> apply-reconcile pipeline
// against a fixture project, covering create, merge, supersede, and drop in
// one reconcile batch, plus the hard rules a real reconcile agent must obey.
func TestIntentScan_EndToEnd_FixtureProject(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("# Project rules\n\nNever bump major without asking.\n"), 0644); err != nil {
		t.Fatal(err)
	}

	store := intent.NewStore(dir)
	dupTarget, err := store.CreateRequirement(&intent.Requirement{
		Status:     intent.StatusActive,
		Scope:      intent.Scope{Level: intent.ScopeLevelProject},
		Confidence: intent.ConfidenceHigh,
		Body:       "Already-tracked requirement.",
	})
	if err != nil {
		t.Fatal(err)
	}
	editedTarget, err := store.CreateRequirement(&intent.Requirement{
		Status:     intent.StatusActive,
		Scope:      intent.Scope{Level: intent.ScopeLevelDomain, Domains: []string{"versioning"}},
		Confidence: intent.ConfidenceHigh,
		UserEdited: true,
		Body:       "The human's carefully chosen wording.",
	})
	if err != nil {
		t.Fatal(err)
	}
	disabledTarget, err := store.CreateRequirement(&intent.Requirement{
		Status:     intent.StatusDisabled,
		Scope:      intent.Scope{Level: intent.ScopeLevelProject},
		Confidence: intent.ConfidenceMedium,
		Body:       "A requirement the user turned off.",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Step: collect-sources (real, deterministic).
	sourcesOut := filepath.Join(dir, "sources")
	collection, _, err := runIntentCollectSources(dir, sourcesOut, false)
	if err != nil {
		t.Fatalf("collect-sources: %v", err)
	}
	if !collection.HasNew() {
		t.Fatalf("expected CLAUDE.md to be picked up as new material")
	}
	if _, err := os.Stat(filepath.Join(sourcesOut, "docs", "CLAUDE.md")); err != nil {
		t.Fatalf("expected collected doc: %v", err)
	}

	// Step: extract (stubbed agent) — not exercised via Apply, but a real
	// candidates.json is written to prove the shape the reconcile step reads
	// matches what MarshalCandidates/ParseCandidates round-trip.
	candidatesPath := filepath.Join(dir, "candidates.json")
	if err := os.WriteFile(candidatesPath, []byte(`{"candidates": [
		{"statement": "Never bump major without asking.", "rationale": "batched by the maintainer", "scope": {"level": "project"}, "confidence": "high", "provenance": {"kind": "doc", "ref": "CLAUDE.md"}}
	]}`), 0644); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(candidatesPath); err != nil {
		t.Fatal(err)
	} else if _, err := scan.ParseCandidates(data); err != nil {
		t.Fatalf("candidates.json produced by the stubbed extract step must parse: %v", err)
	}

	// Step: reconcile (stubbed agent) — one action of each kind, covering
	// create, merge into an active requirement, supersede of a user_edited
	// requirement, and drop of a candidate duplicating a disabled one.
	reconcileJSON := map[string]any{
		"actions": []map[string]any{
			{
				"action":     "create",
				"statement":  "Never bump major without asking.",
				"confidence": "high",
				"scope":      map[string]any{"level": "project"},
				"provenance": map[string]any{"kind": "doc", "ref": "CLAUDE.md"},
			},
			{
				"action":      "merge",
				"existing_id": dupTarget.ID,
				"reason":      "already tracked",
			},
			{
				"action":      "supersede",
				"existing_id": editedTarget.ID,
				"statement":   "The machine's replacement wording.",
				"confidence":  "medium",
				"scope":       map[string]any{"level": "domain", "domains": []string{"versioning"}},
				"provenance":  map[string]any{"kind": "commit", "ref": "abc123"},
				"reason":      "newer commit reverses this",
			},
			{
				"action":      "drop",
				"existing_id": disabledTarget.ID,
				"reason":      "duplicates a disabled requirement",
			},
		},
	}
	data, err := json.Marshal(reconcileJSON)
	if err != nil {
		t.Fatal(err)
	}
	reconcilePath := filepath.Join(dir, "reconcile.json")
	if err := os.WriteFile(reconcilePath, data, 0644); err != nil {
		t.Fatal(err)
	}

	// Step: apply-reconcile (real).
	report, err := runIntentApplyReconcile(dir, reconcilePath)
	if err != nil {
		t.Fatalf("apply-reconcile: %v", err)
	}

	if len(report.Created) != 1 {
		t.Fatalf("expected 1 created requirement from the create action, got %d", len(report.Created))
	}
	if len(report.Superseded) != 1 {
		t.Fatalf("expected 1 superseded requirement (its replacement is reported via Superseded, not Created), got %d", len(report.Superseded))
	}
	if len(report.Merged) != 1 || report.Merged[0] != dupTarget.ID {
		t.Fatalf("expected merge into %s, got %v", dupTarget.ID, report.Merged)
	}
	if report.Dropped != 1 {
		t.Fatalf("expected 1 dropped candidate, got %d", report.Dropped)
	}

	reloadedEdited, err := store.GetRequirement(editedTarget.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloadedEdited.Status != intent.StatusSuperseded {
		t.Fatalf("expected %s to be superseded, got %s", editedTarget.ID, reloadedEdited.Status)
	}
	if reloadedEdited.Body != "The human's carefully chosen wording." {
		t.Fatalf("user_edited requirement's body must survive untouched, got %q", reloadedEdited.Body)
	}

	reloadedDup, err := store.GetRequirement(dupTarget.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloadedDup.Status != intent.StatusActive {
		t.Fatalf("merge target must remain active, got %s", reloadedDup.Status)
	}

	reloadedDisabled, err := store.GetRequirement(disabledTarget.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloadedDisabled.Status != intent.StatusDisabled {
		t.Fatalf("disabled requirement must never be re-enabled, got %s", reloadedDisabled.Status)
	}

	// Re-running collect-sources is now quiet (the "re-running a quiet scan
	// is a no-op" acceptance criterion).
	collection2, _, err := runIntentCollectSources(dir, sourcesOut, false)
	if err != nil {
		t.Fatalf("second collect-sources: %v", err)
	}
	if collection2.HasNew() {
		t.Fatalf("expected the second scan to be quiet")
	}
}
