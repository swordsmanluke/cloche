package host

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func runGitForPromptRevisionTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return string(out)
}

// TestExecutor_AgentStep_RecordsPromptRevisionKV verifies that dispatching an
// agent step whose prompt config is a file("...") reference records both the
// resolved prompt file path and the git commit that last touched it, under
// the ledger's "<workflow>:<step>:prompt_file"/"prompt_rev" KV keys.
func TestExecutor_AgentStep_RecordsPromptRevisionKV(t *testing.T) {
	tmpDir := t.TempDir()
	outputDir := filepath.Join(tmpDir, "output")

	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, ".cloche", "prompts"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, ".cloche", "prompts", "implement.md"), []byte("Implement the feature."), 0644))

	runGitForPromptRevisionTest(t, tmpDir, "init")
	runGitForPromptRevisionTest(t, tmpDir, "add", ".")
	runGitForPromptRevisionTest(t, tmpDir, "commit", "-m", "add prompt")
	wantRev := strings.TrimSpace(runGitForPromptRevisionTest(t, tmpDir, "rev-parse", "HEAD"))

	mockAgent := filepath.Join(tmpDir, "mock-agent.sh")
	require.NoError(t, os.WriteFile(mockAgent, []byte("#!/bin/sh\ncat > captured_prompt.txt\necho ok\necho CLOCHE_RESULT:$CLOCHE_RESULT_NONCE:success\n"), 0755))

	store := &fakeStore{runs: map[string]*domain.Run{}}
	executor := &Executor{
		ProjectDir:   tmpDir,
		OutputDir:    outputDir,
		Store:        store,
		HostRunID:    "run1",
		TaskID:       "task1",
		AttemptID:    "att1",
		WorkflowName: "develop",
	}

	step := &domain.Step{
		Name:    "implement",
		Type:    domain.StepTypeAgent,
		Results: []string{"success", "fail"},
		Config: map[string]string{
			"prompt":        `file(".cloche/prompts/implement.md")`,
			"agent_command": mockAgent,
		},
	}

	result, err := executor.Execute(context.Background(), step)
	require.NoError(t, err)
	assert.Equal(t, "success", result.Result)

	file, ok, err := store.GetContextKey(context.Background(), "task1", "att1", "run1", "develop:implement:prompt_file")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, ".cloche/prompts/implement.md", file)

	rev, ok, err := store.GetContextKey(context.Background(), "task1", "att1", "run1", "develop:implement:prompt_rev")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, wantRev, rev)
}

// TestExecutor_AgentStep_InlinePrompt_NoRevisionRecorded verifies that steps
// whose prompt config is inline text (not a file("...") reference) don't get
// spurious prompt_file/prompt_rev KV entries.
func TestExecutor_AgentStep_InlinePrompt_NoRevisionRecorded(t *testing.T) {
	tmpDir := t.TempDir()
	outputDir := filepath.Join(tmpDir, "output")

	mockAgent := filepath.Join(tmpDir, "mock-agent.sh")
	require.NoError(t, os.WriteFile(mockAgent, []byte("#!/bin/sh\ncat > captured_prompt.txt\necho ok\necho CLOCHE_RESULT:$CLOCHE_RESULT_NONCE:success\n"), 0755))

	store := &fakeStore{runs: map[string]*domain.Run{}}
	executor := &Executor{
		ProjectDir:   tmpDir,
		OutputDir:    outputDir,
		Store:        store,
		HostRunID:    "run1",
		TaskID:       "task1",
		AttemptID:    "att1",
		WorkflowName: "develop",
	}

	step := &domain.Step{
		Name:    "implement",
		Type:    domain.StepTypeAgent,
		Results: []string{"success", "fail"},
		Config: map[string]string{
			"prompt":        "You are a coding assistant.",
			"agent_command": mockAgent,
		},
	}

	_, err := executor.Execute(context.Background(), step)
	require.NoError(t, err)

	_, ok, err := store.GetContextKey(context.Background(), "task1", "att1", "run1", "develop:implement:prompt_file")
	require.NoError(t, err)
	assert.False(t, ok)
}
