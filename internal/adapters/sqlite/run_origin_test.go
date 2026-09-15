package sqlite

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swordsmanluke/cloche/internal/domain"
)

func TestRunStore_IsBuiltinAndUserInitiated_RoundTrip(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	run := domain.NewRun("run-origin-1", "intent-scan")
	run.IsBuiltin = true
	run.UserInitiated = true
	require.NoError(t, store.CreateRun(ctx, run))

	got, err := store.GetRun(ctx, "run-origin-1")
	require.NoError(t, err)
	assert.True(t, got.IsBuiltin)
	assert.True(t, got.UserInitiated)

	// UpdateRun must persist changes to both flags.
	got.IsBuiltin = false
	got.UserInitiated = false
	require.NoError(t, store.UpdateRun(ctx, got))

	updated, err := store.GetRun(ctx, "run-origin-1")
	require.NoError(t, err)
	assert.False(t, updated.IsBuiltin)
	assert.False(t, updated.UserInitiated)
}

func TestRunStore_IsBuiltinAndUserInitiated_DefaultFalse(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	run := domain.NewRun("run-origin-2", "develop")
	require.NoError(t, store.CreateRun(ctx, run))

	got, err := store.GetRun(ctx, "run-origin-2")
	require.NoError(t, err)
	assert.False(t, got.IsBuiltin)
	assert.False(t, got.UserInitiated)
}

// TestBackfillRunOrigin simulates rows written before is_builtin/user_initiated
// existed (raw INSERT, bypassing CreateRun so the columns stay at their
// zero-value default) and verifies backfillRunOrigin classifies them the same
// way CreateRun would have going forward: built-in by workflow_name, and
// user-initiated only for non-builtin runs carrying a synthetic "user-xxxx"
// task ID.
func TestBackfillRunOrigin(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	insertLegacyRun := func(id, workflowName, taskID string) {
		_, err := store.db.Exec(
			`INSERT INTO runs (id, workflow_name, state, active_steps, started_at, completed_at, task_id, attempt_id)
			 VALUES (?, ?, 'failed', '', '', '', ?, ?)`,
			id, workflowName, taskID, id,
		)
		require.NoError(t, err)
	}

	insertLegacyRun("legacy-scan", "intent-scan", "user-aaaa")  // automatic trigger, masqueraded as a task
	insertLegacyRun("legacy-manual", "develop", "user-bbbb")    // real `cloche run develop`
	insertLegacyRun("legacy-external", "main", "cloche-123456") // loop-dispatched from an external tracker

	// NewStore already ran (and gated) the backfill once against an empty
	// table; reset the gate so it actually processes the rows above.
	_, err = store.db.Exec(`DELETE FROM _migrations WHERE id = 'run-origin-backfill-v1'`)
	require.NoError(t, err)
	require.NoError(t, backfillRunOrigin(store.db))

	scan, err := store.GetRun(context.Background(), "legacy-scan")
	require.NoError(t, err)
	assert.True(t, scan.IsBuiltin, "intent-scan is a known built-in workflow")
	assert.False(t, scan.UserInitiated, "backfill defaults built-in runs to non-user-initiated")

	manual, err := store.GetRun(context.Background(), "legacy-manual")
	require.NoError(t, err)
	assert.False(t, manual.IsBuiltin)
	assert.True(t, manual.UserInitiated, "synthetic user-xxxx task ID on a non-builtin run implies a manual `cloche run`")

	external, err := store.GetRun(context.Background(), "legacy-external")
	require.NoError(t, err)
	assert.False(t, external.IsBuiltin)
	assert.False(t, external.UserInitiated, "an external task ID implies loop dispatch, not a manual run")

	// Gated by _migrations: a later, legitimate update (e.g. a subsequent
	// manual `cloche run intent-scan` explicitly marking the row
	// user-initiated) must survive a second backfill call rather than being
	// reclassified.
	scan.UserInitiated = true
	require.NoError(t, store.UpdateRun(context.Background(), scan))
	require.NoError(t, backfillRunOrigin(store.db))
	scanAgain, err := store.GetRun(context.Background(), "legacy-scan")
	require.NoError(t, err)
	assert.True(t, scanAgain.UserInitiated, "the one-shot gate must not re-run and clobber a later legitimate update")
}
