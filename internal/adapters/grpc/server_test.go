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
	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	grpclib "google.golang.org/grpc"
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

	// Verify prompt was written to run-specific path
	promptData, err := os.ReadFile(filepath.Join(dir, ".cloche", resp.RunId, "prompt.txt"))
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

func TestServer_RunWorkflow_CapturesPromptAndOutput(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	dir := t.TempDir()

	// Mock agent outputs status messages that include prompt text and agent output
	msgs := []protocol.StatusMessage{
		{Type: protocol.MsgStepStarted, StepName: "implement", PromptText: "the assembled prompt"},
		{Type: protocol.MsgStepCompleted, StepName: "implement", Result: "success", AgentOutput: "I wrote the code", AttemptNumber: 1},
		{Type: protocol.MsgRunCompleted, Result: "succeeded"},
	}
	script := "#!/bin/sh\n"
	for _, msg := range msgs {
		data, _ := json.Marshal(msg)
		script += "echo '" + string(data) + "'\n"
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "capture.cloche"), []byte(script), 0755))

	rt := local.NewRuntime("sh")
	srv := server.NewClocheServerWithCaptures(store, store, rt, "")

	resp, err := srv.RunWorkflow(context.Background(), &pb.RunWorkflowRequest{
		WorkflowName: "capture",
		ProjectDir:   dir,
	})
	require.NoError(t, err)

	// Poll until complete and captures are stored
	var captures []*domain.StepExecution
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		status, err := srv.GetStatus(context.Background(), &pb.GetStatusRequest{RunId: resp.RunId})
		require.NoError(t, err)
		if status.State == "succeeded" {
			captures, err = store.GetCaptures(context.Background(), resp.RunId)
			require.NoError(t, err)
			if len(captures) >= 2 {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Should have 2 captures: one for step_started, one for step_completed
	require.GreaterOrEqual(t, len(captures), 2)

	// Check captures directly - the started one has prompt_text, completed has agent_output
	var foundPrompt, foundOutput bool
	for _, c := range captures {
		if c.PromptText == "the assembled prompt" {
			foundPrompt = true
		}
		if c.AgentOutput == "I wrote the code" && c.AttemptNumber == 1 {
			foundOutput = true
		}
	}
	assert.True(t, foundPrompt, "should find capture with prompt text")
	assert.True(t, foundOutput, "should find capture with agent output and attempt number")
}

func TestServer_StreamLogs(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	dir := t.TempDir()

	// Mock agent outputs status messages
	msgs := []protocol.StatusMessage{
		{Type: protocol.MsgStepStarted, StepName: "build", PromptText: "build the thing"},
		{Type: protocol.MsgStepCompleted, StepName: "build", Result: "success", AgentOutput: "done building"},
		{Type: protocol.MsgRunCompleted, Result: "succeeded"},
	}
	script := "#!/bin/sh\n"
	for _, msg := range msgs {
		data, _ := json.Marshal(msg)
		script += "echo '" + string(data) + "'\n"
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "test.cloche"), []byte(script), 0755))

	rt := local.NewRuntime("sh")
	srv := server.NewClocheServerWithCaptures(store, store, rt, "")

	resp, err := srv.RunWorkflow(context.Background(), &pb.RunWorkflowRequest{
		WorkflowName: "test",
		ProjectDir:   dir,
	})
	require.NoError(t, err)

	// Poll until the run completes and captures are stored
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		status, err := srv.GetStatus(context.Background(), &pb.GetStatusRequest{RunId: resp.RunId})
		require.NoError(t, err)
		if status.State == "succeeded" {
			captures, _ := store.GetCaptures(context.Background(), resp.RunId)
			if len(captures) >= 2 {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Use a mock stream to collect LogEntry messages
	mock := &mockLogStream{ctx: context.Background()}
	err = srv.StreamLogs(&pb.StreamLogsRequest{RunId: resp.RunId}, mock)
	require.NoError(t, err)

	// Should have entries: step_started, step_completed, run_completed
	require.GreaterOrEqual(t, len(mock.entries), 3)

	// Find step_started and step_completed entries
	var foundStarted, foundCompleted, foundRun bool
	for _, e := range mock.entries {
		switch e.Type {
		case "step_started":
			if e.StepName == "build" {
				foundStarted = true
				assert.Equal(t, "build the thing", e.Message)
			}
		case "step_completed":
			if e.StepName == "build" {
				foundCompleted = true
				assert.Equal(t, "success", e.Result)
				assert.Equal(t, "done building", e.Message)
			}
		case "run_completed":
			foundRun = true
			assert.Equal(t, "succeeded", e.Result)
		}
	}
	assert.True(t, foundStarted, "should find step_started for build")
	assert.True(t, foundCompleted, "should find step_completed for build")
	assert.True(t, foundRun, "should find run_completed")
}

// mockLogStream implements grpc.ServerStreamingServer[pb.LogEntry] for testing.
type mockLogStream struct {
	grpclib.ServerStream
	ctx     context.Context
	entries []*pb.LogEntry
}

func (m *mockLogStream) Send(entry *pb.LogEntry) error {
	m.entries = append(m.entries, entry)
	return nil
}

func (m *mockLogStream) Context() context.Context {
	return m.ctx
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
