package host

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swordsmanluke/cloche/internal/domain"
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

// TestRunner_RunNamed_TagsBuiltinAndUserInitiated covers domain.Run.IsBuiltin
// and .UserInitiated being set on the persisted run record: IsBuiltin follows
// the resolved workflow (project override aware, same as findHostWorkflow),
// and UserInitiated is threaded through verbatim from Runner.UserInitiated.
func TestRunner_RunNamed_TagsBuiltinAndUserInitiated(t *testing.T) {
	tmpDir := t.TempDir()
	clocheDir := filepath.Join(tmpDir, ".cloche")
	require.NoError(t, os.MkdirAll(clocheDir, 0755))
	hostCloche := `workflow main {
  host {}
  step greet {
    run     = "echo hi"
    results = [success, fail]
  }
  greet:success -> done
  greet:fail    -> abort
}`
	require.NoError(t, os.WriteFile(filepath.Join(clocheDir, "host.cloche"), []byte(hostCloche), 0644))

	store := &fakeStore{runs: map[string]*domain.Run{}}
	runner := &Runner{Store: store, UserInitiated: true}

	result, err := runner.RunNamed(context.Background(), tmpDir, "main")
	require.NoError(t, err)

	run, err := store.GetRun(context.Background(), result.RunID)
	require.NoError(t, err)
	assert.False(t, run.IsBuiltin, "a project-defined workflow is never built-in")
	assert.True(t, run.UserInitiated)
}

// TestRunner_RunNamed_ProjectOverrideIsNotBuiltin covers a project overriding
// the "intent-scan" name with its own workflow: the persisted run must not be
// tagged IsBuiltin, mirroring TestFindAllWorkflows_ProjectOverridesBuiltin but
// asserting what actually lands on the run row. (The no-override case — a run
// resolving to the real registered built-in — is deliberately not exercised
// here: the real intent-scan workflow's entry step is a live agent
// invocation, unsafe and slow to run in a unit test; TestFindHostWorkflow_
// BuiltinFallback already proves wf.Builtin is true in that case, and this
// runner code simply copies that value onto the run record unchanged.)
func TestRunner_RunNamed_ProjectOverrideIsNotBuiltin(t *testing.T) {
	tmpDir := t.TempDir()
	clocheDir := filepath.Join(tmpDir, ".cloche")
	require.NoError(t, os.MkdirAll(clocheDir, 0755))
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

	store := &fakeStore{runs: map[string]*domain.Run{}}
	runner := &Runner{Store: store, UserInitiated: true}

	result, err := runner.RunNamed(context.Background(), tmpDir, "intent-scan")
	require.NoError(t, err)

	run, err := store.GetRun(context.Background(), result.RunID)
	require.NoError(t, err)
	assert.False(t, run.IsBuiltin)
	assert.True(t, run.UserInitiated)
}
