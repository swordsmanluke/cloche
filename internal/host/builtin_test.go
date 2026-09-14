package host

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFindHostWorkflow_BuiltinFallback(t *testing.T) {
	tmpDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, ".cloche"), 0755))

	wf, err := findHostWorkflow(tmpDir, "intent-scan")
	require.NoError(t, err)
	assert.True(t, wf.Builtin)
	assert.Equal(t, domain.LocationHost, wf.Location)
	assert.Equal(t, "discover-domains", wf.EntryStep)
}

func TestFindHostWorkflow_UnknownName(t *testing.T) {
	tmpDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, ".cloche"), 0755))

	_, err := findHostWorkflow(tmpDir, "does-not-exist")
	require.Error(t, err)
}

func TestFindAllWorkflows_IncludesBuiltinFallback(t *testing.T) {
	tmpDir := t.TempDir()
	clocheDir := filepath.Join(tmpDir, ".cloche")
	require.NoError(t, os.MkdirAll(clocheDir, 0755))

	content := `workflow main {
  host {}
  step run {
    run     = "echo hi"
    results = [success]
  }
  run:success -> done
}
`
	require.NoError(t, os.WriteFile(filepath.Join(clocheDir, "host.cloche"), []byte(content), 0644))

	all, err := FindAllWorkflows(tmpDir)
	require.NoError(t, err)
	require.Contains(t, all, "intent-scan")
	assert.True(t, all["intent-scan"].Builtin)
	assert.Contains(t, all, "main")
}

func TestFindAllWorkflows_ProjectOverridesBuiltin(t *testing.T) {
	tmpDir := t.TempDir()
	clocheDir := filepath.Join(tmpDir, ".cloche")
	require.NoError(t, os.MkdirAll(clocheDir, 0755))

	// Project redefines intent-scan with a single custom step. Its version
	// must win over the built-in.
	content := `workflow intent-scan {
  host {}
  step custom-step {
    run     = "echo custom"
    results = [success]
  }
  custom-step:success -> done
}
`
	require.NoError(t, os.WriteFile(filepath.Join(clocheDir, "host.cloche"), []byte(content), 0644))

	all, err := FindAllWorkflows(tmpDir)
	require.NoError(t, err)
	require.Contains(t, all, "intent-scan")

	wf := all["intent-scan"]
	assert.False(t, wf.Builtin)
	assert.Contains(t, wf.Steps, "custom-step")
	assert.NotContains(t, wf.Steps, "discover-domains")

	// findHostWorkflow must resolve the same project override, not the built-in.
	direct, err := findHostWorkflow(tmpDir, "intent-scan")
	require.NoError(t, err)
	assert.False(t, direct.Builtin)
	assert.Contains(t, direct.Steps, "custom-step")
}
