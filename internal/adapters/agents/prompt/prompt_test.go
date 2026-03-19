package prompt_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cloche-dev/cloche/internal/adapters/agents/prompt"
	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPromptAdapter_ExecutesCommand(t *testing.T) {
	dir := t.TempDir()

	// Write a user prompt under a task ID
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".cloche", "runs", "test-task"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".cloche", "runs", "test-task", "prompt.txt"), []byte("add a calculator"), 0644))

	// Use a mock command that writes a file to prove it ran
	adapter := &prompt.Adapter{
		Commands:     []string{"sh"},
		ExplicitArgs: []string{"-c", "cat > /dev/null && echo 'implemented' > result.txt && echo ok"},
		RunID:        "test-run",
		TaskID:       "test-task",
	}

	step := &domain.Step{
		Name:    "implement",
		Type:    domain.StepTypeAgent,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"prompt": "You are a coding assistant."},
	}

	result, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)
	assert.Equal(t, "success", result)

	// Verify the mock command ran
	content, err := os.ReadFile(filepath.Join(dir, "result.txt"))
	require.NoError(t, err)
	assert.Contains(t, string(content), "implemented")
}

func TestPromptAdapter_IncludesFeedback(t *testing.T) {
	dir := t.TempDir()

	// Set up feedback logs
	outputDir := filepath.Join(dir, ".cloche", "output")
	require.NoError(t, os.MkdirAll(outputDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(outputDir, "test.log"), []byte("3 failures"), 0644))

	// Mock command that captures stdin to a file so we can inspect it
	adapter := &prompt.Adapter{
		Commands:     []string{"sh"},
		ExplicitArgs: []string{"-c", "cat > captured_prompt.txt && echo ok"},
	}

	step := &domain.Step{
		Name:    "fix",
		Type:    domain.StepTypeAgent,
		Results: []string{"success", "fail", "give-up"},
		Config:  map[string]string{"prompt": "Fix the code.", "feedback": "true"},
	}

	result, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)
	assert.Equal(t, "success", result)

	// Verify feedback was included in the prompt
	captured, err := os.ReadFile(filepath.Join(dir, "captured_prompt.txt"))
	require.NoError(t, err)
	assert.Contains(t, string(captured), "Fix the code.")
	assert.Contains(t, string(captured), "3 failures")
}

func TestPromptAdapter_NoFeedbackByDefault(t *testing.T) {
	dir := t.TempDir()

	// Set up feedback logs
	outputDir := filepath.Join(dir, ".cloche", "output")
	require.NoError(t, os.MkdirAll(outputDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(outputDir, "test.log"), []byte("3 failures"), 0644))

	// Mock command that captures stdin to a file so we can inspect it
	adapter := &prompt.Adapter{
		Commands:     []string{"sh"},
		ExplicitArgs: []string{"-c", "cat > captured_prompt.txt && echo ok"},
	}

	step := &domain.Step{
		Name:    "update-docs",
		Type:    domain.StepTypeAgent,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"prompt": "Update the docs."},
	}

	result, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)
	assert.Equal(t, "success", result)

	// Verify feedback was NOT included in the prompt
	captured, err := os.ReadFile(filepath.Join(dir, "captured_prompt.txt"))
	require.NoError(t, err)
	assert.Contains(t, string(captured), "Update the docs.")
	assert.NotContains(t, string(captured), "3 failures")
	assert.NotContains(t, string(captured), "Validation Output")
}

func TestPromptAdapter_RespectsMaxAttempts(t *testing.T) {
	dir := t.TempDir()

	// Pre-set attempt count to the max
	attemptDir := filepath.Join(dir, ".cloche", "attempt_count")
	require.NoError(t, os.MkdirAll(attemptDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(attemptDir, "fix"), []byte("2"), 0644))

	// Command should NOT be called since max is reached
	adapter := &prompt.Adapter{
		Commands:     []string{"sh"},
		ExplicitArgs: []string{"-c", "exit 1"}, // would fail if called
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

	result, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)
	assert.Equal(t, "give-up", result)
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

	result, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)
	assert.Equal(t, "fail", result)
}

func TestPromptAdapter_InjectsResultInstructions(t *testing.T) {
	dir := t.TempDir()

	adapter := &prompt.Adapter{
		Commands:     []string{"sh"},
		ExplicitArgs: []string{"-c", "cat > captured_prompt.txt && echo ok"},
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
	assert.Contains(t, string(captured), "CLOCHE_RESULT:success")
	assert.Contains(t, string(captured), "CLOCHE_RESULT:fail")
	assert.Contains(t, string(captured), "CLOCHE_RESULT:needs_research")
}

func TestPromptAdapter_StdoutMarkerSelectsResult(t *testing.T) {
	dir := t.TempDir()

	adapter := &prompt.Adapter{
		Commands:     []string{"sh"},
		ExplicitArgs: []string{"-c", "cat > /dev/null && echo 'CLOCHE_RESULT:needs_research'"},
	}

	step := &domain.Step{
		Name:    "analyze",
		Type:    domain.StepTypeAgent,
		Results: []string{"success", "fail", "needs_research"},
		Config:  map[string]string{"prompt": "Analyze the code."},
	}

	result, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)
	assert.Equal(t, "needs_research", result)
}

func TestExecuteWritesOutputFile(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".cloche", "runs", "test-task"), 0755)
	os.WriteFile(filepath.Join(dir, ".cloche", "runs", "test-task", "prompt.txt"), []byte("user request"), 0644)

	a := &prompt.Adapter{
		Commands:     []string{"sh"},
		ExplicitArgs: []string{"-c", "cat > /dev/null && echo 'agent output'"},
		RunID:        "test-run",
		TaskID:       "test-task",
	}

	step := &domain.Step{
		Name:    "implement",
		Type:    domain.StepTypeAgent,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"prompt": "Build something"},
	}

	result, err := a.Execute(context.Background(), step, dir)
	require.NoError(t, err)
	assert.Equal(t, "success", result)

	// Verify output was written to file
	outputPath := filepath.Join(dir, ".cloche", "output", "implement.log")
	data, err := os.ReadFile(outputPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "agent output")
}

func TestPromptAdapter_IncrementsAttemptCount(t *testing.T) {
	dir := t.TempDir()

	adapter := &prompt.Adapter{
		Commands:     []string{"sh"},
		ExplicitArgs: []string{"-c", "cat > /dev/null"},
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
	countPath := filepath.Join(dir, ".cloche", "attempt_count", "fix")
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
		ExplicitArgs: []string{"-c", "cat > /dev/null && echo 'fallback ran'"},
	}

	step := &domain.Step{
		Name:    "implement",
		Type:    domain.StepTypeAgent,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"prompt": "Do something."},
	}

	result, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)
	assert.Equal(t, "success", result)

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
	require.NoError(t, os.WriteFile(succeeding, []byte("#!/bin/sh\ncat > /dev/null\necho 'good agent output'\n"), 0755))

	adapter := &prompt.Adapter{
		Commands: []string{failing, succeeding},
	}

	step := &domain.Step{
		Name:    "implement",
		Type:    domain.StepTypeAgent,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"prompt": "Do something."},
	}

	result, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)
	assert.Equal(t, "success", result)

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
	require.NoError(t, os.WriteFile(failing, []byte("#!/bin/sh\ncat > /dev/null\necho 'CLOCHE_RESULT:fail'\nexit 1\n"), 0755))

	shouldNotRun := filepath.Join(dir, "should-not-run.sh")
	require.NoError(t, os.WriteFile(shouldNotRun, []byte("#!/bin/sh\ncat > /dev/null\necho 'CLOCHE_RESULT:success'\n"), 0755))

	adapter := &prompt.Adapter{
		Commands: []string{failing, shouldNotRun},
	}

	step := &domain.Step{
		Name:    "implement",
		Type:    domain.StepTypeAgent,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"prompt": "Do something."},
	}

	result, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)
	// Should use the first agent's result, not fall back
	assert.Equal(t, "fail", result)
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

	result, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)
	assert.Equal(t, "fail", result)
}

func TestPromptAdapter_SingleCommandPreservesBehavior(t *testing.T) {
	dir := t.TempDir()

	// Single command, exit 0 — should behave exactly like before
	adapter := &prompt.Adapter{
		Commands:     []string{"sh"},
		ExplicitArgs: []string{"-c", "cat > /dev/null && echo hello"},
	}

	step := &domain.Step{
		Name:    "implement",
		Type:    domain.StepTypeAgent,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"prompt": "Do something."},
	}

	result, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)
	assert.Equal(t, "success", result)
}

func TestPromptAdapter_DefaultArgsForClaude(t *testing.T) {
	// Verify New() creates an adapter with "claude" as the default command
	adapter := prompt.New()
	assert.Equal(t, []string{"claude"}, adapter.Commands)
	assert.Nil(t, adapter.ExplicitArgs)
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
