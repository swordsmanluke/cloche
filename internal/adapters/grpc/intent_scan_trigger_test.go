package grpc

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cloche-dev/cloche/internal/adapters/sqlite"
	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/host"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCreatePhaseLoop_PostTaskScanner_DefaultOn_FreshProject covers the
// bootstrap fixture from docs/plans/2026-09-14-intent-builtin-migration-design.md
// ticket B: a project with no .cloche/intent/ (and no .cloche/ at all) still
// gets wired for a post-task intent-scan now that scan_after_tasks defaults
// to true, since the workflow resolves against the built-in intent-scan.
func TestCreatePhaseLoop_PostTaskScanner_DefaultOn_FreshProject(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	srv := NewClocheServer(store, nil)
	projectDir := t.TempDir()

	loop := srv.createPhaseLoop(host.LoopConfig{ProjectDir: projectDir}, projectDir, time.Minute)

	assert.True(t, loop.PostTaskScannerConfigured(), "a fresh project should be auto-scanned after its first task by default")
	assert.NoDirExists(t, filepath.Join(projectDir, ".cloche", "intent"), "wiring the scanner must not itself create .cloche/intent/")
}

// TestCreatePhaseLoop_PostTaskScanner_OptOut_MatchesPreFeatureBehavior covers
// the other half of the ticket B fixture: a project that explicitly opts out
// via scan_after_tasks = false gets no scanner wired, byte-identical to the
// pre-feature (default-off) behavior.
func TestCreatePhaseLoop_PostTaskScanner_OptOut_MatchesPreFeatureBehavior(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	srv := NewClocheServer(store, nil)
	projectDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(projectDir, ".cloche"), 0755))
	require.NoError(t, os.WriteFile(
		filepath.Join(projectDir, ".cloche", "config.toml"),
		[]byte("[intent]\nscan_after_tasks = false\n"),
		0644,
	))

	loop := srv.createPhaseLoop(host.LoopConfig{ProjectDir: projectDir}, projectDir, time.Minute)

	assert.False(t, loop.PostTaskScannerConfigured(), "scan_after_tasks = false must disable the post-task scan trigger entirely")
	assert.NoDirExists(t, filepath.Join(projectDir, ".cloche", "intent"))
}

// TestIntentScanTrigger_ScanQueuedOrRunning covers the concurrency guard: an
// intent-scan already pending or running for the project suppresses a new
// enqueue, but a finished one does not.
func TestIntentScanTrigger_ScanQueuedOrRunning(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	srv := NewClocheServer(store, nil)
	trigger := &intentScanTrigger{server: srv}
	ctx := context.Background()
	projectDir := "/tmp/intent-scan-trigger-fixture"

	assert.False(t, trigger.scanQueuedOrRunning(ctx, projectDir), "no runs yet should not block enqueue")

	run := domain.NewRun("intent-scan:run1", "intent-scan")
	run.ProjectDir = projectDir
	run.IsHost = true
	require.NoError(t, store.CreateRun(ctx, run))

	assert.True(t, trigger.scanQueuedOrRunning(ctx, projectDir), "a pending scan should block a new enqueue")

	run.State = domain.RunStateRunning
	require.NoError(t, store.UpdateRun(ctx, run))
	assert.True(t, trigger.scanQueuedOrRunning(ctx, projectDir), "a running scan should block a new enqueue")

	run.State = domain.RunStateSucceeded
	require.NoError(t, store.UpdateRun(ctx, run))
	assert.False(t, trigger.scanQueuedOrRunning(ctx, projectDir), "a finished scan should not block a new enqueue")
}

// TestIntentScanTrigger_ScanQueuedOrRunning_OtherProjectDoesNotBlock ensures
// the guard is scoped per project directory, not global.
func TestIntentScanTrigger_ScanQueuedOrRunning_OtherProjectDoesNotBlock(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	srv := NewClocheServer(store, nil)
	trigger := &intentScanTrigger{server: srv}
	ctx := context.Background()

	other := domain.NewRun("intent-scan:run1", "intent-scan")
	other.ProjectDir = "/tmp/intent-scan-trigger-fixture-other"
	other.IsHost = true
	require.NoError(t, store.CreateRun(ctx, other))

	assert.False(t, trigger.scanQueuedOrRunning(ctx, "/tmp/intent-scan-trigger-fixture"))
}

// TestIntentScanTrigger_EnqueueScan_SkipsWhenScanQueued verifies EnqueueScan
// itself honors the guard: no new host run is dispatched while one is
// already queued or running for the project.
func TestIntentScanTrigger_EnqueueScan_SkipsWhenScanQueued(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	srv := NewClocheServer(store, nil)
	trigger := &intentScanTrigger{server: srv}
	ctx := context.Background()
	projectDir := "/tmp/intent-scan-trigger-fixture"

	run := domain.NewRun("intent-scan:run1", "intent-scan")
	run.ProjectDir = projectDir
	run.IsHost = true
	require.NoError(t, store.CreateRun(ctx, run))

	err = trigger.EnqueueScan(ctx, projectDir, "task-1")
	require.NoError(t, err)
	assert.Empty(t, srv.hostCancels, "no new host run should be dispatched while a scan is already queued or running")
}
