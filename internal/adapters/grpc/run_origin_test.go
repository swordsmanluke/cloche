package grpc_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	pb "github.com/cloche-dev/cloche/api/clochepb"
	server "github.com/cloche-dev/cloche/internal/adapters/grpc"
	"github.com/cloche-dev/cloche/internal/adapters/local"
	"github.com/cloche-dev/cloche/internal/adapters/sqlite"
	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestServer_RunWorkflow_TagsUserInitiated covers RunWorkflow's container-run
// path: every real caller of this RPC (CLI `cloche run`, the dashboard "Scan
// now" button, `cloche intent scan`) is a direct external request, never a
// propagated/loop-dispatched attempt, so the created run is always tagged
// UserInitiated. IsBuiltin follows the resolved workflow — false here since
// "test" is an ordinary project-defined workflow.
func TestServer_RunWorkflow_TagsUserInitiated(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	dir := t.TempDir()
	msgs := []protocol.StatusMessage{
		{Type: protocol.MsgRunCompleted, Result: "succeeded"},
	}
	script := "#!/bin/sh\n"
	for _, msg := range msgs {
		data, _ := json.Marshal(msg)
		script += "echo '" + string(data) + "'\n"
	}
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".cloche"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".cloche", "test.cloche"), []byte(script), 0755))

	rt := local.NewRuntime("sh")
	srv := server.NewClocheServerWithCaptures(store, store, rt, "")

	resp, err := srv.RunWorkflow(context.Background(), &pb.RunWorkflowRequest{
		WorkflowName: "test",
		ProjectDir:   dir,
	})
	require.NoError(t, err)

	run, err := store.GetRun(context.Background(), resp.RunId)
	require.NoError(t, err)
	assert.True(t, run.UserInitiated)
	assert.False(t, run.IsBuiltin)
}

// TestServer_ListRuns_IncludesOriginFields verifies IsBuiltin/UserInitiated
// round-trip through the ListRuns RPC (RunSummary), which is what `cloche
// list --runs` renders.
func TestServer_ListRuns_IncludesOriginFields(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	run := domain.NewRun("run-origin", "intent-scan")
	run.IsBuiltin = true
	run.UserInitiated = false
	require.NoError(t, store.CreateRun(ctx, run))

	srv := server.NewClocheServer(store, nil)
	resp, err := srv.ListRuns(ctx, &pb.ListRunsRequest{All: true})
	require.NoError(t, err)
	require.Len(t, resp.Runs, 1)
	assert.True(t, resp.Runs[0].IsBuiltin)
	assert.False(t, resp.Runs[0].UserInitiated)
}

// TestServer_ListTasks_MarksBuiltinTask verifies a task whose only run is a
// built-in workflow (e.g. the synthetic task created for the automatic
// intent-scan trigger) is marked IsBuiltin in TaskSummary, so `cloche list`
// can show it distinctly instead of as an ordinary task.
func TestServer_ListTasks_MarksBuiltinTask(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	projectDir := t.TempDir()

	task := &domain.Task{ID: "user-aaaa", Title: "Incremental intent scan", ProjectDir: projectDir}
	require.NoError(t, store.SaveTask(ctx, task))

	run := domain.NewRun("intent-scan:aaaa", "intent-scan")
	run.ProjectDir = projectDir
	run.TaskID = "user-aaaa"
	run.IsBuiltin = true
	require.NoError(t, store.CreateRun(ctx, run))

	srv := server.NewClocheServer(store, nil)
	srv.SetTaskStore(store)
	resp, err := srv.ListTasks(ctx, &pb.ListTasksRequest{ProjectDir: projectDir})
	require.NoError(t, err)
	require.Len(t, resp.Tasks, 1)
	assert.True(t, resp.Tasks[0].IsBuiltin)
}
