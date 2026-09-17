package sqlite

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBackfillRunRepositories simulates rows written before the
// repositories column existed (raw INSERT, bypassing CreateRun), including
// one whose project_dir was itself set to a [[repositories]] sub-repo path
// (the "cloche run from inside repos/cloche" bug) and verifies
// backfillRunRepositories both resolves Repositories and folds that run's
// project_dir back into its parent project.
func TestBackfillRunRepositories(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	parentDir := t.TempDir()
	subRepoDir := filepath.Join(parentDir, "repos", "cloche")
	require.NoError(t, os.MkdirAll(filepath.Join(parentDir, ".cloche"), 0755))
	configToml := "[[repositories]]\nname = \"cloche\"\npath = \"repos/cloche\"\n"
	require.NoError(t, os.WriteFile(filepath.Join(parentDir, ".cloche", "config.toml"), []byte(configToml), 0644))

	insertLegacyRun := func(id, workflowName, projectDir, repository string) {
		_, err := store.write.Exec(
			`INSERT INTO runs (id, workflow_name, state, active_steps, started_at, completed_at, project_dir, task_id, attempt_id, repository)
			 VALUES (?, ?, 'succeeded', '', '', '', ?, ?, ?, ?)`,
			id, workflowName, projectDir, id, id, repository,
		)
		require.NoError(t, err)
	}

	// Dispatched from the project root, single declared repo already known
	// via the legacy `repository` column (rule a).
	insertLegacyRun("run-a", "develop", parentDir, "cloche")
	// Mistakenly dispatched with project_dir set to the sub-repo path itself
	// (the secondary bug: "cloche run" invoked from inside repos/cloche).
	insertLegacyRun("run-b", "develop", subRepoDir, "")

	_, err = store.write.Exec(`DELETE FROM _migrations WHERE id = 'run-repositories-backfill-v1'`)
	require.NoError(t, err)
	require.NoError(t, backfillRunRepositories(store.write))

	a, err := store.GetRun(context.Background(), "run-a")
	require.NoError(t, err)
	assert.Equal(t, parentDir, a.ProjectDir)
	assert.Equal(t, []string{"cloche"}, a.Repositories)

	b, err := store.GetRun(context.Background(), "run-b")
	require.NoError(t, err)
	assert.Equal(t, parentDir, b.ProjectDir, "sub-repo-dir run must be folded into its parent project")
	assert.Equal(t, []string{"cloche"}, b.Repositories, "folded run is attributed to the sub-repo it was dispatched from (rule b)")

	// Gated by _migrations: a later legitimate edit must survive a second
	// backfill call rather than being clobbered.
	a.Repositories = []string{"cloche", "extra"}
	require.NoError(t, store.UpdateRun(context.Background(), a))
	require.NoError(t, backfillRunRepositories(store.write))
	aAgain, err := store.GetRun(context.Background(), "run-a")
	require.NoError(t, err)
	assert.Equal(t, []string{"cloche", "extra"}, aAgain.Repositories, "one-shot gate must not re-run")
}
