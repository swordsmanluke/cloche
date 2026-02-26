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

	// Write a user prompt under a run ID
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".cloche", "test-run"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".cloche", "test-run", "prompt.txt"), []byte("add a calculator"), 0644))

	// Use a mock command that writes a file to prove it ran
	adapter := &prompt.Adapter{
		Command: "sh",
		Args:    []string{"-c", "cat > /dev/null && echo 'implemented' > result.txt"},
		RunID:   "test-run",
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
		Command: "sh",
		Args:    []string{"-c", "cat > captured_prompt.txt"},
	}

	step := &domain.Step{
		Name:    "fix",
		Type:    domain.StepTypeAgent,
		Results: []string{"success", "fail", "give-up"},
		Config:  map[string]string{"prompt": "Fix the code."},
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

func TestPromptAdapter_RespectsMaxAttempts(t *testing.T) {
	dir := t.TempDir()

	// Pre-set attempt count to the max
	attemptDir := filepath.Join(dir, ".cloche", "attempt_count")
	require.NoError(t, os.MkdirAll(attemptDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(attemptDir, "fix"), []byte("2"), 0644))

	// Command should NOT be called since max is reached
	adapter := &prompt.Adapter{
		Command: "sh",
		Args:    []string{"-c", "exit 1"}, // would fail if called
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
		Command: "sh",
		Args:    []string{"-c", "exit 1"},
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
		Command: "sh",
		Args:    []string{"-c", "cat > captured_prompt.txt"},
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
		Command: "sh",
		Args:    []string{"-c", "cat > /dev/null && echo 'CLOCHE_RESULT:needs_research'"},
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

func TestExecuteCapturesData(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".cloche", "test-run"), 0755)
	os.WriteFile(filepath.Join(dir, ".cloche", "test-run", "prompt.txt"), []byte("user request"), 0644)

	var captured prompt.CapturedData
	a := &prompt.Adapter{
		Command: "sh",
		Args:    []string{"-c", "cat > /dev/null && echo 'agent output'"},
		RunID:   "test-run",
		OnCapture: func(c prompt.CapturedData) {
			captured = c
		},
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
	assert.Contains(t, captured.PromptText, "Build something")
	assert.Contains(t, captured.PromptText, "user request")
	assert.NotEmpty(t, captured.AgentOutput)
	assert.Equal(t, 1, captured.AttemptNumber)
}

func TestPromptAdapter_IncrementsAttemptCount(t *testing.T) {
	dir := t.TempDir()

	adapter := &prompt.Adapter{
		Command: "sh",
		Args:    []string{"-c", "cat > /dev/null"},
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
