package prompt_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swordsmanluke/cloche/internal/adapters/agents/prompt"
	"github.com/swordsmanluke/cloche/internal/domain"
	"github.com/swordsmanluke/cloche/internal/protocol"
)

func TestPromptAdapter_ExecutesCommand(t *testing.T) {
	dir := t.TempDir()

	// Write a user prompt under a task ID
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".cloche", "runs", "test-task"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".cloche", "runs", "test-task", "prompt.txt"), []byte("add a calculator"), 0644))

	// Use a mock command that writes a file to prove it ran
	adapter := &prompt.Adapter{
		Commands:     []string{"sh"},
		ExplicitArgs: []string{"-c", "cat > /dev/null && echo 'implemented' > result.txt && echo ok && echo CLOCHE_RESULT:$CLOCHE_RESULT_NONCE:success"},
		RunID:        "test-run",
		TaskID:       "test-task",
	}

	step := &domain.Step{
		Name:    "implement",
		Type:    domain.StepTypeAgent,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"prompt": "You are a coding assistant."},
	}

	sr, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)
	assert.Equal(t, "success", sr.Result)

	// Verify the mock command ran
	content, err := os.ReadFile(filepath.Join(dir, "result.txt"))
	require.NoError(t, err)
	assert.Contains(t, string(content), "implemented")
}

func TestPromptAdapter_PreviousOutputSubstitution(t *testing.T) {
	dir := t.TempDir()

	// Mock command that captures stdin to a file so we can inspect it
	adapter := &prompt.Adapter{
		Commands:     []string{"sh"},
		ExplicitArgs: []string{"-c", "cat > captured_prompt.txt && echo ok && echo CLOCHE_RESULT:$CLOCHE_RESULT_NONCE:success"},
		PrevOutput:   "test passed: 42/42",
	}

	step := &domain.Step{
		Name:    "fix",
		Type:    domain.StepTypeAgent,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"prompt": "Previous results: {previous_output}"},
	}

	sr, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)
	assert.Equal(t, "success", sr.Result)

	captured, err := os.ReadFile(filepath.Join(dir, "captured_prompt.txt"))
	require.NoError(t, err)
	assert.Contains(t, string(captured), "test passed: 42/42")
}

func TestPromptAdapter_PreviousOutputEmptyWhenNotSet(t *testing.T) {
	dir := t.TempDir()

	// Set up output logs from other steps — these should NOT appear in the prompt
	outputDir := filepath.Join(dir, ".cloche", "output")
	require.NoError(t, os.MkdirAll(outputDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(outputDir, "other-step.log"), []byte("sibling output"), 0644))

	// Mock command that captures stdin to a file so we can inspect it
	adapter := &prompt.Adapter{
		Commands:     []string{"sh"},
		ExplicitArgs: []string{"-c", "cat > captured_prompt.txt && echo ok && echo CLOCHE_RESULT:$CLOCHE_RESULT_NONCE:success"},
		// PrevOutput not set — {previous_output} should substitute empty string
	}

	step := &domain.Step{
		Name:    "update-docs",
		Type:    domain.StepTypeAgent,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"prompt": "Context: {previous_output}\nUpdate the docs."},
	}

	sr, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)
	assert.Equal(t, "success", sr.Result)

	captured, err := os.ReadFile(filepath.Join(dir, "captured_prompt.txt"))
	require.NoError(t, err)
	assert.Contains(t, string(captured), "Update the docs.")
	assert.NotContains(t, string(captured), "sibling output")
}

func TestPromptAdapter_RespectsMaxAttempts(t *testing.T) {
	dir := t.TempDir()
	taskID := "test-task"

	// Pre-set attempt count to the max
	attemptDir := filepath.Join(dir, ".cloche", "runs", taskID, "attempt_count")
	require.NoError(t, os.MkdirAll(attemptDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(attemptDir, "fix"), []byte("2"), 0644))

	// Command should NOT be called since max is reached
	adapter := &prompt.Adapter{
		Commands:     []string{"sh"},
		ExplicitArgs: []string{"-c", "exit 1"}, // would fail if called
		TaskID:       taskID,
	}

	step := &domain.Step{
		Name:    "fix",
		Type:    domain.StepTypeAgent,
		Results: []string{"fixed", "give-up"},
		Config: map[string]string{
			"prompt":       "Fix the code.",
			"max_attempts": "2",
		},
	}

	sr, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)
	assert.Equal(t, "give-up", sr.Result)
}

func TestPromptAdapter_CommandFailure(t *testing.T) {
	dir := t.TempDir()

	adapter := &prompt.Adapter{
		Commands:     []string{"sh"},
		ExplicitArgs: []string{"-c", "exit 1"},
	}

	step := &domain.Step{
		Name:    "implement",
		Type:    domain.StepTypeAgent,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"prompt": "Do something."},
	}

	sr, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)
	assert.Equal(t, "fail", sr.Result)
}

// TestPromptAdapter_NoMarkerDefaultsToFail is the regression test for the
// bug where an agent that aborted (e.g. missing task prompt) but exited 0
// without emitting a CLOCHE_RESULT marker was classified as "success" and
// rode the merge path. A missing marker must never default to success.
func TestPromptAdapter_NoMarkerDefaultsToFail(t *testing.T) {
	dir := t.TempDir()

	adapter := &prompt.Adapter{
		Commands:     []string{"sh"},
		ExplicitArgs: []string{"-c", "cat > /dev/null && echo 'Result: FAIL - task prompt file not found'"},
	}

	step := &domain.Step{
		Name:    "implement",
		Type:    domain.StepTypeAgent,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"prompt": "Do something."},
	}

	sr, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)
	assert.Equal(t, "fail", sr.Result)
}

func TestPromptAdapter_InjectsResultInstructions(t *testing.T) {
	dir := t.TempDir()

	adapter := &prompt.Adapter{
		Commands:     []string{"sh"},
		ExplicitArgs: []string{"-c", "cat > captured_prompt.txt && echo ok && echo CLOCHE_RESULT:$CLOCHE_RESULT_NONCE:success"},
	}

	step := &domain.Step{
		Name:    "analyze",
		Type:    domain.StepTypeAgent,
		Results: []string{"success", "fail", "needs_research"},
		Config:  map[string]string{"prompt": "Analyze the code."},
	}

	_, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)

	captured, err := os.ReadFile(filepath.Join(dir, "captured_prompt.txt"))
	require.NoError(t, err)
	nonce := readResultNonce(t, dir, "", "analyze")
	assert.Contains(t, string(captured), "CLOCHE_RESULT:"+nonce+":success")
	assert.Contains(t, string(captured), "CLOCHE_RESULT:"+nonce+":fail")
	assert.Contains(t, string(captured), "CLOCHE_RESULT:"+nonce+":needs_research")
}

// readResultNonce reads back the per-step nonce the adapter generated and
// persisted for (taskID, stepName) under workDir, so tests can assert
// against the exact nonce-framed marker text the assembled prompt carried.
func readResultNonce(t *testing.T, workDir, taskID, stepName string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(workDir, ".cloche", "runs", taskID, "result_nonce", stepName))
	require.NoError(t, err)
	return string(data)
}

// TestPromptAdapter_NoResultsStillGetsMarkerReminder is the regression test
// for cloche init's scaffolded prompts (and any out-of-tree project) that
// declare a `prompt` step with no `results = [...]` — the "## Result
// Selection" section only fires when results are declared, but
// classifyResult unconditionally requires a CLOCHE_RESULT marker on exit 0
// regardless. Without a belt-and-braces reminder, such a step would fail
// every run with no instruction telling the agent why.
func TestPromptAdapter_NoResultsStillGetsMarkerReminder(t *testing.T) {
	dir := t.TempDir()

	adapter := &prompt.Adapter{
		Commands:     []string{"sh"},
		ExplicitArgs: []string{"-c", "cat > captured_prompt.txt && echo ok && echo CLOCHE_RESULT:$CLOCHE_RESULT_NONCE:success"},
	}

	step := &domain.Step{
		Name:   "implement",
		Type:   domain.StepTypeAgent,
		Config: map[string]string{"prompt": "Do something."},
	}

	_, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)

	captured, err := os.ReadFile(filepath.Join(dir, "captured_prompt.txt"))
	require.NoError(t, err)
	nonce := readResultNonce(t, dir, "", "implement")
	assert.Contains(t, string(captured), "CLOCHE_RESULT:"+nonce+":success")
	assert.Contains(t, string(captured), "CLOCHE_RESULT:"+nonce+":fail")
}

// TestPromptAdapter_NoReminderWhenAlreadyMentioned ensures the belt-and-braces
// reminder does not pile on top of an explicit template that already covers
// the marker protocol (e.g. the hand-authored .cloche/prompts/*.md files).
func TestPromptAdapter_NoReminderWhenAlreadyMentioned(t *testing.T) {
	dir := t.TempDir()

	adapter := &prompt.Adapter{
		Commands:     []string{"sh"},
		ExplicitArgs: []string{"-c", "cat > captured_prompt.txt && echo ok && echo CLOCHE_RESULT:$CLOCHE_RESULT_NONCE:success"},
	}

	step := &domain.Step{
		Name: "implement",
		Type: domain.StepTypeAgent,
		Config: map[string]string{
			"prompt": "Do something.\n\nPrint CLOCHE_RESULT:success or CLOCHE_RESULT:fail when done.",
		},
	}

	_, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)

	captured, err := os.ReadFile(filepath.Join(dir, "captured_prompt.txt"))
	require.NoError(t, err)
	assert.NotContains(t, string(captured), "## Reporting your result (required)")
}

func TestPromptAdapter_StdoutMarkerSelectsResult(t *testing.T) {
	dir := t.TempDir()

	adapter := &prompt.Adapter{
		Commands:     []string{"sh"},
		ExplicitArgs: []string{"-c", "cat > /dev/null && echo CLOCHE_RESULT:$CLOCHE_RESULT_NONCE:needs_research"},
	}

	step := &domain.Step{
		Name:    "analyze",
		Type:    domain.StepTypeAgent,
		Results: []string{"success", "fail", "needs_research"},
		Config:  map[string]string{"prompt": "Analyze the code."},
	}

	sr, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)
	assert.Equal(t, "needs_research", sr.Result)
}

func TestExecuteWritesOutputFile(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".cloche", "runs", "test-task"), 0755)
	os.WriteFile(filepath.Join(dir, ".cloche", "runs", "test-task", "prompt.txt"), []byte("user request"), 0644)

	a := &prompt.Adapter{
		Commands:     []string{"sh"},
		ExplicitArgs: []string{"-c", "cat > /dev/null && echo 'agent output' && echo CLOCHE_RESULT:$CLOCHE_RESULT_NONCE:success"},
		RunID:        "test-run",
		TaskID:       "test-task",
	}

	step := &domain.Step{
		Name:    "implement",
		Type:    domain.StepTypeAgent,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"prompt": "Build something"},
	}

	sr, err := a.Execute(context.Background(), step, dir)
	require.NoError(t, err)
	assert.Equal(t, "success", sr.Result)

	// Verify output was written to file
	outputPath := filepath.Join(dir, ".cloche", "output", "implement.log")
	data, err := os.ReadFile(outputPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "agent output")
}

// TestExecuteDerivesPerAttemptOutputDir verifies the safe default for callers
// that set task/attempt IDs but no explicit OutputDir: output and history land
// in the per-attempt logs dir, not the shared .cloche/output layout that
// caused cross-run contamination (cloche-1or8).
func TestExecuteDerivesPerAttemptOutputDir(t *testing.T) {
	dir := t.TempDir()

	a := &prompt.Adapter{
		Commands:     []string{"sh"},
		ExplicitArgs: []string{"-c", "cat > /dev/null && echo 'agent output' && echo CLOCHE_RESULT:$CLOCHE_RESULT_NONCE:success"},
		RunID:        "test-run",
		TaskID:       "test-task",
		AttemptID:    "test-attempt",
	}

	step := &domain.Step{
		Name:    "implement",
		Type:    domain.StepTypeAgent,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"prompt": "Build something"},
	}

	sr, err := a.Execute(context.Background(), step, dir)
	require.NoError(t, err)
	assert.Equal(t, "success", sr.Result)

	attemptDir := filepath.Join(dir, ".cloche", "logs", "test-task", "test-attempt")
	data, err := os.ReadFile(filepath.Join(attemptDir, "implement.log"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "agent output")

	hist, err := os.ReadFile(filepath.Join(attemptDir, "history.log"))
	require.NoError(t, err)
	assert.Contains(t, string(hist), "step:implement result:success")

	// The shared layout must not be touched.
	_, statErr := os.Stat(filepath.Join(dir, ".cloche", "output", "implement.log"))
	assert.True(t, os.IsNotExist(statErr))
	_, statErr = os.Stat(filepath.Join(dir, ".cloche", "history.log"))
	assert.True(t, os.IsNotExist(statErr))
}

// TestExecutePinnedOutputDirKeepsLegacyHistory verifies the container
// configuration: OutputDir explicitly pinned to <workDir>/.cloche/output (as
// the in-container session does) keeps history at the legacy
// .cloche/history.log location even when task/attempt IDs are set.
func TestExecutePinnedOutputDirKeepsLegacyHistory(t *testing.T) {
	dir := t.TempDir()

	a := &prompt.Adapter{
		Commands:     []string{"sh"},
		ExplicitArgs: []string{"-c", "cat > /dev/null && echo 'agent output' && echo CLOCHE_RESULT:$CLOCHE_RESULT_NONCE:success"},
		TaskID:       "test-task",
		AttemptID:    "test-attempt",
		OutputDir:    filepath.Join(dir, ".cloche", "output"),
	}

	step := &domain.Step{
		Name:    "implement",
		Type:    domain.StepTypeAgent,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"prompt": "Build something"},
	}

	_, err := a.Execute(context.Background(), step, dir)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, ".cloche", "output", "implement.log"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "agent output")

	hist, err := os.ReadFile(filepath.Join(dir, ".cloche", "history.log"))
	require.NoError(t, err)
	assert.Contains(t, string(hist), "step:implement result:success")
}

func TestPromptAdapter_IncrementsAttemptCount(t *testing.T) {
	dir := t.TempDir()
	taskID := "test-task"

	adapter := &prompt.Adapter{
		Commands:     []string{"sh"},
		ExplicitArgs: []string{"-c", "cat > /dev/null"},
		TaskID:       taskID,
	}

	step := &domain.Step{
		Name:    "fix",
		Type:    domain.StepTypeAgent,
		Results: []string{"fixed", "give-up"},
		Config:  map[string]string{"prompt": "Fix it."},
	}

	// Execute twice
	_, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)
	_, err = adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)

	// Verify count is 2
	countPath := filepath.Join(dir, ".cloche", "runs", taskID, "attempt_count", "fix")
	data, err := os.ReadFile(countPath)
	require.NoError(t, err)
	assert.Equal(t, "2", string(data))
}

// --- Fallback chain tests ---

func TestPromptAdapter_FallbackOnCommandNotFound(t *testing.T) {
	dir := t.TempDir()

	// First command doesn't exist, second one does
	adapter := &prompt.Adapter{
		Commands:     []string{"nonexistent-agent-xyz", "sh"},
		ExplicitArgs: []string{"-c", "cat > /dev/null && echo 'fallback ran' && echo CLOCHE_RESULT:$CLOCHE_RESULT_NONCE:success"},
	}

	step := &domain.Step{
		Name:    "implement",
		Type:    domain.StepTypeAgent,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"prompt": "Do something."},
	}

	sr, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)
	assert.Equal(t, "success", sr.Result)

	// Verify the fallback command's output was captured
	outputPath := filepath.Join(dir, ".cloche", "output", "implement.log")
	data, err := os.ReadFile(outputPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "fallback ran")
}

func TestPromptAdapter_FallbackOnExitErrorNoMarker(t *testing.T) {
	dir := t.TempDir()

	// Create two scripts: first exits 1 without marker, second succeeds
	failing := filepath.Join(dir, "failing-agent.sh")
	require.NoError(t, os.WriteFile(failing, []byte("#!/bin/sh\ncat > /dev/null\nexit 1\n"), 0755))

	succeeding := filepath.Join(dir, "good-agent.sh")
	require.NoError(t, os.WriteFile(succeeding, []byte("#!/bin/sh\ncat > /dev/null\necho 'good agent output'\necho CLOCHE_RESULT:$CLOCHE_RESULT_NONCE:success\n"), 0755))

	adapter := &prompt.Adapter{
		Commands: []string{failing, succeeding},
	}

	step := &domain.Step{
		Name:    "implement",
		Type:    domain.StepTypeAgent,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"prompt": "Do something."},
	}

	sr, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)
	assert.Equal(t, "success", sr.Result)

	// Verify the fallback command's output was captured
	outputPath := filepath.Join(dir, ".cloche", "output", "implement.log")
	data, err := os.ReadFile(outputPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "good agent output")
}

func TestPromptAdapter_NoFallbackOnMarkerResult(t *testing.T) {
	dir := t.TempDir()

	// First command exits 1 but reports a CLOCHE_RESULT marker — should NOT fall back
	failing := filepath.Join(dir, "reporting-agent.sh")
	require.NoError(t, os.WriteFile(failing, []byte("#!/bin/sh\ncat > /dev/null\necho CLOCHE_RESULT:$CLOCHE_RESULT_NONCE:fail\nexit 1\n"), 0755))

	shouldNotRun := filepath.Join(dir, "should-not-run.sh")
	require.NoError(t, os.WriteFile(shouldNotRun, []byte("#!/bin/sh\ncat > /dev/null\necho CLOCHE_RESULT:$CLOCHE_RESULT_NONCE:success\n"), 0755))

	adapter := &prompt.Adapter{
		Commands: []string{failing, shouldNotRun},
	}

	step := &domain.Step{
		Name:    "implement",
		Type:    domain.StepTypeAgent,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"prompt": "Do something."},
	}

	sr, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)
	// Should use the first agent's result, not fall back
	assert.Equal(t, "fail", sr.Result)
}

func TestPromptAdapter_AllCommandsFail(t *testing.T) {
	dir := t.TempDir()

	// All commands don't exist
	adapter := &prompt.Adapter{
		Commands: []string{"nonexistent-agent-1", "nonexistent-agent-2"},
	}

	step := &domain.Step{
		Name:    "implement",
		Type:    domain.StepTypeAgent,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"prompt": "Do something."},
	}

	_, err := adapter.Execute(context.Background(), step, dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to start")
}

func TestPromptAdapter_LastCommandCrashReturnsFailResult(t *testing.T) {
	dir := t.TempDir()

	// Both commands exit non-zero without marker
	failing1 := filepath.Join(dir, "failing1.sh")
	require.NoError(t, os.WriteFile(failing1, []byte("#!/bin/sh\ncat > /dev/null\necho 'crash1'\nexit 1\n"), 0755))

	failing2 := filepath.Join(dir, "failing2.sh")
	require.NoError(t, os.WriteFile(failing2, []byte("#!/bin/sh\ncat > /dev/null\necho 'crash2'\nexit 1\n"), 0755))

	adapter := &prompt.Adapter{
		Commands: []string{failing1, failing2},
	}

	step := &domain.Step{
		Name:    "implement",
		Type:    domain.StepTypeAgent,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"prompt": "Do something."},
	}

	sr, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)
	assert.Equal(t, "fail", sr.Result)
}

func TestPromptAdapter_SingleCommandPreservesBehavior(t *testing.T) {
	dir := t.TempDir()

	// Single command, exit 0 — should behave exactly like before
	adapter := &prompt.Adapter{
		Commands:     []string{"sh"},
		ExplicitArgs: []string{"-c", "cat > /dev/null && echo hello && echo CLOCHE_RESULT:$CLOCHE_RESULT_NONCE:success"},
	}

	step := &domain.Step{
		Name:    "implement",
		Type:    domain.StepTypeAgent,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"prompt": "Do something."},
	}

	sr, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)
	assert.Equal(t, "success", sr.Result)
}

func TestPromptAdapter_DefaultArgsForClaude(t *testing.T) {
	// Verify New() creates an adapter with "claude" as the default command
	adapter := prompt.New()
	assert.Equal(t, []string{"claude"}, adapter.Commands)
	assert.Nil(t, adapter.ExplicitArgs)
}

func TestPromptAdapter_UsageCommandFromStepConfig(t *testing.T) {
	dir := t.TempDir()

	// Mock agent that produces no stream-json usage
	adapter := &prompt.Adapter{
		Commands:     []string{"sh"},
		ExplicitArgs: []string{"-c", "cat > /dev/null && echo ok && echo CLOCHE_RESULT:$CLOCHE_RESULT_NONCE:success"},
	}

	step := &domain.Step{
		Name:    "implement",
		Type:    domain.StepTypeAgent,
		Results: []string{"success", "fail"},
		Config: map[string]string{
			"prompt":        "Do something.",
			"usage_command": `echo '{"input_tokens":200,"output_tokens":75}'`,
		},
	}

	sr, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)
	assert.Equal(t, "success", sr.Result)
	require.NotNil(t, sr.Usage)
	assert.Equal(t, int64(200), sr.Usage.InputTokens)
	assert.Equal(t, int64(75), sr.Usage.OutputTokens)
}

func TestPromptAdapter_UsageCommandFromAdapterField(t *testing.T) {
	dir := t.TempDir()

	adapter := &prompt.Adapter{
		Commands:     []string{"sh"},
		ExplicitArgs: []string{"-c", "cat > /dev/null && echo ok && echo CLOCHE_RESULT:$CLOCHE_RESULT_NONCE:success"},
		UsageCommand: `echo '{"input_tokens":300,"output_tokens":120}'`,
	}

	step := &domain.Step{
		Name:    "implement",
		Type:    domain.StepTypeAgent,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"prompt": "Do something."},
	}

	sr, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)
	assert.Equal(t, "success", sr.Result)
	require.NotNil(t, sr.Usage)
	assert.Equal(t, int64(300), sr.Usage.InputTokens)
	assert.Equal(t, int64(120), sr.Usage.OutputTokens)
}

func TestPromptAdapter_StepConfigUsageCommandOverridesAdapterField(t *testing.T) {
	dir := t.TempDir()

	adapter := &prompt.Adapter{
		Commands:     []string{"sh"},
		ExplicitArgs: []string{"-c", "cat > /dev/null && echo ok && echo CLOCHE_RESULT:$CLOCHE_RESULT_NONCE:success"},
		UsageCommand: `echo '{"input_tokens":999,"output_tokens":999}'`,
	}

	step := &domain.Step{
		Name:    "implement",
		Type:    domain.StepTypeAgent,
		Results: []string{"success", "fail"},
		Config: map[string]string{
			"prompt":        "Do something.",
			"usage_command": `echo '{"input_tokens":42,"output_tokens":17}'`,
		},
	}

	sr, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)
	require.NotNil(t, sr.Usage)
	// Step config wins over adapter field
	assert.Equal(t, int64(42), sr.Usage.InputTokens)
	assert.Equal(t, int64(17), sr.Usage.OutputTokens)
}

func TestPromptAdapter_UsageCommandFailsDegracefully(t *testing.T) {
	dir := t.TempDir()

	adapter := &prompt.Adapter{
		Commands:     []string{"sh"},
		ExplicitArgs: []string{"-c", "cat > /dev/null && echo ok && echo CLOCHE_RESULT:$CLOCHE_RESULT_NONCE:success"},
	}

	step := &domain.Step{
		Name:    "implement",
		Type:    domain.StepTypeAgent,
		Results: []string{"success", "fail"},
		Config: map[string]string{
			"prompt":        "Do something.",
			"usage_command": "exit 1",
		},
	}

	sr, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)
	assert.Equal(t, "success", sr.Result)
	// Usage is nil when usage_command fails
	assert.Nil(t, sr.Usage)
}

func TestPromptAdapter_NoUsageCommandReturnsNilUsage(t *testing.T) {
	dir := t.TempDir()

	adapter := &prompt.Adapter{
		Commands:     []string{"sh"},
		ExplicitArgs: []string{"-c", "cat > /dev/null && echo ok && echo CLOCHE_RESULT:$CLOCHE_RESULT_NONCE:success"},
	}

	step := &domain.Step{
		Name:    "implement",
		Type:    domain.StepTypeAgent,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"prompt": "Do something."},
	}

	sr, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)
	assert.Equal(t, "success", sr.Result)
	assert.Nil(t, sr.Usage)
}

func TestPromptAdapter_SetsAgentNameInUsage(t *testing.T) {
	dir := t.TempDir()
	// Simulate a claude-style stream-json result with usage data. The %s
	// placeholder is filled in by printf from $CLOCHE_RESULT_NONCE so the
	// marker embedded in the "result" field carries this run's nonce.
	streamJSON := `{"type":"result","subtype":"success","result":"CLOCHE_RESULT:%s:success","usage":{"input_tokens":100,"output_tokens":50}}`
	adapter := &prompt.Adapter{
		Commands:     []string{"sh"},
		ExplicitArgs: []string{"-c", "cat > /dev/null && printf '" + streamJSON + "\\n' \"$CLOCHE_RESULT_NONCE\""},
	}

	step := &domain.Step{
		Name:    "implement",
		Type:    domain.StepTypeAgent,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"prompt": "Do something."},
	}

	sr, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)
	assert.Equal(t, "success", sr.Result)
	require.NotNil(t, sr.Usage)
	assert.Equal(t, "sh", sr.Usage.AgentName)
	assert.Equal(t, int64(100), sr.Usage.InputTokens)
	assert.Equal(t, int64(50), sr.Usage.OutputTokens)
}

func TestPromptAdapter_SetsAgentNameViaUsageCommand(t *testing.T) {
	dir := t.TempDir()
	// Command produces no stream-json usage; usage_command provides it.
	adapter := &prompt.Adapter{
		Commands:     []string{"sh"},
		ExplicitArgs: []string{"-c", "cat > /dev/null && echo ok && echo CLOCHE_RESULT:$CLOCHE_RESULT_NONCE:success"},
	}

	step := &domain.Step{
		Name:    "implement",
		Type:    domain.StepTypeAgent,
		Results: []string{"success", "fail"},
		Config: map[string]string{
			"prompt":        "Do something.",
			"usage_command": `echo '{"input_tokens":200,"output_tokens":75}'`,
		},
	}

	sr, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)
	assert.Equal(t, "success", sr.Result)
	require.NotNil(t, sr.Usage)
	assert.Equal(t, "sh", sr.Usage.AgentName)
	assert.Equal(t, int64(200), sr.Usage.InputTokens)
	assert.Equal(t, int64(75), sr.Usage.OutputTokens)
}

// TestPromptAdapter_UsageAlwaysCarriesAgentName asserts the invariant that
// every non-nil StepResult.Usage the prompt adapter returns has a non-empty
// AgentName, across every code path that can produce one: the buffered
// (StatusWriter == nil) stream-json extraction, the streaming
// (StatusWriter != nil) per-line extraction, and the usage_command fallback.
// A usage record with an empty AgentName is silently unattributed once it
// reaches the store's per-agent breakdown (see cloche-bb79).
func TestPromptAdapter_UsageAlwaysCarriesAgentName(t *testing.T) {
	streamJSON := `{"type":"result","subtype":"success","result":"CLOCHE_RESULT:%s:success","usage":{"input_tokens":100,"output_tokens":50}}`

	tests := []struct {
		name       string
		adapter    func(statusBuf *bytes.Buffer) *prompt.Adapter
		stepConfig map[string]string
	}{
		{
			name: "buffered stream-json usage",
			adapter: func(_ *bytes.Buffer) *prompt.Adapter {
				return &prompt.Adapter{
					Commands:     []string{"sh"},
					ExplicitArgs: []string{"-c", "cat > /dev/null && printf '" + streamJSON + "\\n' \"$CLOCHE_RESULT_NONCE\""},
				}
			},
			stepConfig: map[string]string{"prompt": "Do something."},
		},
		{
			name: "streaming stream-json usage",
			adapter: func(statusBuf *bytes.Buffer) *prompt.Adapter {
				return &prompt.Adapter{
					Commands:     []string{"sh"},
					ExplicitArgs: []string{"-c", "cat > /dev/null && printf '" + streamJSON + "\\n' \"$CLOCHE_RESULT_NONCE\""},
					StatusWriter: protocol.NewStatusWriter(statusBuf),
				}
			},
			stepConfig: map[string]string{"prompt": "Do something."},
		},
		{
			name: "usage_command fallback",
			adapter: func(_ *bytes.Buffer) *prompt.Adapter {
				return &prompt.Adapter{
					Commands:     []string{"sh"},
					ExplicitArgs: []string{"-c", "cat > /dev/null && echo ok && echo CLOCHE_RESULT:$CLOCHE_RESULT_NONCE:success"},
				}
			},
			stepConfig: map[string]string{
				"prompt":        "Do something.",
				"usage_command": `echo '{"input_tokens":200,"output_tokens":75}'`,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			var statusBuf bytes.Buffer
			adapter := tc.adapter(&statusBuf)

			step := &domain.Step{
				Name:    "implement",
				Type:    domain.StepTypeAgent,
				Results: []string{"success", "fail"},
				Config:  tc.stepConfig,
			}

			sr, err := adapter.Execute(context.Background(), step, dir)
			require.NoError(t, err)
			assert.Equal(t, "success", sr.Result)
			require.NotNil(t, sr.Usage, "expected a usage record to be captured")
			assert.NotEmpty(t, sr.Usage.AgentName, "usage record must carry a non-empty agent name")
		})
	}
}

func TestPromptAdapter_ExtraEnvPropagatedToAgent(t *testing.T) {
	dir := t.TempDir()

	// The agent script echoes the value of the injected env var.
	adapter := &prompt.Adapter{
		Commands:     []string{"sh"},
		ExplicitArgs: []string{"-c", "cat > /dev/null && echo \"TASK=$CLOCHE_TASK_ID RUN=$CLOCHE_RUN_ID\" && echo CLOCHE_RESULT:$CLOCHE_RESULT_NONCE:success"},
		ExtraEnv:     []string{"CLOCHE_TASK_ID=test-task-42", "CLOCHE_RUN_ID=run-99"},
	}

	step := &domain.Step{
		Name:    "implement",
		Type:    domain.StepTypeAgent,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"prompt": "Do something."},
	}

	sr, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)
	assert.Equal(t, "success", sr.Result)

	outputPath := filepath.Join(dir, ".cloche", "output", "implement.log")
	data, err := os.ReadFile(outputPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "TASK=test-task-42")
	assert.Contains(t, string(data), "RUN=run-99")
}

// TestPromptAdapter_MarkerInsideStreamJSONResultField is the regression test
// for the host executor classifying a completed agent step as "fail" because
// its CLOCHE_RESULT marker lived inside the stream-json "result" event's
// "result" string field rather than on its own raw stdout line. The host
// path (no StatusWriter set, so tryCommand takes the buffered branch) must
// extract stream-json text the same way the streaming/in-container path
// does before scanning for the marker.
func TestPromptAdapter_MarkerInsideStreamJSONResultField(t *testing.T) {
	dir := t.TempDir()

	resultEvent := map[string]any{
		"type":    "result",
		"subtype": "success",
		"result":  "All done implementing the feature.\n\nCLOCHE_RESULT:NONCE_PLACEHOLDER:success",
	}
	line, err := json.Marshal(resultEvent)
	require.NoError(t, err)

	// Split around the placeholder so the nonce is substituted as a printf
	// %s argument rather than spliced into the format string itself: the
	// JSON payload's literal "\n" bytes must reach stdout unchanged (as a
	// real agent's stream-json output would), but printf reinterprets
	// backslash escapes that appear in the format string — only arguments
	// substituted via %s are passed through verbatim.
	parts := strings.SplitN(string(line), "NONCE_PLACEHOLDER", 2)
	require.Len(t, parts, 2)

	adapter := &prompt.Adapter{
		Commands:     []string{"sh"},
		ExplicitArgs: []string{"-c", "cat > /dev/null && printf '%s%s%s\\n' '" + parts[0] + "' \"$CLOCHE_RESULT_NONCE\" '" + parts[1] + "'"},
	}

	step := &domain.Step{
		Name:    "implement",
		Type:    domain.StepTypeAgent,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"prompt": "Do something."},
	}

	sr, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)
	assert.Equal(t, "success", sr.Result)
}

// TestPromptAdapter_StrayQuotedMarkerDoesNotPoisonResult is the regression
// test for the vulnerability where an agent's own transcript quoting the bare
// "CLOCHE_RESULT:<name>" protocol string (e.g. grepping code, echoing a test
// fixture, discussing the marker itself) got mistaken for the genuine
// terminal marker. The nonce framing means only a marker carrying this run's
// nonce counts — a bare, unnonced line is just text.
func TestPromptAdapter_StrayQuotedMarkerDoesNotPoisonResult(t *testing.T) {
	dir := t.TempDir()

	adapter := &prompt.Adapter{
		Commands: []string{"sh"},
		ExplicitArgs: []string{"-c",
			`cat > /dev/null && ` +
				`echo "grep found: CLOCHE_RESULT:success in fixtures_test.go" && ` +
				`echo "CLOCHE_RESULT:success" && ` + // bare, unnonced — must be ignored
				`echo CLOCHE_RESULT:$CLOCHE_RESULT_NONCE:fail`,
		},
	}

	step := &domain.Step{
		Name:    "implement",
		Type:    domain.StepTypeAgent,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"prompt": "Do something."},
	}

	sr, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)
	assert.Equal(t, "fail", sr.Result)
}

// TestPromptAdapter_ResumeReusesPersistedNonce verifies that the nonce
// generated for a step's first invocation is reused on a later
// ResumeConversation invocation of the same step: the resume prompt is just
// "retry"/an answer, never restating the marker instructions, so the agent on
// the other end only ever learned the original nonce.
func TestPromptAdapter_ResumeReusesPersistedNonce(t *testing.T) {
	dir := t.TempDir()
	taskID := "test-task"

	adapter := &prompt.Adapter{
		Commands:     []string{"sh"},
		ExplicitArgs: []string{"-c", "cat > /dev/null && echo CLOCHE_RESULT:$CLOCHE_RESULT_NONCE:success"},
		TaskID:       taskID,
	}

	step := &domain.Step{
		Name:    "implement",
		Type:    domain.StepTypeAgent,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"prompt": "Do something."},
	}

	sr, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)
	require.Equal(t, "success", sr.Result)
	firstNonce := readResultNonce(t, dir, taskID, "implement")

	adapter.ResumeConversation = true
	sr, err = adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)
	assert.Equal(t, "success", sr.Result, "resumed call must classify using the same nonce the agent originally learned")
	assert.Equal(t, firstNonce, readResultNonce(t, dir, taskID, "implement"))
}

// recoveryProbeScript returns a fake agent script that distinguishes the
// initial invocation from the follow-up recovery invocation by checking for
// the "-c" resume flag maybeRecoverMissingMarker prepends for non-claude
// commands (see runRecoveryTurn): $1 is unset on the first call and "-c" on
// the recovery call. onRecovery is the shell snippet run when $1 == "-c".
func recoveryProbeScript(onRecovery string) string {
	return "#!/bin/sh\ncat > /dev/null\n" +
		"if [ \"$1\" = \"-c\" ]; then\n" + onRecovery + "\nfi\n" +
		"echo 'did real work, forgot the marker'\n"
}

// TestPromptAdapter_RecoversMissingMarkerAfterExitZero is the regression
// test for cloche-usjb/cloche-fnn6: an agent that exits 0 with real,
// test-passing work but drops the trailing CLOCHE_RESULT marker (a failure
// mode long sessions hit systematically) gets exactly one follow-up turn
// resuming the same invocation before being classified as a failure. When
// that follow-up turn emits the marker, its result is honored.
func TestPromptAdapter_RecoversMissingMarkerAfterExitZero(t *testing.T) {
	dir := t.TempDir()

	script := filepath.Join(dir, "forgetful-agent.sh")
	require.NoError(t, os.WriteFile(script, []byte(
		recoveryProbeScript("echo CLOCHE_RESULT:$CLOCHE_RESULT_NONCE:success\nexit 0"),
	), 0755))

	adapter := &prompt.Adapter{
		Commands: []string{script},
	}

	step := &domain.Step{
		Name:    "implement",
		Type:    domain.StepTypeAgent,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"prompt": "Do something."},
	}

	sr, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)
	assert.Equal(t, "success", sr.Result, "recovery turn's marker must be honored")

	// Both turns' output should be preserved in the step log.
	outputPath := filepath.Join(dir, ".cloche", "output", "implement.log")
	data, err := os.ReadFile(outputPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "did real work, forgot the marker")
}

// TestPromptAdapter_RecoveryStillMissingMarkerFails verifies that when the
// one recovery turn also fails to produce a marker, the adapter keeps the
// existing conservative "fail" (cloche-anu5) rather than retrying again or
// defaulting to success.
func TestPromptAdapter_RecoveryStillMissingMarkerFails(t *testing.T) {
	dir := t.TempDir()

	// Never emits a marker, on either the initial or the recovery call.
	script := filepath.Join(dir, "forgetful-agent.sh")
	require.NoError(t, os.WriteFile(script, []byte(
		"#!/bin/sh\ncat > /dev/null\necho 'did real work, forgot the marker'\n",
	), 0755))

	adapter := &prompt.Adapter{
		Commands: []string{script},
	}

	step := &domain.Step{
		Name:    "implement",
		Type:    domain.StepTypeAgent,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"prompt": "Do something."},
	}

	sr, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)
	assert.Equal(t, "fail", sr.Result)
}

// TestPromptAdapter_RecoveryNotAttemptedOnNonzeroExit verifies the recovery
// turn only ever fires for the exit-0-no-marker case: a command that exits
// nonzero without a marker is unaffected and must not trigger a follow-up
// invocation. The script would report "success" if the recovery flag were
// ever passed to it, so a "fail" result proves the follow-up never ran.
func TestPromptAdapter_RecoveryNotAttemptedOnNonzeroExit(t *testing.T) {
	dir := t.TempDir()

	script := filepath.Join(dir, "crashing-agent.sh")
	require.NoError(t, os.WriteFile(script, []byte(
		"#!/bin/sh\ncat > /dev/null\n"+
			"if [ \"$1\" = \"-c\" ]; then\necho CLOCHE_RESULT:$CLOCHE_RESULT_NONCE:success\nexit 0\nfi\n"+
			"echo 'crashed without marker'\nexit 1\n",
	), 0755))

	adapter := &prompt.Adapter{
		Commands: []string{script},
	}

	step := &domain.Step{
		Name:    "implement",
		Type:    domain.StepTypeAgent,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"prompt": "Do something."},
	}

	sr, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)
	assert.Equal(t, "fail", sr.Result)

	outputPath := filepath.Join(dir, ".cloche", "output", "implement.log")
	data, err := os.ReadFile(outputPath)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "success", "recovery turn must not run for a nonzero exit")
}

// TestPromptAdapter_RecoveryTurnLoggedToStatusWriter verifies the recovery
// turn is logged via StatusWriter, satisfying the requirement that a
// recovery attempt is visible in the step's live log stream.
func TestPromptAdapter_RecoveryTurnLoggedToStatusWriter(t *testing.T) {
	dir := t.TempDir()

	script := filepath.Join(dir, "forgetful-agent.sh")
	require.NoError(t, os.WriteFile(script, []byte(
		recoveryProbeScript("echo CLOCHE_RESULT:$CLOCHE_RESULT_NONCE:success\nexit 0"),
	), 0755))

	var statusBuf bytes.Buffer
	adapter := &prompt.Adapter{
		Commands:     []string{script},
		StatusWriter: protocol.NewStatusWriter(&statusBuf),
	}

	step := &domain.Step{
		Name:    "implement",
		Type:    domain.StepTypeAgent,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"prompt": "Do something."},
	}

	sr, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)
	assert.Equal(t, "success", sr.Result)

	msgs, err := protocol.ParseStatusStream(statusBuf.Bytes())
	require.NoError(t, err)
	var loggedRecovery bool
	for _, m := range msgs {
		if m.Type == protocol.MsgLog && strings.Contains(m.Message, "recovery turn") {
			loggedRecovery = true
		}
	}
	assert.True(t, loggedRecovery, "recovery turn must be logged via StatusWriter")
}

func TestParseCommands(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{"claude", []string{"claude"}},
		{"claude,gemini", []string{"claude", "gemini"}},
		{"claude, gemini, codex", []string{"claude", "gemini", "codex"}},
		{" claude , gemini , codex ", []string{"claude", "gemini", "codex"}},
		{"claude,,gemini", []string{"claude", "gemini"}},
		{"", nil},
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			result := prompt.ParseCommands(tc.input)
			assert.Equal(t, tc.expected, result)
		})
	}
}
