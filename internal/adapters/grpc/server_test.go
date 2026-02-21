package grpc_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	pb "github.com/cloche-dev/cloche/api/clochepb"
	server "github.com/cloche-dev/cloche/internal/adapters/grpc"
	"github.com/cloche-dev/cloche/internal/adapters/local"
	"github.com/cloche-dev/cloche/internal/adapters/sqlite"
	"github.com/cloche-dev/cloche/internal/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServer_ListRuns_Empty(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	srv := server.NewClocheServer(store, nil)
	resp, err := srv.ListRuns(context.Background(), &pb.ListRunsRequest{})
	require.NoError(t, err)
	assert.Empty(t, resp.Runs)
}

func TestServer_GetStatus_NotFound(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	srv := server.NewClocheServer(store, nil)
	_, err = srv.GetStatus(context.Background(), &pb.GetStatusRequest{RunId: "nonexistent"})
	assert.Error(t, err)
}

func TestServer_RunWorkflow(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	dir := t.TempDir()

	// Create a mock "agent" shell script at test.cloche that outputs JSON status lines.
	// The local runtime runs: sh <projectDir>/test.cloche
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
	require.NoError(t, os.WriteFile(filepath.Join(dir, "test.cloche"), []byte(script), 0755))

	// Use "sh" as the agent binary so it runs: sh test.cloche
	rt := local.NewRuntime("sh")
	srv := server.NewClocheServerWithCaptures(store, store, rt, "")

	resp, err := srv.RunWorkflow(context.Background(), &pb.RunWorkflowRequest{
		WorkflowName: "test",
		ProjectDir:   dir,
		Prompt:       "hello world",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, resp.RunId)

	// Verify prompt was written
	promptData, err := os.ReadFile(filepath.Join(dir, ".cloche", "prompt.txt"))
	require.NoError(t, err)
	assert.Equal(t, "hello world", string(promptData))

	// Poll until the background goroutine finishes processing (up to 5s)
	var status *pb.GetStatusResponse
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		status, err = srv.GetStatus(context.Background(), &pb.GetStatusRequest{RunId: resp.RunId})
		require.NoError(t, err)
		if status.State == "succeeded" && len(status.StepExecutions) >= 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Check that the run was tracked and completed
	require.NotNil(t, status)
	assert.Equal(t, resp.RunId, status.RunId)
	assert.Equal(t, "succeeded", status.State)
	assert.GreaterOrEqual(t, len(status.StepExecutions), 1)
}

func TestServer_RunWorkflow_NoRuntime(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	srv := server.NewClocheServer(store, nil)
	_, err = srv.RunWorkflow(context.Background(), &pb.RunWorkflowRequest{
		WorkflowName: "test",
		ProjectDir:   t.TempDir(),
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no container runtime configured")
}
