package grpc_test

import (
	"context"
	"testing"
	"time"

	pb "github.com/cloche-dev/cloche/api/clochepb"
	server "github.com/cloche-dev/cloche/internal/adapters/grpc"
	"github.com/cloche-dev/cloche/internal/adapters/sqlite"
	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/help"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newHelpTestServer(t *testing.T) (*server.ClocheServer, *sqlite.Store, *help.Router) {
	t.Helper()
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })

	srv := server.NewClocheServer(store, nil)
	router := help.NewRouter(store, time.Hour)
	srv.SetHelpRouter(router)
	return srv, store, router
}

func TestServer_AskHelp_Disabled(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	srv := server.NewClocheServer(store, nil)
	_, err = srv.AskHelp(context.Background(), &pb.AskHelpRequest{RunId: "run-1", Question: "q?"})
	assert.Error(t, err)
}

func TestServer_AskHelp_RoundTrip(t *testing.T) {
	srv, store, _ := newHelpTestServer(t)
	ctx := context.Background()

	run := domain.NewRun("run-1", "develop")
	run.ProjectDir = "/tmp/my-project"
	run.TaskID = "task-a"
	run.AttemptID = "att-1"
	require.NoError(t, store.CreateRun(ctx, run))

	resultCh := make(chan *pb.AskHelpResponse, 1)
	errCh := make(chan error, 1)
	go func() {
		resp, err := srv.AskHelp(ctx, &pb.AskHelpRequest{
			TaskId: "task-a", AttemptId: "att-1", RunId: "run-1", StepName: "plan",
			Question: "Which schema should I use?",
		})
		resultCh <- resp
		errCh <- err
	}()

	require.Eventually(t, func() bool {
		listResp, err := srv.ListThreads(ctx, &pb.ListThreadsRequest{})
		return err == nil && len(listResp.Threads) == 1
	}, time.Second, 5*time.Millisecond)

	listResp, err := srv.ListThreads(ctx, &pb.ListThreadsRequest{})
	require.NoError(t, err)
	require.Len(t, listResp.Threads, 1)
	thread := listResp.Threads[0]
	assert.Equal(t, "my-project", thread.Channel)

	// GetStatus and ListRuns should surface the pending question while blocked.
	statusResp, err := srv.GetStatus(ctx, &pb.GetStatusRequest{RunId: "run-1"})
	require.NoError(t, err)
	assert.Equal(t, thread.Channel+"/"+thread.Name, statusResp.PendingHelpAddress)

	runsResp, err := srv.ListRuns(ctx, &pb.ListRunsRequest{All: true})
	require.NoError(t, err)
	require.Len(t, runsResp.Runs, 1)
	assert.Equal(t, thread.Channel+"/"+thread.Name, runsResp.Runs[0].PendingHelpAddress)

	_, err = srv.ReplyThread(ctx, &pb.ReplyThreadRequest{Address: thread.Channel + "/" + thread.Name, Body: "Use schema B"})
	require.NoError(t, err)

	select {
	case resp := <-resultCh:
		assert.Equal(t, "Use schema B", resp.Answer)
		assert.Equal(t, thread.Id, resp.ThreadId)
		assert.False(t, resp.Parked)
	case <-time.After(2 * time.Second):
		t.Fatal("AskHelp did not return after ReplyThread")
	}
	require.NoError(t, <-errCh)

	getResp, err := srv.GetThread(ctx, &pb.GetThreadRequest{Address: thread.Channel + "/" + thread.Name})
	require.NoError(t, err)
	require.Len(t, getResp.Messages, 2)
	assert.Equal(t, "agent", getResp.Messages[0].Author)
	assert.Equal(t, "user", getResp.Messages[1].Author)

	// The run's pending question should be gone now.
	statusResp, err = srv.GetStatus(ctx, &pb.GetStatusRequest{RunId: "run-1"})
	require.NoError(t, err)
	assert.Empty(t, statusResp.PendingHelpAddress)
}

func TestServer_AskHelp_SecondConcurrentRejected(t *testing.T) {
	srv, store, _ := newHelpTestServer(t)
	ctx := context.Background()

	run := domain.NewRun("run-1", "develop")
	run.ProjectDir = "/tmp/my-project"
	require.NoError(t, store.CreateRun(ctx, run))

	go srv.AskHelp(ctx, &pb.AskHelpRequest{RunId: "run-1", Question: "First?"})
	require.Eventually(t, func() bool {
		listResp, err := srv.ListThreads(ctx, &pb.ListThreadsRequest{})
		return err == nil && len(listResp.Threads) == 1
	}, time.Second, 5*time.Millisecond)

	_, err := srv.AskHelp(ctx, &pb.AskHelpRequest{RunId: "run-1", Question: "Second?"})
	assert.Error(t, err)

	listResp, err := srv.ListThreads(ctx, &pb.ListThreadsRequest{})
	require.NoError(t, err)
	require.Len(t, listResp.Threads, 1)
	_, err = srv.ReplyThread(ctx, &pb.ReplyThreadRequest{
		Address: listResp.Threads[0].Channel + "/" + listResp.Threads[0].Name, Body: "ok",
	})
	require.NoError(t, err)
}

func TestServer_AskHelp_MissingRunID(t *testing.T) {
	srv, _, _ := newHelpTestServer(t)
	_, err := srv.AskHelp(context.Background(), &pb.AskHelpRequest{Question: "q?"})
	assert.Error(t, err)
}

func TestServer_AskHelpForRun_RoundTrip(t *testing.T) {
	srv, store, _ := newHelpTestServer(t)
	ctx := context.Background()

	run := domain.NewRun("run-1", "develop")
	run.ProjectDir = "/tmp/mcp-project"
	run.TaskID = "task-mcp"
	run.AttemptID = "att-mcp"
	require.NoError(t, store.CreateRun(ctx, run))

	resultCh := make(chan string, 1)
	threadCh := make(chan string, 1)
	go func() {
		answer, threadID, parked, err := srv.AskHelpForRun(ctx, "run-1", "Which schema?", "", "", nil, "")
		require.NoError(t, err)
		assert.False(t, parked)
		resultCh <- answer
		threadCh <- threadID
	}()

	require.Eventually(t, func() bool {
		listResp, err := srv.ListThreads(ctx, &pb.ListThreadsRequest{})
		return err == nil && len(listResp.Threads) == 1
	}, time.Second, 5*time.Millisecond)

	listResp, err := srv.ListThreads(ctx, &pb.ListThreadsRequest{})
	require.NoError(t, err)
	require.Len(t, listResp.Threads, 1)
	thread := listResp.Threads[0]
	assert.Equal(t, "mcp-project", thread.Channel)
	// task_id/attempt_id came from the resolved run record, not client input.
	assert.Equal(t, "task-mcp", thread.TaskId)
	assert.Equal(t, "att-mcp", thread.AttemptId)

	_, err = srv.ReplyThread(ctx, &pb.ReplyThreadRequest{Address: thread.Channel + "/" + thread.Name, Body: "Schema B"})
	require.NoError(t, err)

	select {
	case answer := <-resultCh:
		assert.Equal(t, "Schema B", answer)
	case <-time.After(2 * time.Second):
		t.Fatal("AskHelpForRun did not return after ReplyThread")
	}
	assert.Equal(t, thread.Id, <-threadCh)
}

func TestServer_AskHelpForRun_Disabled(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	srv := server.NewClocheServer(store, nil)
	_, _, _, err = srv.AskHelpForRun(context.Background(), "run-1", "q?", "", "", nil, "")
	assert.Error(t, err)
}
