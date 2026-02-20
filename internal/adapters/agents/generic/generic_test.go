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
