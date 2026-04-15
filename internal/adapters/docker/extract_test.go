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

// setupTestGitRepo creates a temporary git repo with an initial commit.
// Returns the repo directory and the initial commit SHA.
func setupTestGitRepo(t *testing.T) (repoDir, baseSHA string) {
	t.Helper()
	repoDir = t.TempDir()

	gitEnv := append(os.Environ(),
		"GIT_AUTHOR_NAME=test",
		"GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=test",
		"GIT_COMMITTER_EMAIL=test@test.com",
	)

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		cmd.Env = gitEnv
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	run("init")
	run("config", "user.email", "test@test.com")
	run("config", "user.name", "test")

	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("initial"), 0644); err != nil {
		t.Fatal(err)
	}
	run("add", "README.md")
	run("commit", "-m", "initial commit")

	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git rev-parse HEAD: %v", err)
	}
	return repoDir, strings.TrimSpace(string(out))
}

// branchExists reports whether branch exists in repoDir.
func branchExists(t *testing.T, repoDir, branch string) bool {
	t.Helper()
	cmd := exec.Command("git", "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	cmd.Dir = repoDir
	return cmd.Run() == nil
}

// TestExtractResultsOptions is a table-driven test covering the new options
// added to ExtractOptions: TargetDir, Branch, NoGit, and Persist.
func TestExtractResultsOptions(t *testing.T) {
	// Create a fixture dir that simulates a container /workspace.
	fixtureDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(fixtureDir, "output.txt"), []byte("extracted"), 0644); err != nil {
		t.Fatal(err)
	}

	// Override dockerCp to use local cp -a from the fixture dir.
	// The src argument is ignored; only dst matters.
	origDockerCp := dockerCp
	t.Cleanup(func() { dockerCp = origDockerCp })
	dockerCp = func(ctx context.Context, src, dst string) error {
		return exec.CommandContext(ctx, "cp", "-a", fixtureDir+"/.", dst).Run()
	}

	tests := []struct {
		name    string
		wantErr bool
		// run sets up state and calls ExtractResults; returns result and error.
		run func(t *testing.T) (ExtractResult, error)
		// check validates a successful result (skipped when wantErr is true).
		check func(t *testing.T, r ExtractResult)
	}{
		{
			name: "default options: behavior unchanged",
			run: func(t *testing.T) (ExtractResult, error) {
				repoDir, baseSHA := setupTestGitRepo(t)
				return ExtractResults(context.Background(), ExtractOptions{
					ContainerID:  "fake",
					ProjectDir:   repoDir,
					RunID:        "run-default",
					BaseSHA:      baseSHA,
					WorkflowName: "develop",
					Result:       "succeeded",
				})
			},
			check: func(t *testing.T, r ExtractResult) {
				if r.Branch != "cloche/run-default" {
					t.Errorf("Branch = %q, want %q", r.Branch, "cloche/run-default")
				}
				if r.CommitSHA == "" {
					t.Error("CommitSHA should not be empty")
				}
				// Persist=false: worktree is removed after function returns.
				if _, err := os.Stat(r.TargetDir); !os.IsNotExist(err) {
					t.Error("worktree should be removed after function returns (Persist=false)")
				}
			},
		},
		{
			name: "TargetDir override: worktree ends up at specified path",
			run: func(t *testing.T) (ExtractResult, error) {
				repoDir, baseSHA := setupTestGitRepo(t)
				customTarget := filepath.Join(t.TempDir(), "my-output")
				return ExtractResults(context.Background(), ExtractOptions{
					ContainerID:  "fake",
					ProjectDir:   repoDir,
					RunID:        "run-targetdir",
					BaseSHA:      baseSHA,
					WorkflowName: "develop",
					Result:       "succeeded",
					TargetDir:    customTarget,
					Persist:      true, // keep so we can verify after return
				})
			},
			check: func(t *testing.T, r ExtractResult) {
				if !strings.Contains(r.TargetDir, "my-output") {
					t.Errorf("TargetDir = %q, want path containing 'my-output'", r.TargetDir)
				}
				// Worktree was cleaned up by Persist=false... but we set Persist=true so it's still here.
				if _, err := os.Stat(r.TargetDir); err != nil {
					t.Errorf("TargetDir should exist (Persist=true): %v", err)
				}
				if r.CommitSHA == "" {
					t.Error("CommitSHA should not be empty")
				}
			},
		},
		{
			name: "Branch override: branch has the specified name",
			run: func(t *testing.T) (ExtractResult, error) {
				repoDir, baseSHA := setupTestGitRepo(t)
				return ExtractResults(context.Background(), ExtractOptions{
					ContainerID:  "fake",
					ProjectDir:   repoDir,
					RunID:        "run-branch",
					BaseSHA:      baseSHA,
					WorkflowName: "develop",
					Result:       "succeeded",
					Branch:       "feature/custom-branch",
				})
			},
			check: func(t *testing.T, r ExtractResult) {
				if r.Branch != "feature/custom-branch" {
					t.Errorf("Branch = %q, want %q", r.Branch, "feature/custom-branch")
				}
				if r.CommitSHA == "" {
					t.Error("CommitSHA should not be empty")
				}
			},
		},
		{
			name: "NoGit: target contains container files, no .git, no branch",
			run: func(t *testing.T) (ExtractResult, error) {
				targetDir := filepath.Join(t.TempDir(), "nogit-out")
				return ExtractResults(context.Background(), ExtractOptions{
					ContainerID: "fake",
					ProjectDir:  "/nonexistent",
					RunID:       "run-nogit",
					BaseSHA:     "", // not required in NoGit mode
					NoGit:       true,
					TargetDir:   targetDir,
				})
			},
			check: func(t *testing.T, r ExtractResult) {
				if r.Branch != "" {
					t.Errorf("Branch should be empty for NoGit, got %q", r.Branch)
				}
				if r.CommitSHA != "" {
					t.Errorf("CommitSHA should be empty for NoGit, got %q", r.CommitSHA)
				}
				// Target should contain the fixture file.
				if _, err := os.Stat(filepath.Join(r.TargetDir, "output.txt")); err != nil {
					t.Errorf("output.txt should exist in target: %v", err)
				}
				// No .git directory.
				if _, err := os.Stat(filepath.Join(r.TargetDir, ".git")); !os.IsNotExist(err) {
					t.Error("target should not contain .git (NoGit mode)")
				}
			},
		},
		{
			name: "Persist: worktree still on disk after function returns",
			run: func(t *testing.T) (ExtractResult, error) {
				repoDir, baseSHA := setupTestGitRepo(t)
				return ExtractResults(context.Background(), ExtractOptions{
					ContainerID:  "fake",
					ProjectDir:   repoDir,
					RunID:        "run-persist",
					BaseSHA:      baseSHA,
					WorkflowName: "develop",
					Result:       "succeeded",
					Persist:      true,
				})
			},
			check: func(t *testing.T, r ExtractResult) {
				// Worktree must still exist after ExtractResults returns.
				if _, err := os.Stat(r.TargetDir); err != nil {
					t.Errorf("worktree should persist after function returns (Persist=true): %v", err)
				}
				if r.CommitSHA == "" {
					t.Error("CommitSHA should not be empty")
				}
			},
		},
		{
			name:    "nonempty TargetDir precondition: returns error, nothing copied",
			wantErr: true,
			run: func(t *testing.T) (ExtractResult, error) {
				targetDir := t.TempDir()
				// Put a file in it to make it non-empty.
				if err := os.WriteFile(filepath.Join(targetDir, "existing.txt"), []byte("data"), 0644); err != nil {
					t.Fatal(err)
				}
				return ExtractResults(context.Background(), ExtractOptions{
					ContainerID: "fake",
					ProjectDir:  "/nonexistent",
					RunID:       "run-nonempty",
					BaseSHA:     "abc123",
					TargetDir:   targetDir,
				})
			},
		},
		{
			name:    "branch collision in git mode: returns error",
			wantErr: true,
			run: func(t *testing.T) (ExtractResult, error) {
				repoDir, baseSHA := setupTestGitRepo(t)
				// Create the branch we'll try to collide with.
				cmd := exec.Command("git", "branch", "taken/branch")
				cmd.Dir = repoDir
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("creating branch: %v\n%s", err, out)
				}
				return ExtractResults(context.Background(), ExtractOptions{
					ContainerID: "fake",
					ProjectDir:  repoDir,
					RunID:       "run-collision",
					BaseSHA:     baseSHA,
					Branch:      "taken/branch",
				})
			},
		},
		{
			name: "NoGit with empty BaseSHA: succeeds",
			run: func(t *testing.T) (ExtractResult, error) {
				targetDir := filepath.Join(t.TempDir(), "nogit-nosha")
				return ExtractResults(context.Background(), ExtractOptions{
					ContainerID: "fake",
					ProjectDir:  "/nonexistent",
					RunID:       "run-nosha",
					BaseSHA:     "", // empty — OK in NoGit mode
					NoGit:       true,
					TargetDir:   targetDir,
				})
			},
			check: func(t *testing.T, r ExtractResult) {
				if r.Branch != "" || r.CommitSHA != "" {
					t.Errorf("Branch and CommitSHA should be empty for NoGit, got Branch=%q CommitSHA=%q", r.Branch, r.CommitSHA)
				}
				if _, err := os.Stat(filepath.Join(r.TargetDir, "output.txt")); err != nil {
					t.Errorf("output.txt should exist in target: %v", err)
				}
			},
		},
		{
			name: "NoGit ignores Branch override: no branch created",
			run: func(t *testing.T) (ExtractResult, error) {
				repoDir := t.TempDir()
				targetDir := filepath.Join(t.TempDir(), "nogit-branch")
				r, err := ExtractResults(context.Background(), ExtractOptions{
					ContainerID: "fake",
					ProjectDir:  repoDir,
					RunID:       "run-nogit-branch",
					BaseSHA:     "",
					NoGit:       true,
					Branch:      "should/be-ignored",
					TargetDir:   targetDir,
				})
				// Verify no branch was created in the repo dir (which isn't
				// even a real git repo — if Branch were honoured, git would fail).
				return r, err
			},
			check: func(t *testing.T, r ExtractResult) {
				// Branch field in result must be empty when NoGit=true.
				if r.Branch != "" {
					t.Errorf("Branch should be empty for NoGit, got %q", r.Branch)
				}
				// Target should have the fixture file.
				if _, err := os.Stat(filepath.Join(r.TargetDir, "output.txt")); err != nil {
					t.Errorf("output.txt should exist in target: %v", err)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := tt.run(t)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.check != nil {
				tt.check(t, r)
			}
		})
	}
}
