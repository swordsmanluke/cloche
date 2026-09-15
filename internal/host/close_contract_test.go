package host

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveCloseTaskWorkflow_Undefined(t *testing.T) {
	tmpDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, ".cloche"), 0755))

	name, ok := ResolveCloseTaskWorkflow(tmpDir)
	assert.False(t, ok)
	assert.Empty(t, name)
}

func TestResolveCloseTaskWorkflow_CloseTaskDefined(t *testing.T) {
	tmpDir := t.TempDir()
	clocheDir := filepath.Join(tmpDir, ".cloche")
	require.NoError(t, os.MkdirAll(clocheDir, 0755))

	content := `workflow close-task {
  host {}
  step close-task {
    run     = "echo closed"
    results = [success, fail]
  }
  close-task:success -> done
  close-task:fail    -> abort
}
`
	require.NoError(t, os.WriteFile(filepath.Join(clocheDir, "host.cloche"), []byte(content), 0644))

	name, ok := ResolveCloseTaskWorkflow(tmpDir)
	assert.True(t, ok)
	assert.Equal(t, "close-task", name)
}

func TestResolveCloseTaskWorkflow_CancelTaskFallback(t *testing.T) {
	tmpDir := t.TempDir()
	clocheDir := filepath.Join(tmpDir, ".cloche")
	require.NoError(t, os.MkdirAll(clocheDir, 0755))

	content := `workflow cancel-task {
  host {}
  step cancel-task {
    run     = "echo cancelled"
    results = [success, fail]
  }
  cancel-task:success -> done
  cancel-task:fail    -> abort
}
`
	require.NoError(t, os.WriteFile(filepath.Join(clocheDir, "host.cloche"), []byte(content), 0644))

	name, ok := ResolveCloseTaskWorkflow(tmpDir)
	assert.True(t, ok)
	assert.Equal(t, "cancel-task", name)
}

func TestResolveCloseTaskWorkflow_StepOnlyDoesNotCount(t *testing.T) {
	// A "close-task" step nested inside another workflow (e.g. "main") is a
	// different namespace than a standalone workflow of that name — it must
	// not satisfy the contract.
	tmpDir := t.TempDir()
	clocheDir := filepath.Join(tmpDir, ".cloche")
	require.NoError(t, os.MkdirAll(clocheDir, 0755))

	content := `workflow main {
  host {}
  step close-task {
    run     = "echo closed"
    results = [success, fail]
  }
  close-task:success -> done
  close-task:fail    -> abort
}
`
	require.NoError(t, os.WriteFile(filepath.Join(clocheDir, "host.cloche"), []byte(content), 0644))

	name, ok := ResolveCloseTaskWorkflow(tmpDir)
	assert.False(t, ok)
	assert.Empty(t, name)
}
