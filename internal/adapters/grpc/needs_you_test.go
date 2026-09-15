package grpc_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	pb "github.com/swordsmanluke/cloche/api/clochepb"
	server "github.com/swordsmanluke/cloche/internal/adapters/grpc"
	"github.com/swordsmanluke/cloche/internal/adapters/local"
	"github.com/swordsmanluke/cloche/internal/adapters/sqlite"
	"github.com/swordsmanluke/cloche/internal/attention"
	"github.com/swordsmanluke/cloche/internal/host"
	"github.com/swordsmanluke/cloche/internal/protocol"
)

func TestServer_CloseTask_NoContract(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".cloche"), 0755))

	srv := server.NewClocheServerWithCaptures(store, store, nil, "")
	err = srv.CloseTask(context.Background(), dir, "task-1")
	require.Error(t, err)
	assert.True(t, errors.Is(err, host.ErrNoCloseContract))
}

func TestServer_CloseTask_Success(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	dir := t.TempDir()
	clocheDir := filepath.Join(dir, ".cloche")
	require.NoError(t, os.MkdirAll(clocheDir, 0755))
	hostCloche := `workflow close-task {
  host {}

  step close-task {
    run     = "true"
    results = [success, fail]
  }
  close-task:success -> done
  close-task:fail    -> abort
}`
	require.NoError(t, os.WriteFile(filepath.Join(clocheDir, "host.cloche"), []byte(hostCloche), 0644))

	srv := server.NewClocheServerWithCaptures(store, store, nil, "")
	err = srv.CloseTask(context.Background(), dir, "task-1")
	require.NoError(t, err)
}

func TestServer_RunOnce_DispatchesSingleAttempt(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	dir := t.TempDir()

	msgs := []protocol.StatusMessage{
		{Type: protocol.MsgStepStarted, StepName: "build"},
		{Type: protocol.MsgStepCompleted, StepName: "build", Result: "success"},
		{Type: protocol.MsgRunCompleted, Result: "succeeded"},
	}
	script := "#!/bin/sh\n"
	for _, msg := range msgs {
		data, _ := json.Marshal(msg)
		script += "echo '" + string(data) + "'\n"
	}
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".cloche"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".cloche", "retry.cloche"), []byte(script), 0755))

	rt := local.NewRuntime("sh")
	srv := server.NewClocheServerWithCaptures(store, store, rt, "")

	runID, err := srv.RunOnce(context.Background(), dir, "task-42", "retry", "try again")
	require.NoError(t, err)
	assert.NotEmpty(t, runID)

	run, err := store.GetRun(context.Background(), runID)
	require.NoError(t, err)
	assert.Equal(t, "task-42", run.TaskID)

	promptData, err := os.ReadFile(filepath.Join(dir, ".cloche", "runs", run.TaskID, "prompt.txt"))
	require.NoError(t, err)
	assert.Equal(t, "try again", string(promptData))

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		status, err := srv.GetStatus(context.Background(), &pb.GetStatusRequest{RunId: runID})
		require.NoError(t, err)
		if status.State == "succeeded" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestServer_RunOnce_RequiresWorkflow(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	srv := server.NewClocheServerWithCaptures(store, store, nil, "")
	_, err = srv.RunOnce(context.Background(), t.TempDir(), "task-1", "", "")
	require.Error(t, err)
}

func TestServer_MuteAttentionItem(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	dir := t.TempDir()
	srv := server.NewClocheServerWithCaptures(store, store, nil, "")

	key := "builtin-failures:intent-scan"
	assert.False(t, attention.IsMuted(dir, key))

	require.NoError(t, srv.MuteAttentionItem(context.Background(), dir, key))
	assert.True(t, attention.IsMuted(dir, key))
}

func TestServer_MuteAttentionItem_RequiresKey(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	srv := server.NewClocheServerWithCaptures(store, store, nil, "")
	err = srv.MuteAttentionItem(context.Background(), t.TempDir(), "")
	require.Error(t, err)
}
