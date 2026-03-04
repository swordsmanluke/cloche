package orchestrator

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloche-dev/cloche/internal/ports"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockMergeQueue implements ports.MergeQueueStore for testing.
type mockMergeQueue struct {
	entries   []*ports.MergeQueueEntry
	completed []string
	failed    []string
}

func (m *mockMergeQueue) EnqueueMerge(_ context.Context, entry *ports.MergeQueueEntry) error {
	m.entries = append(m.entries, entry)
	return nil
}

func (m *mockMergeQueue) NextPendingMerge(_ context.Context, project string) (*ports.MergeQueueEntry, error) {
	for i, e := range m.entries {
		if e.Project == project && e.Status == "pending" {
			e.Status = "in_progress"
			m.entries[i] = e
			return e, nil
		}
	}
	return nil, nil
}

func (m *mockMergeQueue) CompleteMerge(_ context.Context, runID string) error {
	m.completed = append(m.completed, runID)
	for _, e := range m.entries {
		if e.RunID == runID {
			e.Status = "completed"
		}
	}
	return nil
}

func (m *mockMergeQueue) FailMerge(_ context.Context, runID string) error {
	m.failed = append(m.failed, runID)
	for _, e := range m.entries {
		if e.RunID == runID {
			e.Status = "failed"
		}
	}
	return nil
}

// mockLLM implements LLMClient for testing.
type mockLLM struct {
	response string
	err      error
	calls    int
}

func (m *mockLLM) Complete(_ context.Context, systemPrompt, userPrompt string) (string, error) {
	m.calls++
	return m.response, m.err
}

// initTestRepo creates a temporary git repo with an initial commit on main.
func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	env := []string{
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test",
	}

	cmds := [][]string{
		{"git", "init", "-b", "main"},
		{"git", "commit", "--allow-empty", "-m", "initial commit"},
	}
	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), env...)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git command %v failed: %s", args, out)
	}
	return dir
}

// createFeatureBranch creates a feature branch with a file change.
func createFeatureBranch(t *testing.T, repoDir, branchName, fileName, content string) {
	t.Helper()
	env := []string{
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test",
	}

	cmds := [][]string{
		{"git", "checkout", "-b", branchName},
	}
	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = repoDir
		cmd.Env = append(os.Environ(), env...)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git command %v failed: %s", args, out)
	}

	require.NoError(t, os.WriteFile(filepath.Join(repoDir, fileName), []byte(content), 0644))

	addCmd := exec.Command("git", "add", "-A")
	addCmd.Dir = repoDir
	addCmd.Env = append(os.Environ(), env...)
	out, err := addCmd.CombinedOutput()
	require.NoError(t, err, "git add failed: %s", out)

	commitCmd := exec.Command("git", "commit", "-m", "feature: "+fileName)
	commitCmd.Dir = repoDir
	commitCmd.Env = append(os.Environ(), env...)
	out, err = commitCmd.CombinedOutput()
	require.NoError(t, err, "git commit failed: %s", out)

	// Switch back to main
	checkoutCmd := exec.Command("git", "checkout", "main")
	checkoutCmd.Dir = repoDir
	checkoutCmd.Env = append(os.Environ(), env...)
	out, err = checkoutCmd.CombinedOutput()
	require.NoError(t, err, "git checkout main failed: %s", out)
}

func TestMergeAgent_SuccessfulMerge(t *testing.T) {
	repoDir := initTestRepo(t)
	createFeatureBranch(t, repoDir, "cloche/test-run-1", "feature.go", "package feature\n")

	mq := &mockMergeQueue{}
	agent := &MergeAgent{MergeQueue: mq}

	entry := &ports.MergeQueueEntry{
		RunID:   "test-run-1",
		Branch:  "cloche/test-run-1",
		Project: repoDir,
		Status:  "in_progress",
	}

	err := agent.Merge(context.Background(), entry, repoDir)
	require.NoError(t, err)

	// Verify the file is on main
	cmd := exec.Command("git", "show", "main:feature.go")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	require.NoError(t, err)
	assert.Equal(t, "package feature\n", string(out))

	// Verify the feature branch was deleted
	branchCmd := exec.Command("git", "branch", "--list", "cloche/test-run-1")
	branchCmd.Dir = repoDir
	branchOut, _ := branchCmd.Output()
	assert.Empty(t, strings.TrimSpace(string(branchOut)))

	// Verify the worktree was cleaned up
	worktreeDir := filepath.Join(repoDir, ".gitworktrees", "merge", "test-run-1")
	_, err = os.Stat(worktreeDir)
	assert.True(t, os.IsNotExist(err))
}

func TestMergeAgent_MergeFailure_BranchPreserved(t *testing.T) {
	repoDir := initTestRepo(t)

	// Create a conflicting setup: both main and feature modify the same file
	env := []string{
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test",
	}

	// Add file on main
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "conflict.go"), []byte("package main\n// main version\n"), 0644))
	runGit(t, repoDir, env, "git", "add", "-A")
	runGit(t, repoDir, env, "git", "commit", "-m", "main: add conflict.go")

	// Create feature branch with conflicting change
	runGit(t, repoDir, env, "git", "checkout", "-b", "cloche/conflict-run")
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "conflict.go"), []byte("package main\n// feature version\n"), 0644))
	runGit(t, repoDir, env, "git", "add", "-A")
	runGit(t, repoDir, env, "git", "commit", "-m", "feature: modify conflict.go")
	runGit(t, repoDir, env, "git", "checkout", "main")

	// Further modify on main to cause actual conflict
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "conflict.go"), []byte("package main\n// updated main version\n"), 0644))
	runGit(t, repoDir, env, "git", "add", "-A")
	runGit(t, repoDir, env, "git", "commit", "-m", "main: update conflict.go")

	// No LLM configured, so conflict resolution will fail
	mq := &mockMergeQueue{}
	agent := &MergeAgent{MergeQueue: mq}

	entry := &ports.MergeQueueEntry{
		RunID:   "conflict-run",
		Branch:  "cloche/conflict-run",
		Project: repoDir,
		Status:  "in_progress",
	}

	err := agent.Merge(context.Background(), entry, repoDir)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "conflict resolution failed")

	// Verify the branch is preserved
	branchCmd := exec.Command("git", "branch", "--list", "cloche/conflict-run")
	branchCmd.Dir = repoDir
	branchOut, _ := branchCmd.Output()
	assert.NotEmpty(t, strings.TrimSpace(string(branchOut)))
}

func TestMergeAgent_ConflictResolution_WithLLM(t *testing.T) {
	repoDir := initTestRepo(t)
	env := []string{
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test",
	}

	// Add file on main
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "app.go"), []byte("package main\n\nfunc main() {\n\t// main logic\n}\n"), 0644))
	runGit(t, repoDir, env, "git", "add", "-A")
	runGit(t, repoDir, env, "git", "commit", "-m", "main: add app.go")

	// Create feature branch with conflicting change
	runGit(t, repoDir, env, "git", "checkout", "-b", "cloche/llm-resolve-run")
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "app.go"), []byte("package main\n\nfunc main() {\n\t// feature logic\n}\n"), 0644))
	runGit(t, repoDir, env, "git", "add", "-A")
	runGit(t, repoDir, env, "git", "commit", "-m", "feature: modify app.go")
	runGit(t, repoDir, env, "git", "checkout", "main")

	// Further modify on main to cause conflict
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "app.go"), []byte("package main\n\nfunc main() {\n\t// updated main logic\n}\n"), 0644))
	runGit(t, repoDir, env, "git", "add", "-A")
	runGit(t, repoDir, env, "git", "commit", "-m", "main: update app.go")

	// LLM returns resolved content
	llm := &mockLLM{
		response: "=== FILE: app.go ===\npackage main\n\nfunc main() {\n\t// merged logic from both sides\n}\n",
	}
	mq := &mockMergeQueue{}
	agent := &MergeAgent{LLM: llm, MergeQueue: mq}

	entry := &ports.MergeQueueEntry{
		RunID:   "llm-resolve-run",
		Branch:  "cloche/llm-resolve-run",
		Project: repoDir,
		Status:  "in_progress",
	}

	err := agent.Merge(context.Background(), entry, repoDir)
	require.NoError(t, err)

	// Verify the resolved content is on main
	cmd := exec.Command("git", "show", "main:app.go")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	require.NoError(t, err)
	assert.Contains(t, string(out), "merged logic from both sides")

	// LLM was called
	assert.Equal(t, 1, llm.calls)

	// Branch was deleted
	branchCmd := exec.Command("git", "branch", "--list", "cloche/llm-resolve-run")
	branchCmd.Dir = repoDir
	branchOut, _ := branchCmd.Output()
	assert.Empty(t, strings.TrimSpace(string(branchOut)))
}

func TestMergeAgent_ValidationFailure(t *testing.T) {
	repoDir := initTestRepo(t)
	createFeatureBranch(t, repoDir, "cloche/validate-run", "feature.go", "package feature\n")

	mq := &mockMergeQueue{}
	agent := &MergeAgent{
		MergeQueue: mq,
		Validate:   "exit 1", // always fail
	}

	entry := &ports.MergeQueueEntry{
		RunID:   "validate-run",
		Branch:  "cloche/validate-run",
		Project: repoDir,
		Status:  "in_progress",
	}

	err := agent.Merge(context.Background(), entry, repoDir)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "validation failed")

	// Branch should be preserved since validation failed
	branchCmd := exec.Command("git", "branch", "--list", "cloche/validate-run")
	branchCmd.Dir = repoDir
	branchOut, _ := branchCmd.Output()
	assert.NotEmpty(t, strings.TrimSpace(string(branchOut)))
}

func TestMergeAgent_ProcessQueue_Success(t *testing.T) {
	repoDir := initTestRepo(t)
	createFeatureBranch(t, repoDir, "cloche/queue-run-1", "file1.go", "package file1\n")

	mq := &mockMergeQueue{
		entries: []*ports.MergeQueueEntry{
			{RunID: "queue-run-1", Branch: "cloche/queue-run-1", Project: repoDir, Status: "pending"},
		},
	}
	agent := &MergeAgent{MergeQueue: mq}

	processed := agent.ProcessQueue(context.Background(), repoDir)
	assert.True(t, processed)
	assert.Equal(t, []string{"queue-run-1"}, mq.completed)
	assert.Empty(t, mq.failed)
}

func TestMergeAgent_ProcessQueue_Failure(t *testing.T) {
	repoDir := initTestRepo(t)

	// Enqueue a run with a non-existent branch
	mq := &mockMergeQueue{
		entries: []*ports.MergeQueueEntry{
			{RunID: "bad-run", Branch: "cloche/nonexistent", Project: repoDir, Status: "pending"},
		},
	}
	agent := &MergeAgent{MergeQueue: mq}

	processed := agent.ProcessQueue(context.Background(), repoDir)
	assert.True(t, processed)
	assert.Empty(t, mq.completed)
	assert.Equal(t, []string{"bad-run"}, mq.failed)
}

func TestMergeAgent_ProcessQueue_Empty(t *testing.T) {
	mq := &mockMergeQueue{}
	agent := &MergeAgent{MergeQueue: mq}

	processed := agent.ProcessQueue(context.Background(), "/some/project")
	assert.False(t, processed)
}

func TestParseFileSections(t *testing.T) {
	input := `=== FILE: main.go ===
package main

func main() {}
=== FILE: util.go ===
package main

func helper() {}
`
	sections := parseFileSections(input)
	assert.Len(t, sections, 2)
	assert.Contains(t, sections["main.go"], "package main")
	assert.Contains(t, sections["main.go"], "func main() {}")
	assert.Contains(t, sections["util.go"], "func helper() {}")
}

func TestParseFileSections_Empty(t *testing.T) {
	sections := parseFileSections("")
	assert.Empty(t, sections)
}

func TestParseFileSections_SingleFile(t *testing.T) {
	input := "=== FILE: only.go ===\npackage only\n"
	sections := parseFileSections(input)
	assert.Len(t, sections, 1)
	assert.Contains(t, sections["only.go"], "package only")
}

// runGit is a test helper that runs a git command and fails on error.
func runGit(t *testing.T, dir string, env []string, args ...string) {
	t.Helper()
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git command %v failed: %s", args, out)
}
