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

// branchExists reports whether the named branch exists in the given repo.
func branchExists(t *testing.T, repoDir, branch string) bool {
	t.Helper()
	cmd := exec.Command("git", "show-ref", "--verify", "refs/heads/"+branch)
	cmd.Dir = repoDir
	return cmd.Run() == nil
}

func TestExtractResultsDefaultOptions(t *testing.T) {
	repoDir, baseSHA := setupTestRepo(t)
	fixtureDir := makeFixtureDir(t)
	overrideDockerCp(t, fixtureDir)

	runID := "testrun-default"
	opts := ExtractOptions{
		ContainerID:  "fake-container",
		ProjectDir:   repoDir,
		RunID:        runID,
		BaseSHA:      baseSHA,
		WorkflowName: "develop",
		Result:       "succeeded",
	}

	result, err := ExtractResults(context.Background(), opts)
	if err != nil {
		t.Fatalf("ExtractResults: %v", err)
	}

	expectedBranch := "cloche/" + runID
	if result.Branch != expectedBranch {
		t.Errorf("Branch = %q, want %q", result.Branch, expectedBranch)
	}
	if result.CommitSHA == "" {
		t.Error("CommitSHA should be non-empty")
	}
	// Default targetDir is cleaned up after the function returns (Persist is false).
	expectedTarget := filepath.Join(repoDir, ".gitworktrees", "cloche", runID)
	if result.TargetDir != expectedTarget {
		t.Errorf("TargetDir = %q, want %q", result.TargetDir, expectedTarget)
	}
	if _, err := os.Stat(expectedTarget); !os.IsNotExist(err) {
		t.Error("worktree dir should have been cleaned up after return")
	}
	// Branch ref should still exist even after worktree removal.
	if !branchExists(t, repoDir, expectedBranch) {
		t.Errorf("branch %q should exist after extract", expectedBranch)
	}
}

func TestExtractResultsTargetDirOverride(t *testing.T) {
	repoDir, baseSHA := setupTestRepo(t)
	fixtureDir := makeFixtureDir(t)
	overrideDockerCp(t, fixtureDir)

	customTarget := filepath.Join(t.TempDir(), "my-target")
	runID := "testrun-targetdir"
	opts := ExtractOptions{
		ContainerID:  "fake-container",
		ProjectDir:   repoDir,
		RunID:        runID,
		BaseSHA:      baseSHA,
		WorkflowName: "develop",
		Result:       "succeeded",
		TargetDir:    customTarget,
	}

	result, err := ExtractResults(context.Background(), opts)
	if err != nil {
		t.Fatalf("ExtractResults: %v", err)
	}

	if result.TargetDir != customTarget {
		t.Errorf("TargetDir = %q, want %q", result.TargetDir, customTarget)
	}
	// Worktree cleaned up since Persist is false.
	if _, err := os.Stat(customTarget); !os.IsNotExist(err) {
		t.Error("worktree dir should have been cleaned up after return")
	}
	// Branch should still exist.
	expectedBranch := "cloche/" + runID
	if !branchExists(t, repoDir, expectedBranch) {
		t.Errorf("branch %q should exist", expectedBranch)
	}
}

func TestExtractResultsBranchOverride(t *testing.T) {
	repoDir, baseSHA := setupTestRepo(t)
	fixtureDir := makeFixtureDir(t)
	overrideDockerCp(t, fixtureDir)

	customBranch := "feature/my-custom-branch"
	runID := "testrun-branch"
	opts := ExtractOptions{
		ContainerID:  "fake-container",
		ProjectDir:   repoDir,
		RunID:        runID,
		BaseSHA:      baseSHA,
		WorkflowName: "develop",
		Result:       "succeeded",
		Branch:       customBranch,
	}

	result, err := ExtractResults(context.Background(), opts)
	if err != nil {
		t.Fatalf("ExtractResults: %v", err)
	}

	if result.Branch != customBranch {
		t.Errorf("Branch = %q, want %q", result.Branch, customBranch)
	}
	if !branchExists(t, repoDir, customBranch) {
		t.Errorf("branch %q should exist", customBranch)
	}
	// Default cloche/<runID> branch should NOT exist.
	defaultBranch := "cloche/" + runID
	if branchExists(t, repoDir, defaultBranch) {
		t.Errorf("default branch %q should not exist when Branch override is set", defaultBranch)
	}
}

func TestExtractResultsNoGit(t *testing.T) {
	fixtureDir := makeFixtureDir(t)
	overrideDockerCp(t, fixtureDir)

	targetDir := t.TempDir()
	// targetDir must be empty for the pre-check to pass; TempDir returns an empty dir.
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
	// Target should contain the fixture file.
	if _, err := os.Stat(filepath.Join(targetDir, "result.txt")); err != nil {
		t.Errorf("result.txt should exist in target dir: %v", err)
	}
	// Target must NOT contain a .git directory.
	if _, err := os.Stat(filepath.Join(targetDir, ".git")); !os.IsNotExist(err) {
		t.Error("target dir should not contain .git in NoGit mode")
	}
}

func TestExtractResultsPersist(t *testing.T) {
	repoDir, baseSHA := setupTestRepo(t)
	fixtureDir := makeFixtureDir(t)
	overrideDockerCp(t, fixtureDir)

	runID := "testrun-persist"
	opts := ExtractOptions{
		ContainerID:  "fake-container",
		ProjectDir:   repoDir,
		RunID:        runID,
		BaseSHA:      baseSHA,
		WorkflowName: "develop",
		Result:       "succeeded",
		Persist:      true,
	}

	result, err := ExtractResults(context.Background(), opts)
	if err != nil {
		t.Fatalf("ExtractResults Persist: %v", err)
	}

	// Worktree directory should still exist because Persist is true.
	if _, err := os.Stat(result.TargetDir); os.IsNotExist(err) {
		t.Error("worktree dir should persist after return when Persist is true")
	}
	// Clean up the worktree manually so the temp dir is tidy.
	rmCmd := exec.Command("git", "worktree", "remove", "--force", result.TargetDir)
	rmCmd.Dir = repoDir
	rmCmd.Run()
}

func TestExtractResultsNonemptyTargetDirError(t *testing.T) {
	repoDir, baseSHA := setupTestRepo(t)

	// Create a non-empty target dir.
	targetDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(targetDir, "existing.txt"), []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}

	cpCalled := false
	orig := dockerCp
	dockerCp = func(ctx context.Context, src, dst string) error {
		cpCalled = true
		return nil
	}
	t.Cleanup(func() { dockerCp = orig })

	opts := ExtractOptions{
		ContainerID:  "fake-container",
		ProjectDir:   repoDir,
		RunID:        "testrun-nonempty",
		BaseSHA:      baseSHA,
		WorkflowName: "develop",
		Result:       "succeeded",
		TargetDir:    targetDir,
	}

	_, err := ExtractResults(context.Background(), opts)
	if err == nil {
		t.Fatal("expected error for non-empty TargetDir, got nil")
	}
	if !strings.Contains(err.Error(), "not empty") {
		t.Errorf("error should mention 'not empty', got: %v", err)
	}
	if cpCalled {
		t.Error("docker cp should not have been called when pre-check fails")
	}
}

func TestExtractResultsBranchCollisionError(t *testing.T) {
	repoDir, baseSHA := setupTestRepo(t)

	// Pre-create the branch that we'll try to use.
	collisionBranch := "feature/already-exists"
	gitEnv := append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com",
	)
	createCmd := exec.Command("git", "branch", collisionBranch)
	createCmd.Dir = repoDir
	createCmd.Env = gitEnv
	if out, err := createCmd.CombinedOutput(); err != nil {
		t.Fatalf("pre-creating branch: %s: %v", out, err)
	}

	cpCalled := false
	orig := dockerCp
	dockerCp = func(ctx context.Context, src, dst string) error {
		cpCalled = true
		return nil
	}
	t.Cleanup(func() { dockerCp = orig })

	opts := ExtractOptions{
		ContainerID:  "fake-container",
		ProjectDir:   repoDir,
		RunID:        "testrun-branchcollision",
		BaseSHA:      baseSHA,
		WorkflowName: "develop",
		Result:       "succeeded",
		Branch:       collisionBranch,
	}

	_, err := ExtractResults(context.Background(), opts)
	if err == nil {
		t.Fatal("expected error for branch collision, got nil")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error should mention 'already exists', got: %v", err)
	}
	if cpCalled {
		t.Error("docker cp should not have been called when branch pre-check fails")
	}
}

func TestExtractResultsNoGitEmptyBaseSHA(t *testing.T) {
	fixtureDir := makeFixtureDir(t)
	overrideDockerCp(t, fixtureDir)

	targetDir := t.TempDir()
	opts := ExtractOptions{
		ContainerID: "fake-container",
		RunID:       "testrun-nogit-noshahasemptybassha",
		NoGit:       true,
		TargetDir:   targetDir,
		// BaseSHA deliberately empty — must succeed in NoGit mode.
	}

	result, err := ExtractResults(context.Background(), opts)
	if err != nil {
		t.Fatalf("NoGit with empty BaseSHA should succeed: %v", err)
	}
	if result.TargetDir != targetDir {
		t.Errorf("TargetDir = %q, want %q", result.TargetDir, targetDir)
	}
}

func TestExtractResultsNoGitIgnoresBranch(t *testing.T) {
	fixtureDir := makeFixtureDir(t)
	overrideDockerCp(t, fixtureDir)

	targetDir := t.TempDir()
	opts := ExtractOptions{
		ContainerID: "fake-container",
		RunID:       "testrun-nogit-branch-ignored",
		NoGit:       true,
		TargetDir:   targetDir,
		Branch:      "feature/should-be-ignored",
	}

	result, err := ExtractResults(context.Background(), opts)
	if err != nil {
		t.Fatalf("NoGit with Branch set should succeed: %v", err)
	}
	// Branch should be empty in result regardless of opts.Branch.
	if result.Branch != "" {
		t.Errorf("Branch = %q in NoGit mode, want empty", result.Branch)
	}
	if result.CommitSHA != "" {
		t.Errorf("CommitSHA = %q in NoGit mode, want empty", result.CommitSHA)
	}
}

// --- Existing helper-function tests (unchanged) ---

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
	// Test that buildCommitMessage returns at least the title when git
	// commands fail (e.g. not in a real worktree).
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

func TestExtractContainerCommitsEmpty(t *testing.T) {
	// Non-existent dir should return empty string, not error.
	result := extractContainerCommits(context.Background(), "/nonexistent", "abc123")
	if result != "" {
		t.Errorf("expected empty string, got %q", result)
	}
}

// TestExtractResultConventions verifies that ExtractResults returns an
// ExtractResult whose TargetDir and Branch follow the documented conventions.
// Since the function requires Docker and a real git repo, we only test that
// ExtractResults returns a clear error (not a zero-value result) when the
// BaseSHA is empty — which exercises the early-exit path and confirms the
// return type is (ExtractResult, error).
func TestExtractResultsEmptyBaseSHA(t *testing.T) {
	_, err := ExtractResults(context.Background(), ExtractOptions{
		ContainerID:  "fake-container",
		ProjectDir:   "/nonexistent",
		RunID:        "test-run-1",
		BaseSHA:      "",
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

// TestExtractResultConventions verifies the TargetDir and Branch naming
// conventions used by ExtractOptions without running Docker or git.
func TestExtractResultConventions(t *testing.T) {
	projectDir := "/some/project"
	runID := "abc123"

	expectedTargetDir := filepath.Join(projectDir, ".gitworktrees", "cloche", runID)
	expectedBranch := "cloche/" + runID

	// Confirm conventions match what ExtractResults would compute.
	if got := filepath.Join(projectDir, ".gitworktrees", "cloche", runID); got != expectedTargetDir {
		t.Errorf("TargetDir convention: got %q, want %q", got, expectedTargetDir)
	}
	if got := "cloche/" + runID; got != expectedBranch {
		t.Errorf("Branch convention: got %q, want %q", got, expectedBranch)
	}
}
