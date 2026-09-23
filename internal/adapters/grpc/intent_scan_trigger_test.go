package grpc

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	pb "github.com/swordsmanluke/cloche/api/clochepb"
	"github.com/swordsmanluke/cloche/internal/adapters/sqlite"
	"github.com/swordsmanluke/cloche/internal/domain"
	"github.com/swordsmanluke/cloche/internal/host"
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

// TestIntentScanTrigger_OptOut_CheckedAtTriggerTime covers the other half
// of the ticket B fixture — scan_after_tasks = false must never enqueue a
// scan — and that the flag is honoured when flipped on a live loop: active
// projects get their loop at daemon startup, so a value cached at
// construction would ignore config edits until the next restart.
func TestIntentScanTrigger_OptOut_CheckedAtTriggerTime(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	srv := NewClocheServer(store, nil)
	projectDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(projectDir, ".cloche"), 0755))
	configPath := filepath.Join(projectDir, ".cloche", "config.toml")
	require.NoError(t, os.WriteFile(configPath, []byte("[intent]\nscan_after_tasks = true\n"), 0644))

	loop := srv.createPhaseLoop(host.LoopConfig{ProjectDir: projectDir}, projectDir, time.Minute)
	require.True(t, loop.PostTaskScannerConfigured())

	// Opt out after the loop is already built.
	require.NoError(t, os.WriteFile(configPath, []byte("[intent]\nscan_after_tasks = false\n"), 0644))

	trigger := &intentScanTrigger{server: srv}
	require.NoError(t, trigger.EnqueueScan(context.Background(), projectDir, "task-1"))
	assert.Empty(t, srv.hostCancels, "scan_after_tasks = false must not dispatch a scan, even when set after the loop started")
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

// TestPurgeProject_RefusesActiveRunThenPurges covers the daemon-side guard
// and the happy path: a project with a pending run cannot be purged; once
// the run is finished, purging removes the project entirely.
func TestPurgeProject_RefusesActiveRunThenPurges(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	srv := NewClocheServer(store, nil)
	ctx := context.Background()
	projectDir := "/tmp/purge-fixture"

	run := domain.NewRun("develop:run1", "develop")
	run.ProjectDir = projectDir
	require.NoError(t, store.CreateRun(ctx, run))

	_, err = srv.PurgeProject(ctx, &pb.PurgeProjectRequest{ProjectDir: projectDir})
	require.Error(t, err, "a pending run must block the purge")
	assert.Contains(t, err.Error(), "active run")

	run.State = domain.RunStateSucceeded
	require.NoError(t, store.UpdateRun(ctx, run))

	resp, err := srv.PurgeProject(ctx, &pb.PurgeProjectRequest{Name: "purge-fixture"})
	require.NoError(t, err)
	assert.Equal(t, projectDir, resp.ProjectDir)
	assert.EqualValues(t, 1, resp.RunsDeleted)

	projects, err := store.ListProjects(ctx)
	require.NoError(t, err)
	assert.Empty(t, projects)

	_, err = srv.PurgeProject(ctx, &pb.PurgeProjectRequest{ProjectDir: projectDir})
	assert.Error(t, err, "an unknown project must not purge silently")
}
