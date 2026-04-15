package docker

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// setupTestRepo creates a temporary git repository with an initial commit
// and returns the repo dir and the initial commit SHA.
func setupTestRepo(t *testing.T) (repoDir, baseSHA string) {
	t.Helper()
	dir := t.TempDir()

	gitEnv := append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com",
	)

	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		cmd.Env = gitEnv
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("cmd %v: %s: %v", args, out, err)
		}
		return strings.TrimSpace(string(out))
	}

	run("git", "init")
	run("git", "config", "user.email", "test@test.com")
	run("git", "config", "user.name", "Test User")

	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Test\n"), 0644); err != nil {
		t.Fatal(err)
	}
	run("git", "add", ".")
	run("git", "commit", "-m", "initial commit")

	sha := run("git", "rev-parse", "HEAD")
	return dir, sha
}

// makeFixtureDir creates a temp dir with some files simulating container workspace output.
func makeFixtureDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "result.txt"), []byte("extracted content\n"), 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// overrideDockerCp overrides the dockerCp hook for the test duration,
// using a local cp -a from fixtureDir instead of a real Docker daemon.
func overrideDockerCp(t *testing.T, fixtureDir string) {
	t.Helper()
	orig := dockerCp
	dockerCp = func(ctx context.Context, src, dst string) error {
		cmd := exec.Command("cp", "-a", fixtureDir+"/.", dst)
		return cmd.Run()
	}
	t.Cleanup(func() { dockerCp = orig })
}

// overrideDockerExec overrides the dockerExec hook for the test duration,
// returning the given bytes for any invocation.
func overrideDockerExec(t *testing.T, out []byte) {
	t.Helper()
	orig := dockerExec
	dockerExec = func(ctx context.Context, containerID string, cmd ...string) ([]byte, error) {
		return out, nil
	}
	t.Cleanup(func() { dockerExec = orig })
}

// branchExists reports whether the named branch exists in the given repo.
func branchExists(t *testing.T, repoDir, branch string) bool {
	t.Helper()
	cmd := exec.Command("git", "show-ref", "--verify", "refs/heads/"+branch)
	cmd.Dir = repoDir
	return cmd.Run() == nil
}

// prepareForTest is a helper that calls PrepareExtractWorktree with the
// default naming convention (.gitworktrees/cloche/<runID>, branch cloche/<runID>).
func prepareForTest(t *testing.T, projectDir, baseSHA, runID string) ExtractWorktree {
	t.Helper()
	wt, err := PrepareExtractWorktree(context.Background(), PrepareOptions{
		ProjectDir: projectDir,
		BaseSHA:    baseSHA,
		TargetDir:  filepath.Join(projectDir, ".gitworktrees", "cloche", runID),
		Branch:     "cloche/" + runID,
	})
	if err != nil {
		t.Fatalf("PrepareExtractWorktree: %v", err)
	}
	return wt
}

func TestPrepareExtractWorktree(t *testing.T) {
	repoDir, baseSHA := setupTestRepo(t)
	runID := "prepare-basic"
	wt := prepareForTest(t, repoDir, baseSHA, runID)

	if wt.Branch != "cloche/"+runID {
		t.Errorf("Branch = %q, want %q", wt.Branch, "cloche/"+runID)
	}
	if !branchExists(t, repoDir, wt.Branch) {
		t.Errorf("branch %q should exist after prepare", wt.Branch)
	}
	if _, err := os.Stat(wt.Dir); err != nil {
		t.Errorf("worktree dir should exist: %v", err)
	}
	// The worktree's .git must be a file (worktree pointer), not a directory.
	info, err := os.Lstat(filepath.Join(wt.Dir, ".git"))
	if err != nil {
		t.Fatalf("stat worktree .git: %v", err)
	}
	if info.IsDir() {
		t.Error("worktree .git should be a file (gitdir pointer), not a directory")
	}
}

func TestPrepareExtractWorktreeNonemptyTargetErrors(t *testing.T) {
	repoDir, baseSHA := setupTestRepo(t)
	target := t.TempDir()
	if err := os.WriteFile(filepath.Join(target, "existing.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := PrepareExtractWorktree(context.Background(), PrepareOptions{
		ProjectDir: repoDir,
		BaseSHA:    baseSHA,
		TargetDir:  target,
		Branch:     "cloche/x",
	})
	if err == nil || !strings.Contains(err.Error(), "not empty") {
		t.Fatalf("expected 'not empty' error, got %v", err)
	}
}

func TestPrepareExtractWorktreeBranchCollisionErrors(t *testing.T) {
	repoDir, baseSHA := setupTestRepo(t)

	// Pre-create the branch we'll collide with.
	cmd := exec.Command("git", "branch", "taken/branch")
	cmd.Dir = repoDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("creating branch: %v\n%s", err, out)
	}

	_, err := PrepareExtractWorktree(context.Background(), PrepareOptions{
		ProjectDir: repoDir,
		BaseSHA:    baseSHA,
		TargetDir:  filepath.Join(repoDir, ".gitworktrees", "cloche", "unused"),
		Branch:     "taken/branch",
	})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected 'already exists' error, got %v", err)
	}
}

func TestExtractResultsIntoPreparedWorktree(t *testing.T) {
	repoDir, baseSHA := setupTestRepo(t)
	fixtureDir := makeFixtureDir(t)
	overrideDockerCp(t, fixtureDir)
	overrideDockerExec(t, nil)

	runID := "testrun-default"
	wt := prepareForTest(t, repoDir, baseSHA, runID)

	result, err := ExtractResults(context.Background(), ExtractOptions{
		ContainerID:  "fake-container",
		WorktreeDir:  wt.Dir,
		Branch:       wt.Branch,
		BaseSHA:      baseSHA,
		RunID:        runID,
		WorkflowName: "develop",
		Result:       "succeeded",
	})
	if err != nil {
		t.Fatalf("ExtractResults: %v", err)
	}

	if result.Branch != wt.Branch {
		t.Errorf("Branch = %q, want %q", result.Branch, wt.Branch)
	}
	if result.TargetDir != wt.Dir {
		t.Errorf("TargetDir = %q, want %q", result.TargetDir, wt.Dir)
	}
	if result.CommitSHA == "" {
		t.Error("CommitSHA should be non-empty")
	}
	// Worktree persists by contract — caller owns teardown.
	if _, err := os.Stat(wt.Dir); err != nil {
		t.Errorf("worktree should still exist after extract: %v", err)
	}
	// Extracted content should be present.
	if _, err := os.Stat(filepath.Join(wt.Dir, "result.txt")); err != nil {
		t.Errorf("result.txt should exist in worktree: %v", err)
	}
	// The .git pointer file must still be intact.
	gitInfo, err := os.Lstat(filepath.Join(wt.Dir, ".git"))
	if err != nil {
		t.Fatalf("stat .git after extract: %v", err)
	}
	if gitInfo.IsDir() {
		t.Error(".git should remain a file after extract, not a directory")
	}
}

func TestExtractResultsRestoresGitPointerWhenContainerHasGit(t *testing.T) {
	repoDir, baseSHA := setupTestRepo(t)

	// Fixture dir simulates a container workspace that contains its own
	// .git directory (a real-world case when the agent ran git commands).
	fixtureDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(fixtureDir, "output.txt"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(fixtureDir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixtureDir, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	overrideDockerCp(t, fixtureDir)
	overrideDockerExec(t, nil)

	runID := "with-git"
	wt := prepareForTest(t, repoDir, baseSHA, runID)

	if _, err := ExtractResults(context.Background(), ExtractOptions{
		ContainerID:  "fake-container",
		WorktreeDir:  wt.Dir,
		Branch:       wt.Branch,
		BaseSHA:      baseSHA,
		RunID:        runID,
		WorkflowName: "develop",
		Result:       "succeeded",
	}); err != nil {
		t.Fatalf("ExtractResults: %v", err)
	}

	// After extract, the .git pointer file must still be a file (not the
	// container's .git dir that got copied over).
	gitInfo, err := os.Lstat(filepath.Join(wt.Dir, ".git"))
	if err != nil {
		t.Fatalf("stat .git: %v", err)
	}
	if gitInfo.IsDir() {
		t.Fatal(".git became a directory — pointer was not restored")
	}
	// The content should match what `git worktree add` wrote: a gitdir: line
	// pointing back into the main repo's worktrees bookkeeping.
	contents, err := os.ReadFile(filepath.Join(wt.Dir, ".git"))
	if err != nil {
		t.Fatalf("read .git: %v", err)
	}
	if !strings.HasPrefix(string(contents), "gitdir:") {
		t.Errorf(".git content should start with 'gitdir:', got %q", string(contents))
	}
	// git rev-parse HEAD inside the worktree should still succeed.
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = wt.Dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("worktree no longer usable: %s: %v", out, err)
	}
}

func TestExtractResultsMissingWorktreeDirErrors(t *testing.T) {
	_, err := ExtractResults(context.Background(), ExtractOptions{
		ContainerID:  "fake",
		BaseSHA:      "abc123",
		RunID:        "test-run-1",
		WorkflowName: "develop",
		Result:       "succeeded",
	})
	if err == nil || !strings.Contains(err.Error(), "WorktreeDir") {
		t.Fatalf("expected WorktreeDir error, got %v", err)
	}
}

func TestExtractResultsEmptyBaseSHA(t *testing.T) {
	_, err := ExtractResults(context.Background(), ExtractOptions{
		ContainerID:  "fake-container",
		WorktreeDir:  "/some/worktree",
		BaseSHA:      "",
		RunID:        "test-run-1",
		WorkflowName: "develop",
		Result:       "succeeded",
	})
	if err == nil {
		t.Fatal("expected error for empty BaseSHA, got nil")
	}
	if !strings.Contains(err.Error(), "test-run-1") {
		t.Errorf("error should mention run ID, got: %v", err)
	}
}

func TestExtractResultsNoGit(t *testing.T) {
	fixtureDir := makeFixtureDir(t)
	overrideDockerCp(t, fixtureDir)

	targetDir := t.TempDir()
	opts := ExtractOptions{
		ContainerID: "fake-container",
		RunID:       "testrun-nogit",
		NoGit:       true,
		TargetDir:   targetDir,
	}

	result, err := ExtractResults(context.Background(), opts)
	if err != nil {
		t.Fatalf("ExtractResults NoGit: %v", err)
	}

	if result.TargetDir != targetDir {
		t.Errorf("TargetDir = %q, want %q", result.TargetDir, targetDir)
	}
	if result.Branch != "" {
		t.Errorf("Branch = %q, want empty in NoGit mode", result.Branch)
	}
	if result.CommitSHA != "" {
		t.Errorf("CommitSHA = %q, want empty in NoGit mode", result.CommitSHA)
	}
	if _, err := os.Stat(filepath.Join(targetDir, "result.txt")); err != nil {
		t.Errorf("result.txt should exist in target dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(targetDir, ".git")); !os.IsNotExist(err) {
		t.Error("target dir should not contain .git in NoGit mode")
	}
}

func TestExtractResultsNoGitEmptyBaseSHA(t *testing.T) {
	fixtureDir := makeFixtureDir(t)
	overrideDockerCp(t, fixtureDir)

	targetDir := t.TempDir()
	result, err := ExtractResults(context.Background(), ExtractOptions{
		ContainerID: "fake-container",
		RunID:       "testrun-nogit-nosha",
		NoGit:       true,
		TargetDir:   targetDir,
	})
	if err != nil {
		t.Fatalf("NoGit with empty BaseSHA should succeed: %v", err)
	}
	if result.TargetDir != targetDir {
		t.Errorf("TargetDir = %q, want %q", result.TargetDir, targetDir)
	}
}

func TestContainerCommitsFromDocker(t *testing.T) {
	// Empty output → empty string.
	overrideDockerExec(t, []byte(""))
	if got := containerCommitsFromDocker(context.Background(), "cid", "base"); got != "" {
		t.Errorf("expected empty for empty docker exec output, got %q", got)
	}

	// Two commits separated by %x00 (the delimiter git log --format=%B%x00 uses).
	raw := []byte("Fix login validation\nCheck email format before submit\x00Add unit tests\x00")
	overrideDockerExec(t, raw)
	got := containerCommitsFromDocker(context.Background(), "cid", "base")
	if !strings.Contains(got, "Fix login validation") {
		t.Error("expected first commit subject in output")
	}
	if !strings.Contains(got, "Check email format before submit") {
		t.Error("expected first commit body in output")
	}
	if !strings.Contains(got, "Add unit tests") {
		t.Error("expected second commit in output")
	}
}

func TestClassifyChanges(t *testing.T) {
	input := `A	src/new_file.go
M	src/existing.go
D	src/old_file.go
R100	src/before.go	src/after.go
M	README.md`

	added, modified, deleted, renamed := classifyChanges(input)

	if len(added) != 1 || added[0] != "src/new_file.go" {
		t.Errorf("added = %v, want [src/new_file.go]", added)
	}
	if len(modified) != 2 || modified[0] != "src/existing.go" || modified[1] != "README.md" {
		t.Errorf("modified = %v, want [src/existing.go README.md]", modified)
	}
	if len(deleted) != 1 || deleted[0] != "src/old_file.go" {
		t.Errorf("deleted = %v, want [src/old_file.go]", deleted)
	}
	if len(renamed) != 1 || renamed[0] != "src/before.go -> src/after.go" {
		t.Errorf("renamed = %v, want [src/before.go -> src/after.go]", renamed)
	}
}

func TestClassifyChangesEmpty(t *testing.T) {
	added, modified, deleted, renamed := classifyChanges("")
	if len(added)+len(modified)+len(deleted)+len(renamed) != 0 {
		t.Error("expected empty results for empty input")
	}
}

func TestExtractStatSummary(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name: "typical stat output",
			input: ` src/foo.go | 10 +++++++---
 src/bar.go |  3 +++
 2 files changed, 10 insertions(+), 3 deletions(-)`,
			want: "2 files changed, 10 insertions(+), 3 deletions(-)",
		},
		{
			name:  "empty",
			input: "",
			want:  "",
		},
		{
			name:  "no summary line",
			input: "some random output",
			want:  "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractStatSummary(tt.input)
			if got != tt.want {
				t.Errorf("extractStatSummary() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestWriteChangeSection(t *testing.T) {
	var b strings.Builder
	writeChangeSection(&b, "Added", []string{"a.go", "b.go"})
	got := b.String()
	want := "Added:\n  a.go\n  b.go\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestWriteChangeSectionEmpty(t *testing.T) {
	var b strings.Builder
	writeChangeSection(&b, "Added", nil)
	if b.Len() != 0 {
		t.Error("expected empty output for nil files")
	}
}

func TestBuildCommitMessageIntegration(t *testing.T) {
	msg := buildCommitMessage(context.Background(), "/nonexistent", nil, "test-run-id", "develop", "succeeded", "")
	if !strings.HasPrefix(msg, "cloche run test-run-id: develop (succeeded)") {
		t.Errorf("unexpected message: %s", msg)
	}
}

func TestBuildCommitMessageWithContainerCommits(t *testing.T) {
	commits := "  * Fix login validation\n    Check email format before submit\n\n  * Add unit tests"
	msg := buildCommitMessage(context.Background(), "/nonexistent", nil, "run-1", "develop", "succeeded", commits)

	if !strings.Contains(msg, "Squashed commits:") {
		t.Error("expected squash header in message")
	}
	if !strings.Contains(msg, "Fix login validation") {
		t.Error("expected container commit message in output")
	}
	if !strings.Contains(msg, "Add unit tests") {
		t.Error("expected second commit message in output")
	}
}
