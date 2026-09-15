package sqlite

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBackfillAgentNames_InfersFromWorkflowConfig(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	projectDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(projectDir, ".cloche"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, ".cloche", "develop.cloche"), []byte(`workflow develop {
  step build {
    agent_command = "claude"
    prompt = "build the thing"
    results = [success, fail]
  }
  build:success -> done
  build:fail -> abort
}
`), 0644))

	run := domain.NewRun("develop-run-1", "develop")
	run.ProjectDir = projectDir
	run.Start()
	run.Complete(domain.RunStateSucceeded)
	require.NoError(t, store.CreateRun(ctx, run))

	// Simulate a legacy usage row saved before agent attribution was fixed:
	// tokens recorded, agent_name left empty.
	require.NoError(t, store.SaveCapture(ctx, run.ID, &domain.StepExecution{
		StepName: "build",
		Result:   "success",
		Usage:    &domain.TokenUsage{InputTokens: 100, OutputTokens: 50},
	}))

	require.NoError(t, store.MigrateProjectLogs(projectDir))

	var agentName string
	require.NoError(t, store.db.QueryRow(
		`SELECT agent_name FROM step_executions WHERE run_id = ? AND step_name = 'build'`, run.ID,
	).Scan(&agentName))
	assert.Equal(t, "claude", agentName)
}

func TestBackfillAgentNames_LabelsUnattributedWhenNotInferable(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	// No .cloche directory at all — the workflow file can't be found.
	projectDir := t.TempDir()

	run := domain.NewRun("develop-run-2", "develop")
	run.ProjectDir = projectDir
	run.Start()
	run.Complete(domain.RunStateSucceeded)
	require.NoError(t, store.CreateRun(ctx, run))

	require.NoError(t, store.SaveCapture(ctx, run.ID, &domain.StepExecution{
		StepName: "build",
		Result:   "success",
		Usage:    &domain.TokenUsage{InputTokens: 100, OutputTokens: 50},
	}))

	require.NoError(t, store.MigrateProjectLogs(projectDir))

	var agentName string
	require.NoError(t, store.db.QueryRow(
		`SELECT agent_name FROM step_executions WHERE run_id = ? AND step_name = 'build'`, run.ID,
	).Scan(&agentName))
	assert.Equal(t, domain.UnattributedAgent, agentName)
}

func TestBackfillAgentNames_SkipsRowsWithoutUsage(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	projectDir := t.TempDir()

	run := domain.NewRun("develop-run-3", "develop")
	run.ProjectDir = projectDir
	run.Start()
	run.Complete(domain.RunStateSucceeded)
	require.NoError(t, store.CreateRun(ctx, run))

	// A step with no recorded usage at all — agent_name should stay blank,
	// not get relabeled "unattributed" (there's nothing to attribute).
	require.NoError(t, store.SaveCapture(ctx, run.ID, &domain.StepExecution{
		StepName: "script-step",
		Result:   "success",
	}))

	require.NoError(t, store.MigrateProjectLogs(projectDir))

	var agentName string
	require.NoError(t, store.db.QueryRow(
		`SELECT agent_name FROM step_executions WHERE run_id = ? AND step_name = 'script-step'`, run.ID,
	).Scan(&agentName))
	assert.Equal(t, "", agentName)
}

func TestBackfillAgentNames_Idempotent(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	projectDir := t.TempDir()

	run := domain.NewRun("develop-run-4", "develop")
	run.ProjectDir = projectDir
	run.Start()
	run.Complete(domain.RunStateSucceeded)
	require.NoError(t, store.CreateRun(ctx, run))

	require.NoError(t, store.SaveCapture(ctx, run.ID, &domain.StepExecution{
		StepName: "build",
		Result:   "success",
		Usage:    &domain.TokenUsage{InputTokens: 100, OutputTokens: 50},
	}))

	require.NoError(t, store.MigrateProjectLogs(projectDir))
	require.NoError(t, store.MigrateProjectLogs(projectDir))

	var count int
	require.NoError(t, store.db.QueryRow(
		`SELECT COUNT(*) FROM step_executions WHERE run_id = ? AND agent_name = ?`, run.ID, domain.UnattributedAgent,
	).Scan(&count))
	assert.Equal(t, 1, count)
}
