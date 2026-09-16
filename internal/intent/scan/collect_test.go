package scan_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swordsmanluke/cloche/internal/config"
	"github.com/swordsmanluke/cloche/internal/intent"
	"github.com/swordsmanluke/cloche/internal/intent/scan"
)

func runGitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "git %v: %s", args, out)
}

func initGitProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGitCmd(t, dir, "init", "-q")
	return dir
}

func TestCollect_DocsNewAndUnchanged(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("hello"), 0644))

	c, err := scan.Collect(dir, nil, nil, nil, nil)
	require.NoError(t, err)
	require.Len(t, c.Docs, 1)
	assert.Equal(t, "CLAUDE.md", c.Docs[0].Path)
	assert.True(t, c.HasNew())

	// Persist cursors, then re-scan with no changes: doc should not
	// reappear.
	state := c.NextState(&intent.ScanState{})
	c2, err := scan.Collect(dir, nil, state, nil, nil)
	require.NoError(t, err)
	assert.Empty(t, c2.Docs)
	assert.False(t, c2.HasNew())

	// Edit the doc: it should reappear.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("hello, edited"), 0644))
	c3, err := scan.Collect(dir, nil, state, nil, nil)
	require.NoError(t, err)
	require.Len(t, c3.Docs, 1)
}

func TestCollect_DocGlobRecursive(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "docs", "plans"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "docs", "plans", "a.md"), []byte("a"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "docs", "b.md"), []byte("b"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "docs", "c.txt"), []byte("c"), 0644))

	c, err := scan.Collect(dir, nil, nil, []string{"docs/**/*.md"}, nil)
	require.NoError(t, err)
	var got []string
	for _, d := range c.Docs {
		got = append(got, d.Path)
	}
	assert.ElementsMatch(t, []string{"docs/plans/a.md", "docs/b.md"}, got)
}

func TestCollect_GitLogCursorAndNoiseFilter(t *testing.T) {
	dir := initGitProject(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("1"), 0644))
	runGitCmd(t, dir, "add", "a.txt")
	runGitCmd(t, dir, "commit", "-q", "-m", "Add feature X")

	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("2"), 0644))
	runGitCmd(t, dir, "add", "a.txt")
	runGitCmd(t, dir, "commit", "-q", "-m", "Version 1.2.3")

	c, err := scan.Collect(dir, nil, nil, nil, nil)
	require.NoError(t, err)
	require.Len(t, c.Commits, 1, "the Version bump commit should be filtered as noise")
	assert.Equal(t, "Add feature X", c.Commits[0].Subject)
	assert.NotEmpty(t, c.Commits[0].Patch)

	state := c.NextState(&intent.ScanState{})
	require.Contains(t, state.Repos, "")
	assert.NotEmpty(t, state.Repos[""].LastCommit, "cursor should advance to HEAD even though only 1 of 2 commits was kept")

	// Re-scanning from the persisted cursor with no new commits is quiet.
	c2, err := scan.Collect(dir, nil, state, nil, nil)
	require.NoError(t, err)
	assert.Empty(t, c2.Commits)
	assert.False(t, c2.HasNew())

	// A further noise-only commit still advances the cursor without
	// producing a Commits entry.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("3"), 0644))
	runGitCmd(t, dir, "add", "a.txt")
	runGitCmd(t, dir, "commit", "-q", "-m", "Version 1.2.4")
	c3, err := scan.Collect(dir, nil, state, nil, nil)
	require.NoError(t, err)
	assert.Empty(t, c3.Commits)
	state3 := c3.NextState(state)
	assert.NotEqual(t, state.Repos[""].LastCommit, state3.Repos[""].LastCommit)
}

func TestCollect_RunsTaskPromptCursor(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".cloche", "runs", "run-1"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".cloche", "runs", "run-1", "task_prompt.md"), []byte("do the thing"), 0644))

	c, err := scan.Collect(dir, nil, nil, nil, nil)
	require.NoError(t, err)
	require.Len(t, c.Runs, 1)
	assert.Equal(t, "run-1", c.Runs[0].ID)
	assert.Equal(t, "do the thing", c.Runs[0].TaskPrompt)

	state := c.NextState(&intent.ScanState{})
	require.Contains(t, state.Repos, "")
	assert.Contains(t, state.Repos[""].ScannedRuns, "run-1")

	c2, err := scan.Collect(dir, nil, state, nil, nil)
	require.NoError(t, err)
	assert.Empty(t, c2.Runs, "run-1 already scanned should not reappear")
}

func TestCollect_RunsTranscriptExcludesIntentTrackingOffSteps(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".cloche", "runs", "run-1"), 0755))
	logDir := filepath.Join(dir, ".cloche", "logs", "run-1")
	require.NoError(t, os.MkdirAll(logDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(logDir, "implement.log"), []byte("implement output"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(logDir, "sanitize.log"), []byte("sensitive script output"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(logDir, "llm-sanitize.log"), []byte("sensitive llm output"), 0644))

	c, err := scan.Collect(dir, nil, nil, nil, map[string]bool{"sanitize": true})
	require.NoError(t, err)
	require.Len(t, c.Runs, 1)
	assert.Contains(t, c.Runs[0].Transcript, "implement output")
	assert.NotContains(t, c.Runs[0].Transcript, "sensitive script output")
	assert.NotContains(t, c.Runs[0].Transcript, "sensitive llm output")
}

func TestCollect_RunsTranscriptExcludesSubworkflowDirForExcludedStep(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".cloche", "runs", "run-1"), 0755))
	logDir := filepath.Join(dir, ".cloche", "logs", "run-1")
	subDir := filepath.Join(logDir, "develop")
	require.NoError(t, os.MkdirAll(subDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(subDir, "implement.log"), []byte("nested output"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(logDir, "plan.log"), []byte("plan output"), 0644))

	c, err := scan.Collect(dir, nil, nil, nil, map[string]bool{"develop": true})
	require.NoError(t, err)
	require.Len(t, c.Runs, 1)
	assert.Contains(t, c.Runs[0].Transcript, "plan output")
	assert.NotContains(t, c.Runs[0].Transcript, "nested output")
}

func TestCollect_RunsEmptyDirStillMarkedScanned(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".cloche", "runs", "run-empty"), 0755))

	c, err := scan.Collect(dir, nil, nil, nil, nil)
	require.NoError(t, err)
	assert.Empty(t, c.Runs, "a run dir with no prompt/transcript contributes no RunSource")

	state := c.NextState(&intent.ScanState{})
	require.Contains(t, state.Repos, "")
	assert.Contains(t, state.Repos[""].ScannedRuns, "run-empty", "but is still marked scanned so it isn't rewalked forever")
}

func TestCollect_Quiet(t *testing.T) {
	dir := t.TempDir()
	c, err := scan.Collect(dir, nil, nil, nil, nil)
	require.NoError(t, err)
	assert.False(t, c.HasNew())
}

func TestCollection_Write(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("hello"), 0644))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".cloche", "runs", "run-1"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".cloche", "runs", "run-1", "task_prompt.md"), []byte("do the thing"), 0644))

	c, err := scan.Collect(dir, nil, nil, nil, nil)
	require.NoError(t, err)

	out := t.TempDir()
	require.NoError(t, c.Write(out))

	docBytes, err := os.ReadFile(filepath.Join(out, "docs", "CLAUDE.md"))
	require.NoError(t, err)
	assert.Equal(t, "hello", string(docBytes))

	promptBytes, err := os.ReadFile(filepath.Join(out, "runs", "run-1", "task_prompt.md"))
	require.NoError(t, err)
	assert.Equal(t, "do the thing", string(promptBytes))

	manifest, err := os.ReadFile(filepath.Join(out, "manifest.json"))
	require.NoError(t, err)
	assert.Contains(t, string(manifest), "CLAUDE.md")
	assert.Contains(t, string(manifest), "run-1")
}

// wrapperWithRepo builds a project laid out as a thin orchestration wrapper
// with one repository checked out under repos/<name>/: a wrapper-root
// CLAUDE.md and run, plus a git-initialized repo dir with its own README and
// commit. Mirrors the reproduction scenario from docs/intent.md's
// "Multi-repo projects" section.
func wrapperWithRepo(t *testing.T, repoName string) (wrapperDir string, cfg *config.Config) {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("wrapper doc"), 0644))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".cloche", "runs", "wrapper-run"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".cloche", "runs", "wrapper-run", "task_prompt.md"), []byte("wrapper task"), 0644))

	repoDir := filepath.Join(dir, "repos", repoName)
	require.NoError(t, os.MkdirAll(repoDir, 0755))
	runGitCmd(t, repoDir, "init", "-q")
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("repo doc"), 0644))
	runGitCmd(t, repoDir, "add", "README.md")
	runGitCmd(t, repoDir, "commit", "-q", "-m", "Add README")
	require.NoError(t, os.MkdirAll(filepath.Join(repoDir, ".cloche", "runs", "repo-run"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, ".cloche", "runs", "repo-run", "task_prompt.md"), []byte("repo task"), 0644))

	cfg = &config.Config{Repositories: []config.RepositoryConfig{
		{Name: repoName, Path: "./repos/" + repoName},
	}}
	return dir, cfg
}

func TestCollect_MultiRepo_RootPlusRepos(t *testing.T) {
	dir, cfg := wrapperWithRepo(t, "anarkana")

	c, err := scan.Collect(dir, cfg, nil, nil, nil)
	require.NoError(t, err)

	var docPaths []string
	for _, d := range c.Docs {
		docPaths = append(docPaths, d.Path)
	}
	assert.ElementsMatch(t, []string{"CLAUDE.md", "repos/anarkana/README.md"}, docPaths)

	require.Len(t, c.Commits, 1)
	assert.Equal(t, "repos/anarkana", c.Commits[0].Repo)
	assert.Equal(t, "repos/anarkana@"+c.Commits[0].SHA, c.Commits[0].Ref())

	var runRefs []string
	for _, r := range c.Runs {
		runRefs = append(runRefs, r.Ref())
	}
	assert.ElementsMatch(t, []string{"wrapper-run", "repos/anarkana/repo-run"}, runRefs)

	state := c.NextState(&intent.ScanState{})
	require.Contains(t, state.Repos, "")
	require.Contains(t, state.Repos, "anarkana")
	assert.Contains(t, state.Repos[""].ScannedRuns, "wrapper-run")
	assert.Contains(t, state.Repos["anarkana"].ScannedRuns, "repo-run")
	assert.NotEmpty(t, state.Repos["anarkana"].LastCommit)
	assert.Empty(t, state.Repos[""].LastCommit, "wrapper root has no commits of its own in this fixture")
}

// TestCollect_MultiRepo_IndependentCursors verifies that a new commit in one
// source doesn't cause the other source's already-mined material to
// reappear, and that each source's cursor only advances by what that source
// itself produced — the core promise of a per-repo ScanState.
func TestCollect_MultiRepo_IndependentCursors(t *testing.T) {
	dir, cfg := wrapperWithRepo(t, "anarkana")

	c, err := scan.Collect(dir, cfg, nil, nil, nil)
	require.NoError(t, err)
	state := c.NextState(&intent.ScanState{})

	// New commit in the repo only (touching a non-doc file so the doc
	// cursor is unaffected); nothing changes at the wrapper root.
	repoDir := filepath.Join(dir, "repos", "anarkana")
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "note.txt"), []byte("note"), 0644))
	runGitCmd(t, repoDir, "add", "note.txt")
	runGitCmd(t, repoDir, "commit", "-q", "-m", "Add note")

	c2, err := scan.Collect(dir, cfg, state, nil, nil)
	require.NoError(t, err)
	require.Len(t, c2.Commits, 1)
	assert.Equal(t, "repos/anarkana", c2.Commits[0].Repo)
	assert.Empty(t, c2.Docs, "README's hash is unchanged since the last scan, so it doesn't reappear")
	assert.Empty(t, c2.Runs)

	state2 := c2.NextState(state)
	assert.NotEqual(t, state.Repos["anarkana"].LastCommit, state2.Repos["anarkana"].LastCommit)
	assert.Equal(t, state.Repos[""].LastCommit, state2.Repos[""].LastCommit)
}

func TestCollect_MultiRepo_NoConfigMatchesLegacyBehavior(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("hello"), 0644))

	c, err := scan.Collect(dir, &config.Config{}, nil, nil, nil)
	require.NoError(t, err)
	require.Len(t, c.Docs, 1)
	assert.Equal(t, "CLAUDE.md", c.Docs[0].Path, "no [[repositories]] configured: refs stay unqualified, exactly as before repos existed")
	assert.Equal(t, "", c.Docs[0].Repo)
}

func TestCollection_Write_MultiRepo(t *testing.T) {
	dir, cfg := wrapperWithRepo(t, "anarkana")

	c, err := scan.Collect(dir, cfg, nil, nil, nil)
	require.NoError(t, err)

	out := t.TempDir()
	require.NoError(t, c.Write(out))

	rootDoc, err := os.ReadFile(filepath.Join(out, "docs", "CLAUDE.md"))
	require.NoError(t, err)
	assert.Equal(t, "wrapper doc", string(rootDoc))

	repoDoc, err := os.ReadFile(filepath.Join(out, "docs", "repos", "anarkana", "README.md"))
	require.NoError(t, err)
	assert.Equal(t, "repo doc", string(repoDoc))

	wrapperPrompt, err := os.ReadFile(filepath.Join(out, "runs", "wrapper-run", "task_prompt.md"))
	require.NoError(t, err)
	assert.Equal(t, "wrapper task", string(wrapperPrompt))

	repoPrompt, err := os.ReadFile(filepath.Join(out, "runs", "repos", "anarkana", "repo-run", "task_prompt.md"))
	require.NoError(t, err)
	assert.Equal(t, "repo task", string(repoPrompt))

	commitsTxt, err := os.ReadFile(filepath.Join(out, "commits.txt"))
	require.NoError(t, err)
	assert.Contains(t, string(commitsTxt), "repos/anarkana@")
}
