package grpc_test

import (
	"context"
	"testing"

	pb "github.com/cloche-dev/cloche/api/clochepb"
	server "github.com/cloche-dev/cloche/internal/adapters/grpc"
	"github.com/cloche-dev/cloche/internal/adapters/sqlite"
	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServer_GetLoopOccupancy_NoLoop(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	srv := server.NewClocheServer(store, nil)

	resp, err := srv.GetLoopOccupancy(context.Background(), &pb.GetLoopOccupancyRequest{ProjectDir: t.TempDir()})
	require.NoError(t, err)
	assert.Zero(t, resp.MaxConcurrency)
	assert.Empty(t, resp.Slots)
	assert.Empty(t, resp.Queued)
	assert.Empty(t, resp.Polls)
}

func TestServer_GetLoopOccupancy_WithActiveLoop(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	dir := t.TempDir()

	run := domain.NewRun("run-1", "main")
	run.ProjectDir = dir
	run.IsHost = true
	run.TaskID = "task-1"
	run.AttemptID = "attempt-1"
	run.Start()
	run.ActiveSteps = []string{"build"}
	require.NoError(t, store.CreateRun(ctx, run))

	srv := server.NewClocheServer(store, nil)
	srv.RegisterLoop(dir, newTestLoop(dir, store))

	resp, err := srv.GetLoopOccupancy(ctx, &pb.GetLoopOccupancyRequest{ProjectDir: dir})
	require.NoError(t, err)

	assert.Equal(t, int32(1), resp.MaxConcurrency)
	require.Len(t, resp.Slots, 1)
	assert.Equal(t, "run-1", resp.Slots[0].RunId)
	assert.Equal(t, "task-1", resp.Slots[0].TaskId)
	assert.Equal(t, "attempt-1", resp.Slots[0].AttemptId)
	assert.Equal(t, "build", resp.Slots[0].CurrentStep)
	assert.NotEmpty(t, resp.Slots[0].StartedAt)
}

func TestServer_GetLoopOccupancy_RequiresProjectDir(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	srv := server.NewClocheServer(store, nil)
	_, err = srv.GetLoopOccupancy(context.Background(), &pb.GetLoopOccupancyRequest{})
	assert.Error(t, err)
}

func TestServer_ListLoopOccupancy(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	// Project A: has an active orchestration loop with one busy slot.
	dirA := t.TempDir()
	runA := domain.NewRun("run-a", "main")
	runA.ProjectDir = dirA
	runA.IsHost = true
	runA.Start()
	require.NoError(t, store.CreateRun(ctx, runA))

	// Project B: no registered loop, one failed run (red health), no active runs.
	dirB := t.TempDir()
	runB := domain.NewRun("run-b", "main")
	runB.ProjectDir = dirB
	runB.IsHost = true
	runB.Start()
	runB.Complete(domain.RunStateFailed)
	require.NoError(t, store.CreateRun(ctx, runB))

	srv := server.NewClocheServer(store, nil)
	srv.RegisterLoop(dirA, newTestLoop(dirA, store))

	resp, err := srv.ListLoopOccupancy(ctx, &pb.ListLoopOccupancyRequest{})
	require.NoError(t, err)
	require.Len(t, resp.Projects, 2)

	byDir := make(map[string]*pb.ProjectOccupancySummary, len(resp.Projects))
	for _, p := range resp.Projects {
		byDir[p.ProjectDir] = p
	}

	require.Contains(t, byDir, dirA)
	assert.Equal(t, int32(1), byDir[dirA].Running)
	assert.Equal(t, int32(0), byDir[dirA].Queued)
	assert.Equal(t, int32(0), byDir[dirA].AttentionCount)

	require.Contains(t, byDir, dirB)
	assert.Equal(t, int32(0), byDir[dirB].Running, "no active loop and no active runs")
	assert.Equal(t, "red", byDir[dirB].Health, "only a failed run in the health window")
}
