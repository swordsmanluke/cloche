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

func TestExtractResultsUsesConfiguredIdentity(t *testing.T) {
	repoDir, baseSHA := setupTestRepo(t)
	fixtureDir := makeFixtureDir(t)
	overrideDockerCp(t, fixtureDir)
	overrideDockerExec(t, nil)

	runID := "identity-configured"
	wt := prepareForTest(t, repoDir, baseSHA, runID)

	result, err := ExtractResults(context.Background(), ExtractOptions{
		ContainerID:  "fake-container",
		WorktreeDir:  wt.Dir,
		Branch:       wt.Branch,
		BaseSHA:      baseSHA,
		RunID:        runID,
		WorkflowName: "develop",
		Result:       "succeeded",
		AuthorName:   "cloche-bot",
		AuthorEmail:  "cloche-bot@example.com",
	})
	if err != nil {
		t.Fatalf("ExtractResults: %v", err)
	}

	cmd := exec.Command("git", "show", "-s", "--format=%an <%ae>", result.CommitSHA)
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git show: %v", err)
	}
	got := strings.TrimSpace(string(out))
	if got != "cloche-bot <cloche-bot@example.com>" {
		t.Errorf("author = %q, want %q", got, "cloche-bot <cloche-bot@example.com>")
	}
}

func TestExtractResultsFallsBackToDefaultIdentity(t *testing.T) {
	repoDir, baseSHA := setupTestRepo(t)
	fixtureDir := makeFixtureDir(t)
	overrideDockerCp(t, fixtureDir)
	overrideDockerExec(t, nil)

	runID := "identity-default"
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

	cmd := exec.Command("git", "show", "-s", "--format=%an <%ae>", result.CommitSHA)
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git show: %v", err)
	}
	got := strings.TrimSpace(string(out))
	if got != "cloche <cloche@local>" {
		t.Errorf("author = %q, want %q", got, "cloche <cloche@local>")
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

// TestExtractResultsContainerSubPath verifies that opts.ContainerSubPath
// scopes both docker cp and the container commit-log read to the named
// subdirectory of /workspace, leaving the rest of the workspace untouched.
func TestExtractResultsContainerSubPath(t *testing.T) {
	repoDir, baseSHA := setupTestRepo(t)
	wt := prepareForTest(t, repoDir, baseSHA, "sub-1")

	// dockerCp source should target /workspace/repos/cloche/. — capture and
	// stage some files for the commit so the extraction has something to do.
	var cpSrc, cpDst string
	origCp := dockerCp
	dockerCp = func(_ context.Context, src, dst string) error {
		cpSrc, cpDst = src, dst
		if err := os.WriteFile(filepath.Join(dst, "subfile.txt"), []byte("x"), 0644); err != nil {
			t.Fatalf("seed worktree: %v", err)
		}
		return nil
	}
	t.Cleanup(func() { dockerCp = origCp })

	// dockerExec for git log should be invoked with -C /workspace/repos/cloche.
	var execArgs []string
	origExec := dockerExec
	dockerExec = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		execArgs = append([]string(nil), args...)
		return []byte(""), nil
	}
	t.Cleanup(func() { dockerExec = origExec })

	_, err := ExtractResults(context.Background(), ExtractOptions{
		ContainerID:      "cid",
		WorktreeDir:      wt.Dir,
		Branch:           wt.Branch,
		BaseSHA:          baseSHA,
		RunID:            "run-sub",
		WorkflowName:     "develop",
		Result:           "succeeded",
		ContainerSubPath: "repos/cloche",
	})
	if err != nil {
		t.Fatalf("ExtractResults: %v", err)
	}

	wantSrc := "cid:/workspace/repos/cloche/."
	if cpSrc != wantSrc {
		t.Errorf("dockerCp src = %q, want %q", cpSrc, wantSrc)
	}
	if cpDst != wt.Dir+"/" {
		t.Errorf("dockerCp dst = %q, want %q", cpDst, wt.Dir+"/")
	}

	// containerCommitsFromDocker calls dockerExec with: git -C <path> log ...
	if len(execArgs) < 3 || execArgs[0] != "git" || execArgs[1] != "-C" {
		t.Fatalf("dockerExec args shape unexpected: %v", execArgs)
	}
	if got := execArgs[2]; got != "/workspace/repos/cloche" {
		t.Errorf("dockerExec git -C path = %q, want %q", got, "/workspace/repos/cloche")
	}
}

// TestExtractResultsLegacyNoSubPath verifies that an empty ContainerSubPath
// preserves pre-multi-repo behavior: docker cp from /workspace/. and the
// container git log read from /workspace.
func TestExtractResultsLegacyNoSubPath(t *testing.T) {
	repoDir, baseSHA := setupTestRepo(t)
	wt := prepareForTest(t, repoDir, baseSHA, "legacy-1")

	var cpSrc string
	origCp := dockerCp
	dockerCp = func(_ context.Context, src, dst string) error {
		cpSrc = src
		if err := os.WriteFile(filepath.Join(dst, "f.txt"), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
		return nil
	}
	t.Cleanup(func() { dockerCp = origCp })

	var execPath string
	origExec := dockerExec
	dockerExec = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if len(args) >= 3 && args[0] == "git" && args[1] == "-C" {
			execPath = args[2]
		}
		return []byte(""), nil
	}
	t.Cleanup(func() { dockerExec = origExec })

	if _, err := ExtractResults(context.Background(), ExtractOptions{
		ContainerID:  "cid",
		WorktreeDir:  wt.Dir,
		Branch:       wt.Branch,
		BaseSHA:      baseSHA,
		RunID:        "run-legacy",
		WorkflowName: "develop",
		Result:       "succeeded",
	}); err != nil {
		t.Fatalf("ExtractResults: %v", err)
	}

	if cpSrc != "cid:/workspace/." {
		t.Errorf("dockerCp src = %q, want %q", cpSrc, "cid:/workspace/.")
	}
	if execPath != "/workspace" {
		t.Errorf("dockerExec git -C path = %q, want %q", execPath, "/workspace")
	}
}

func TestContainerCommitsFromDocker(t *testing.T) {
	// Empty output → empty string.
	overrideDockerExec(t, []byte(""))
	if got := containerCommitsFromDocker(context.Background(), "cid", "base", ""); got != "" {
		t.Errorf("expected empty for empty docker exec output, got %q", got)
	}

	// Two commits separated by %x00 (the delimiter git log --format=%B%x00 uses).
	raw := []byte("Fix login validation\nCheck email format before submit\x00Add unit tests\x00")
	overrideDockerExec(t, raw)
	got := containerCommitsFromDocker(context.Background(), "cid", "base", "")
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

func TestFirstAgentCommitSubject(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "empty",
			input: "",
			want:  "",
		},
		{
			name: "skips bookkeeping prefixes, returns last agent subject",
			input: `  * cloche run i3jb-develop: develop (succeeded)

  * Version 3.14.21

  * Add BDD test plan for Repository
    Introduces godog scenarios.

  * Address PR feedback on repository BDD test plan`,
			want: "Address PR feedback on repository BDD test plan",
		},
		{
			name: "single agent commit",
			input: `  * Wire RepositoryStore into project loader
    Reads .cloche/config.toml for [[repositories]] entries.`,
			want: "Wire RepositoryStore into project loader",
		},
		{
			name: "only bookkeeping — returns empty so caller falls back",
			input: `  * cloche run xyz-develop: develop (succeeded)

  * Version 4.2.0`,
			want: "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := firstAgentCommitSubject(tc.input)
			if got != tc.want {
				t.Errorf("firstAgentCommitSubject = %q, want %q", got, tc.want)
			}
		})
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

// setupTestRepoWithManyFiles is like setupTestRepo but populates the repo
// with N tracked files at the initial commit. Used by mass-delete tests.
func setupTestRepoWithManyFiles(t *testing.T, n int) (repoDir, baseSHA string) {
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
	for i := 0; i < n; i++ {
		path := filepath.Join(dir, "file"+itoa(i)+".txt")
		if err := os.WriteFile(path, []byte("content "+itoa(i)+"\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	run("git", "add", ".")
	run("git", "commit", "-m", "initial commit with many files")
	return dir, run("git", "rev-parse", "HEAD")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// TestExtractResults_RefusesMassDeletion verifies the sanity gate: when the
// container's /workspace is nearly empty but the worktree branch tracked many
// files, the extraction must refuse to commit (rather than silently wiping the
// branch). This guards against bug cloche-9d59 (incomplete project copy).
func TestExtractResults_RefusesMassDeletion(t *testing.T) {
	repoDir, baseSHA := setupTestRepoWithManyFiles(t, 80)

	// Container fixture has only one file — i.e. extraction would delete ~80
	// project files and add 1.
	emptyish := t.TempDir()
	if err := os.WriteFile(filepath.Join(emptyish, "lone.txt"), []byte("only thing\n"), 0644); err != nil {
		t.Fatal(err)
	}
	overrideDockerCp(t, emptyish)
	overrideDockerExec(t, nil)

	runID := "mass-delete-test"
	wt := prepareForTest(t, repoDir, baseSHA, runID)

	_, err := ExtractResults(context.Background(), ExtractOptions{
		ContainerID:  "fake",
		WorktreeDir:  wt.Dir,
		Branch:       wt.Branch,
		BaseSHA:      baseSHA,
		RunID:        runID,
		WorkflowName: "develop",
		Result:       "failed",
	})
	if err == nil {
		t.Fatal("expected ExtractResults to refuse the mass-delete commit")
	}
	if !strings.Contains(err.Error(), "would delete") {
		t.Errorf("error doesn't mention deletion guard: %v", err)
	}

	// Verify nothing got committed: branch should still be at baseSHA.
	cmd := exec.Command("git", "rev-parse", "refs/heads/"+wt.Branch)
	cmd.Dir = repoDir
	out, runErr := cmd.Output()
	if runErr != nil {
		t.Fatalf("git rev-parse %s: %v", wt.Branch, runErr)
	}
	got := strings.TrimSpace(string(out))
	if got != baseSHA {
		t.Errorf("branch %s moved to %s; expected to stay at baseSHA %s", wt.Branch, got, baseSHA)
	}
}

// TestExtractResults_AllowsBalancedRefactor verifies the sanity gate doesn't
// trip on legitimate large-scale refactors where many files are deleted but
// many are also added (e.g. a directory rename).
func TestExtractResults_AllowsBalancedRefactor(t *testing.T) {
	repoDir, baseSHA := setupTestRepoWithManyFiles(t, 80)

	// Container fixture has 80 different files — 80 deletions + 80 additions.
	// Ratio is 1:1, well below the deletions > additions*2 trigger.
	balanced := t.TempDir()
	for i := 0; i < 80; i++ {
		path := filepath.Join(balanced, "renamed"+itoa(i)+".txt")
		if err := os.WriteFile(path, []byte("renamed content "+itoa(i)+"\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	overrideDockerCp(t, balanced)
	overrideDockerExec(t, nil)

	runID := "balanced-refactor"
	wt := prepareForTest(t, repoDir, baseSHA, runID)

	result, err := ExtractResults(context.Background(), ExtractOptions{
		ContainerID:  "fake",
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
	if result.CommitSHA == baseSHA {
		t.Error("expected a new commit, got baseSHA")
	}
}

// TestExtractResults_AllowsOverride verifies that
// CLOCHE_EXTRACT_ALLOW_MASS_DELETE=1 bypasses the sanity gate, in case an
// operator legitimately needs to extract a mass deletion.
func TestExtractResults_AllowsOverride(t *testing.T) {
	t.Setenv("CLOCHE_EXTRACT_ALLOW_MASS_DELETE", "1")
	repoDir, baseSHA := setupTestRepoWithManyFiles(t, 80)

	emptyish := t.TempDir()
	if err := os.WriteFile(filepath.Join(emptyish, "lone.txt"), []byte("only thing\n"), 0644); err != nil {
		t.Fatal(err)
	}
	overrideDockerCp(t, emptyish)
	overrideDockerExec(t, nil)

	runID := "override-test"
	wt := prepareForTest(t, repoDir, baseSHA, runID)

	result, err := ExtractResults(context.Background(), ExtractOptions{
		ContainerID:  "fake",
		WorktreeDir:  wt.Dir,
		Branch:       wt.Branch,
		BaseSHA:      baseSHA,
		RunID:        runID,
		WorkflowName: "develop",
		Result:       "succeeded",
	})
	if err != nil {
		t.Fatalf("ExtractResults with override: %v", err)
	}
	if result.CommitSHA == baseSHA {
		t.Error("expected commit despite mass deletion (override on)")
	}
}

// TestExtractGitResetsToBaseSHABeforeCommit verifies that ExtractResults resets
// the worktree to BaseSHA before committing, even when the branch was advanced
// beyond BaseSHA by a prior extract (the multi-sub-workflow-same-attempt scenario).
// Without the git reset --hard BaseSHA step (the L1 fix), the new commit would
// land on top of the advanced HEAD rather than on the caller-specified base.
func TestExtractGitResetsToBaseSHABeforeCommit(t *testing.T) {
	repoDir, baseSHA := setupTestRepo(t)
	fixtureDir := makeFixtureDir(t)
	overrideDockerCp(t, fixtureDir)
	overrideDockerExec(t, nil)

	runID := "reset-anchoring"
	wt := prepareForTest(t, repoDir, baseSHA, runID)

	gitEnv := append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com",
	)
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = wt.Dir
		cmd.Env = gitEnv
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s: %v", args, out, err)
		}
	}

	// Advance the worktree branch beyond baseSHA by committing a dummy file,
	// simulating a prior sub-workflow extract that already ran on this branch.
	if err := os.WriteFile(filepath.Join(wt.Dir, "prior_extract.txt"), []byte("prior content\n"), 0644); err != nil {
		t.Fatal(err)
	}
	run("git", "add", "prior_extract.txt")
	run("git", "commit", "-m", "prior extract commit")

	// Call ExtractResults anchored at baseSHA. The L1 fix (git reset --hard
	// BaseSHA) must re-anchor the commit so its parent is baseSHA, not the
	// advanced HEAD. Without the fix the parent would be the prior-extract commit.
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

	cmd := exec.Command("git", "rev-parse", result.CommitSHA+"^")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git rev-parse parent: %v", err)
	}
	parent := strings.TrimSpace(string(out))
	if parent != baseSHA {
		t.Errorf("commit parent = %q, want baseSHA %q; reset did not re-anchor correctly", parent, baseSHA)
	}
}

// TestExtractMultipleWithChangedBaseSHA verifies that two sequential
// ExtractResults calls on the same worktree chain correctly when BaseSHA
// advances between calls. This is the multi-sub-workflow scenario: L1 commits
// at SHA_A (producing SHA_L1), the branch is then advanced further by another
// operation, and L2 must still commit directly on top of SHA_L1 (not the
// further-advanced HEAD) because BaseSHA=SHA_L1 is explicitly passed.
func TestExtractMultipleWithChangedBaseSHA(t *testing.T) {
	repoDir, baseSHA := setupTestRepo(t)
	overrideDockerExec(t, nil)

	runID := "multi-extract"
	wt := prepareForTest(t, repoDir, baseSHA, runID)

	gitEnv := append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com",
	)
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = wt.Dir
		cmd.Env = gitEnv
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s: %v", args, out, err)
		}
	}

	fixture1 := t.TempDir()
	if err := os.WriteFile(filepath.Join(fixture1, "l1.txt"), []byte("l1 content\n"), 0644); err != nil {
		t.Fatal(err)
	}
	fixture2 := t.TempDir()
	if err := os.WriteFile(filepath.Join(fixture2, "l2.txt"), []byte("l2 content\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Use a pointer so the same closure serves both extracts.
	currentFixture := fixture1
	origCp := dockerCp
	dockerCp = func(ctx context.Context, src, dst string) error {
		return exec.Command("cp", "-a", currentFixture+"/.", dst).Run()
	}
	t.Cleanup(func() { dockerCp = origCp })

	// L1 extract (analog): commit fixture1 on top of baseSHA.
	r1, err := ExtractResults(context.Background(), ExtractOptions{
		ContainerID:  "fake-container",
		WorktreeDir:  wt.Dir,
		Branch:       wt.Branch,
		BaseSHA:      baseSHA,
		RunID:        runID + "-l1",
		WorkflowName: "develop",
		Result:       "succeeded",
	})
	if err != nil {
		t.Fatalf("L1 ExtractResults: %v", err)
	}
	shaL1 := r1.CommitSHA

	// Advance the branch past shaL1, simulating another operation (e.g. a
	// different sub-workflow in the same attempt) landing on the branch between
	// the two extracts. Without the L1 fix, L2 would commit on top of this
	// intermediate commit rather than on top of shaL1.
	if err := os.WriteFile(filepath.Join(wt.Dir, "between.txt"), []byte("between l1 and l2\n"), 0644); err != nil {
		t.Fatal(err)
	}
	run("git", "add", "between.txt")
	run("git", "commit", "-m", "intermediate commit between L1 and L2")

	// L2 extract (analog): BaseSHA is now shaL1. The reset must bring the
	// worktree back to shaL1 so shaL2's direct parent is shaL1, not the
	// intermediate commit above.
	currentFixture = fixture2
	r2, err := ExtractResults(context.Background(), ExtractOptions{
		ContainerID:  "fake-container",
		WorktreeDir:  wt.Dir,
		Branch:       wt.Branch,
		BaseSHA:      shaL1,
		RunID:        runID + "-l2",
		WorkflowName: "develop",
		Result:       "succeeded",
	})
	if err != nil {
		t.Fatalf("L2 ExtractResults: %v", err)
	}
	shaL2 := r2.CommitSHA

	// L2's direct parent must be shaL1 — confirming merge-base(L1, L2) == shaL1
	// and that the chain is L1 → L2, not L1 → intermediate → L2.
	parentCmd := exec.Command("git", "rev-parse", shaL2+"^")
	parentCmd.Dir = repoDir
	pOut, err := parentCmd.Output()
	if err != nil {
		t.Fatalf("git rev-parse L2 parent: %v", err)
	}
	if got := strings.TrimSpace(string(pOut)); got != shaL1 {
		t.Errorf("L2 parent = %q, want SHA_L1 %q; L2 is not chained directly from L1", got, shaL1)
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
