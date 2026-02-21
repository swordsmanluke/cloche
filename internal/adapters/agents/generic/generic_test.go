package generic_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cloche-dev/cloche/internal/adapters/agents/generic"
	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenericAdapter_ScriptSuccess(t *testing.T) {
	adapter := generic.New()
	step := &domain.Step{
		Name:    "build",
		Type:    domain.StepTypeScript,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"run": "echo hello"},
	}

	result, err := adapter.Execute(context.Background(), step, t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, "success", result)
}

func TestGenericAdapter_ScriptFailure(t *testing.T) {
	adapter := generic.New()
	step := &domain.Step{
		Name:    "build",
		Type:    domain.StepTypeScript,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"run": "exit 1"},
	}

	result, err := adapter.Execute(context.Background(), step, t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, "fail", result)
}

func TestGenericAdapter_ScriptModifiesFiles(t *testing.T) {
	dir := t.TempDir()
	adapter := generic.New()
	step := &domain.Step{
		Name:    "generate",
		Type:    domain.StepTypeScript,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"run": "echo 'generated' > output.txt"},
	}

	result, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)
	assert.Equal(t, "success", result)

	content, err := os.ReadFile(filepath.Join(dir, "output.txt"))
	require.NoError(t, err)
	assert.Contains(t, string(content), "generated")
}

func TestGenericAdapter_CapturesOutput(t *testing.T) {
	dir := t.TempDir()
	adapter := generic.New()
	step := &domain.Step{
		Name:    "test",
		Type:    domain.StepTypeScript,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"run": "echo 'hello from test'; echo 'error msg' >&2"},
	}

	result, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)
	assert.Equal(t, "success", result)

	logPath := filepath.Join(dir, ".cloche", "output", "test.log")
	content, err := os.ReadFile(logPath)
	require.NoError(t, err)
	assert.Contains(t, string(content), "hello from test")
	assert.Contains(t, string(content), "error msg")
}

func TestGenericAdapter_CapturesOutputOnFailure(t *testing.T) {
	dir := t.TempDir()
	adapter := generic.New()
	step := &domain.Step{
		Name:    "lint",
		Type:    domain.StepTypeScript,
		Results: []string{"success", "fail"},
		Config:  map[string]string{"run": "echo 'lint error: bad style'; exit 1"},
	}

	result, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)
	assert.Equal(t, "fail", result)

	logPath := filepath.Join(dir, ".cloche", "output", "lint.log")
	content, err := os.ReadFile(logPath)
	require.NoError(t, err)
	assert.Contains(t, string(content), "lint error: bad style")
}

func TestGenericAdapter_StdoutMarkerOverridesExitCode(t *testing.T) {
	dir := t.TempDir()
	adapter := generic.New()
	step := &domain.Step{
		Name:    "analyze",
		Type:    domain.StepTypeScript,
		Results: []string{"success", "fail", "needs_research"},
		Config:  map[string]string{"run": "echo 'analyzing...' && echo 'CLOCHE_RESULT:needs_research'"},
	}

	result, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)
	assert.Equal(t, "needs_research", result)

	// Verify marker is stripped from log
	logPath := filepath.Join(dir, ".cloche", "output", "analyze.log")
	content, err := os.ReadFile(logPath)
	require.NoError(t, err)
	assert.NotContains(t, string(content), "CLOCHE_RESULT")
	assert.Contains(t, string(content), "analyzing...")
}

func TestGenericAdapter_MarkerOverridesFailExitCode(t *testing.T) {
	dir := t.TempDir()
	adapter := generic.New()
	step := &domain.Step{
		Name:    "triage",
		Type:    domain.StepTypeScript,
		Results: []string{"bug_fix", "feature_request"},
		Config:  map[string]string{"run": "echo 'CLOCHE_RESULT:bug_fix' && exit 1"},
	}

	result, err := adapter.Execute(context.Background(), step, dir)
	require.NoError(t, err)
	assert.Equal(t, "bug_fix", result)
}
