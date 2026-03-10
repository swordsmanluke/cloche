package grpc_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/cloche-dev/cloche/api/clochepb"
	server "github.com/cloche-dev/cloche/internal/adapters/grpc"
	"github.com/cloche-dev/cloche/internal/adapters/local"
	"github.com/cloche-dev/cloche/internal/adapters/sqlite"
	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/logstream"
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
	resp, err := srv.ListRuns(context.Background(), &pb.ListRunsRequest{All: true})
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
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".cloche"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".cloche", "test.cloche"), []byte(script), 0755))

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

func TestServer_RunWorkflow_WithTitle(t *testing.T) {
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
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".cloche", "test.cloche"), []byte(script), 0755))

	rt := local.NewRuntime("sh")
	srv := server.NewClocheServerWithCaptures(store, store, rt, "")

	resp, err := srv.RunWorkflow(context.Background(), &pb.RunWorkflowRequest{
		WorkflowName: "test",
		ProjectDir:   dir,
		Prompt:       "do something",
		Title:        "Add dark mode toggle",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, resp.RunId)

	// Poll until the background goroutine finishes
	deadline := time.Now().Add(5 * time.Second)
	var status *pb.GetStatusResponse
	for time.Now().Before(deadline) {
		status, err = srv.GetStatus(context.Background(), &pb.GetStatusRequest{RunId: resp.RunId})
		require.NoError(t, err)
		if status.State == "succeeded" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	require.NotNil(t, status)
	assert.Equal(t, "Add dark mode toggle", status.Title)
}

func TestServer_RunWorkflow_AgentGeneratesTitle(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	dir := t.TempDir()

	// Mock agent outputs a run_title message (simulating agent-generated title)
	msgs := []protocol.StatusMessage{
		{Type: protocol.MsgRunTitle, Message: "Agent generated title"},
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
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".cloche", "test.cloche"), []byte(script), 0755))

	rt := local.NewRuntime("sh")
	srv := server.NewClocheServerWithCaptures(store, store, rt, "")

	resp, err := srv.RunWorkflow(context.Background(), &pb.RunWorkflowRequest{
		WorkflowName: "test",
		ProjectDir:   dir,
	})
	require.NoError(t, err)

	// Poll until complete
	deadline := time.Now().Add(5 * time.Second)
	var status *pb.GetStatusResponse
	for time.Now().Before(deadline) {
		status, err = srv.GetStatus(context.Background(), &pb.GetStatusRequest{RunId: resp.RunId})
		require.NoError(t, err)
		if status.State == "succeeded" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	require.NotNil(t, status)
	assert.Equal(t, "Agent generated title", status.Title)
}

func TestServer_RunWorkflow_ExplicitTitleNotOverriddenByAgent(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	dir := t.TempDir()

	// Mock agent also outputs a run_title message, but explicit title should win
	msgs := []protocol.StatusMessage{
		{Type: protocol.MsgRunTitle, Message: "Agent title"},
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
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".cloche", "test.cloche"), []byte(script), 0755))

	rt := local.NewRuntime("sh")
	srv := server.NewClocheServerWithCaptures(store, store, rt, "")

	resp, err := srv.RunWorkflow(context.Background(), &pb.RunWorkflowRequest{
		WorkflowName: "test",
		ProjectDir:   dir,
		Title:        "Explicit user title",
	})
	require.NoError(t, err)

	// Poll until complete
	deadline := time.Now().Add(5 * time.Second)
	var status *pb.GetStatusResponse
	for time.Now().Before(deadline) {
		status, err = srv.GetStatus(context.Background(), &pb.GetStatusRequest{RunId: resp.RunId})
		require.NoError(t, err)
		if status.State == "succeeded" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	require.NotNil(t, status)
	assert.Equal(t, "Explicit user title", status.Title, "explicit title should not be overridden by agent")
}

func TestServer_ListRuns_IncludesTitle(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	run := domain.NewRun("title-list-1", "develop")
	run.Start()
	run.Title = "Fix the login bug"
	require.NoError(t, store.CreateRun(ctx, run))

	srv := server.NewClocheServer(store, nil)
	resp, err := srv.ListRuns(ctx, &pb.ListRunsRequest{All: true})
	require.NoError(t, err)
	require.Len(t, resp.Runs, 1)
	assert.Equal(t, "Fix the login bug", resp.Runs[0].Title)
}

func TestServer_RunWorkflow_CapturesStepMetadata(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	dir := t.TempDir()

	// Mock agent outputs status messages
	msgs := []protocol.StatusMessage{
		{Type: protocol.MsgStepStarted, StepName: "implement"},
		{Type: protocol.MsgStepCompleted, StepName: "implement", Result: "success"},
		{Type: protocol.MsgRunCompleted, Result: "succeeded"},
	}
	script := "#!/bin/sh\n"
	for _, msg := range msgs {
		data, _ := json.Marshal(msg)
		script += "echo '" + string(data) + "'\n"
	}
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".cloche"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".cloche", "capture.cloche"), []byte(script), 0755))

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

	// Check captures directly - one for step_started, one for step_completed
	var foundStarted, foundCompleted bool
	for _, c := range captures {
		if c.StepName == "implement" && c.Result == "" {
			foundStarted = true
		}
		if c.StepName == "implement" && c.Result == "success" {
			foundCompleted = true
		}
	}
	assert.True(t, foundStarted, "should find capture for step started")
	assert.True(t, foundCompleted, "should find capture for step completed")
}

func TestServer_StreamLogs(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	dir := t.TempDir()

	// Mock agent outputs status messages
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
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".cloche", "test.cloche"), []byte(script), 0755))

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
			}
		case "step_completed":
			if e.StepName == "build" {
				foundCompleted = true
				assert.Equal(t, "success", e.Result)
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

func TestServer_StreamLogs_FallsBackToContainerLog(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	dir := t.TempDir()

	// Mock agent outputs a step that fails — no per-step output file will exist
	msgs := []protocol.StatusMessage{
		{Type: protocol.MsgStepStarted, StepName: "implement"},
		{Type: protocol.MsgStepCompleted, StepName: "implement", Result: "fail"},
		{Type: protocol.MsgRunCompleted, Result: "failed"},
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

	// Poll until run completes
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		status, err := srv.GetStatus(context.Background(), &pb.GetStatusRequest{RunId: resp.RunId})
		require.NoError(t, err)
		if status.State != "running" && status.State != "pending" {
			captures, _ := store.GetCaptures(context.Background(), resp.RunId)
			if len(captures) >= 2 {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Simulate container.log being written (local runtime Logs() returns empty,
	// so write it manually to test the StreamLogs fallback path)
	outputDir := filepath.Join(dir, ".cloche", resp.RunId, "output")
	require.NoError(t, os.MkdirAll(outputDir, 0755))
	require.NoError(t, os.WriteFile(
		filepath.Join(outputDir, "container.log"),
		[]byte("error: compilation failed\ndetailed stack trace here"),
		0644,
	))

	// Stream logs and verify the step_completed entry falls back to container.log
	mock := &mockLogStream{ctx: context.Background()}
	err = srv.StreamLogs(&pb.StreamLogsRequest{RunId: resp.RunId}, mock)
	require.NoError(t, err)

	var foundCompleted bool
	for _, e := range mock.entries {
		if e.Type == "step_completed" && e.StepName == "implement" {
			foundCompleted = true
			assert.Equal(t, "fail", e.Result)
			assert.Contains(t, e.Message, "error: compilation failed")
			assert.Contains(t, e.Message, "detailed stack trace here")
		}
	}
	assert.True(t, foundCompleted, "should find step_completed with container.log fallback content")
}

func TestServer_StreamLogs_PrefersStepOutput(t *testing.T) {
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
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".cloche", "test.cloche"), []byte(script), 0755))

	rt := local.NewRuntime("sh")
	srv := server.NewClocheServerWithCaptures(store, store, rt, "")

	resp, err := srv.RunWorkflow(context.Background(), &pb.RunWorkflowRequest{
		WorkflowName: "test",
		ProjectDir:   dir,
	})
	require.NoError(t, err)

	// Poll until complete
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

	// Write both per-step output AND container.log — per-step should win
	outputDir := filepath.Join(dir, ".cloche", resp.RunId, "output")
	require.NoError(t, os.MkdirAll(outputDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(outputDir, "build.log"), []byte("step-specific output"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(outputDir, "container.log"), []byte("full container output"), 0644))

	mock := &mockLogStream{ctx: context.Background()}
	err = srv.StreamLogs(&pb.StreamLogsRequest{RunId: resp.RunId}, mock)
	require.NoError(t, err)

	var foundCompleted bool
	for _, e := range mock.entries {
		if e.Type == "step_completed" && e.StepName == "build" {
			foundCompleted = true
			assert.Equal(t, "step-specific output", e.Message, "should prefer per-step output over container.log")
		}
	}
	assert.True(t, foundCompleted, "should find step_completed entry")
}

func TestServer_GetStatus_ContainerID(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	run := domain.NewRun("cid-test", "develop")
	run.Start()
	run.ContainerID = "4647e7e70e3fabc123def456"
	require.NoError(t, store.CreateRun(ctx, run))

	srv := server.NewClocheServer(store, nil)
	resp, err := srv.GetStatus(ctx, &pb.GetStatusRequest{RunId: "cid-test"})
	require.NoError(t, err)
	assert.Equal(t, "4647e7e70e3fabc123def456", resp.ContainerId)
}

func TestServer_Shutdown(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	srv := server.NewClocheServer(store, nil)

	called := make(chan struct{}, 1)
	srv.SetShutdownFunc(func() { called <- struct{}{} })

	resp, err := srv.Shutdown(context.Background(), &pb.ShutdownRequest{})
	require.NoError(t, err)
	assert.NotNil(t, resp)

	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("shutdown callback was not called")
	}
}

func TestServer_Shutdown_NotConfigured(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	srv := server.NewClocheServer(store, nil)

	_, err = srv.Shutdown(context.Background(), &pb.ShutdownRequest{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "shutdown not configured")
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

// trackingRuntime wraps a local.Runtime and tracks Remove calls.
type trackingRuntime struct {
	*local.Runtime
	removeCalled atomic.Int32
}

func (tr *trackingRuntime) Remove(ctx context.Context, containerID string) error {
	tr.removeCalled.Add(1)
	return tr.Runtime.Remove(ctx, containerID)
}

// ensuringRuntime wraps a local.Runtime and implements ImageEnsurer to track calls.
type ensuringRuntime struct {
	*local.Runtime
	ensureCalled atomic.Int32
	ensureErr    error
}

func (e *ensuringRuntime) EnsureImage(ctx context.Context, projectDir, image string) error {
	e.ensureCalled.Add(1)
	return e.ensureErr
}

func TestServer_RunWorkflow_CallsEnsureImage(t *testing.T) {
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
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".cloche", "test.cloche"), []byte(script), 0755))

	rt := &ensuringRuntime{Runtime: local.NewRuntime("sh")}
	srv := server.NewClocheServerWithCaptures(store, store, rt, "test-image:latest")

	resp, err := srv.RunWorkflow(context.Background(), &pb.RunWorkflowRequest{
		WorkflowName: "test",
		ProjectDir:   dir,
	})
	require.NoError(t, err)

	// Wait for run to complete
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		status, err := srv.GetStatus(context.Background(), &pb.GetStatusRequest{RunId: resp.RunId})
		require.NoError(t, err)
		if status.State == "succeeded" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	assert.Equal(t, int32(1), rt.ensureCalled.Load(), "EnsureImage should be called once")
}

func TestServer_RunWorkflow_FailedRunKeepsContainer(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	dir := t.TempDir()

	// Mock agent outputs a failed run and exits with non-zero code
	msgs := []protocol.StatusMessage{
		{Type: protocol.MsgStepStarted, StepName: "build"},
		{Type: protocol.MsgStepCompleted, StepName: "build", Result: "fail"},
		{Type: protocol.MsgRunCompleted, Result: "failed"},
	}
	script := "#!/bin/sh\n"
	for _, msg := range msgs {
		data, _ := json.Marshal(msg)
		script += "echo '" + string(data) + "'\n"
	}
	script += "exit 1\n"
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".cloche"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".cloche", "test.cloche"), []byte(script), 0755))

	rt := &trackingRuntime{Runtime: local.NewRuntime("sh")}
	srv := server.NewClocheServerWithCaptures(store, store, rt, "")

	resp, err := srv.RunWorkflow(context.Background(), &pb.RunWorkflowRequest{
		WorkflowName:  "test",
		ProjectDir:    dir,
		KeepContainer: false, // NOT requesting keep, but should still keep on failure
	})
	require.NoError(t, err)

	// Poll until the run completes (failed state)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		status, err := srv.GetStatus(context.Background(), &pb.GetStatusRequest{RunId: resp.RunId})
		require.NoError(t, err)
		if status.State == "failed" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Wait for post-completion cleanup (container retention logic) to finish
	time.Sleep(500 * time.Millisecond)

	// Container should NOT have been removed
	assert.Equal(t, int32(0), rt.removeCalled.Load(), "Remove should not be called for failed runs")

	// ContainerKept should be true in the store
	run, err := store.GetRun(context.Background(), resp.RunId)
	require.NoError(t, err)
	assert.Equal(t, domain.RunStateFailed, run.State)
	assert.True(t, run.ContainerKept, "ContainerKept should be true for failed runs")
}

func TestServer_RunWorkflow_SucceededRunRemovesContainer(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	dir := t.TempDir()

	// Mock agent outputs a successful run
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
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".cloche", "test.cloche"), []byte(script), 0755))

	rt := &trackingRuntime{Runtime: local.NewRuntime("sh")}
	srv := server.NewClocheServerWithCaptures(store, store, rt, "")

	resp, err := srv.RunWorkflow(context.Background(), &pb.RunWorkflowRequest{
		WorkflowName:  "test",
		ProjectDir:    dir,
		KeepContainer: false,
	})
	require.NoError(t, err)

	// Poll until the run completes
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		status, err := srv.GetStatus(context.Background(), &pb.GetStatusRequest{RunId: resp.RunId})
		require.NoError(t, err)
		if status.State == "succeeded" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Wait a bit more for the post-completion cleanup to run
	time.Sleep(200 * time.Millisecond)

	// Container should have been removed
	assert.Equal(t, int32(1), rt.removeCalled.Load(), "Remove should be called for succeeded runs without --keep-container")

	// ContainerKept should be false
	run, err := store.GetRun(context.Background(), resp.RunId)
	require.NoError(t, err)
	assert.False(t, run.ContainerKept, "ContainerKept should be false for succeeded runs")
}

func TestServer_RunWorkflow_KeepContainerOnSuccess(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	dir := t.TempDir()

	// Mock agent outputs a successful run
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
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".cloche", "test.cloche"), []byte(script), 0755))

	rt := &trackingRuntime{Runtime: local.NewRuntime("sh")}
	srv := server.NewClocheServerWithCaptures(store, store, rt, "")

	resp, err := srv.RunWorkflow(context.Background(), &pb.RunWorkflowRequest{
		WorkflowName:  "test",
		ProjectDir:    dir,
		KeepContainer: true, // --keep-container flag set
	})
	require.NoError(t, err)

	// Poll until the run completes
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		status, err := srv.GetStatus(context.Background(), &pb.GetStatusRequest{RunId: resp.RunId})
		require.NoError(t, err)
		if status.State == "succeeded" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Wait a bit more for the post-completion cleanup to run
	time.Sleep(200 * time.Millisecond)

	// Container should NOT have been removed (--keep-container)
	assert.Equal(t, int32(0), rt.removeCalled.Load(), "Remove should not be called when --keep-container is set")

	// ContainerKept should be true
	run, err := store.GetRun(context.Background(), resp.RunId)
	require.NoError(t, err)
	assert.True(t, run.ContainerKept, "ContainerKept should be true with --keep-container")
}

func TestServer_DeleteContainer_ByRunID(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	// Create a run with a container ID and container_kept=true
	run := domain.NewRun("delete-test-run", "develop")
	run.Start()
	run.ContainerID = "abc123def456"
	run.ContainerKept = true
	run.Complete(domain.RunStateFailed)
	require.NoError(t, store.CreateRun(ctx, run))

	rt := &trackingRuntime{Runtime: local.NewRuntime("sh")}
	srv := server.NewClocheServer(store, rt)

	resp, err := srv.DeleteContainer(ctx, &pb.DeleteContainerRequest{Id: "delete-test-run"})
	require.NoError(t, err)
	assert.NotNil(t, resp)

	// Verify Remove was called
	assert.Equal(t, int32(1), rt.removeCalled.Load())

	// Verify container_kept was cleared
	updated, err := store.GetRun(ctx, "delete-test-run")
	require.NoError(t, err)
	assert.False(t, updated.ContainerKept)
}

func TestServer_DeleteContainer_ByContainerID(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	rt := &trackingRuntime{Runtime: local.NewRuntime("sh")}
	srv := server.NewClocheServer(store, rt)

	// Pass a raw container ID (not a run ID)
	resp, err := srv.DeleteContainer(ctx, &pb.DeleteContainerRequest{Id: "some-docker-container-id"})
	require.NoError(t, err)
	assert.NotNil(t, resp)

	// Remove should still be called with the container ID
	assert.Equal(t, int32(1), rt.removeCalled.Load())
}

func TestServer_DeleteContainer_EmptyID(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	srv := server.NewClocheServer(store, &trackingRuntime{Runtime: local.NewRuntime("sh")})
	_, err = srv.DeleteContainer(context.Background(), &pb.DeleteContainerRequest{Id: ""})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "id is required")
}

func TestServer_DeleteContainer_NoRuntime(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	srv := server.NewClocheServer(store, nil)
	_, err = srv.DeleteContainer(context.Background(), &pb.DeleteContainerRequest{Id: "some-id"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no container runtime configured")
}

func TestServer_DeleteContainer_RunWithNoContainer(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	// Create a run without a container ID
	run := domain.NewRun("no-container-run", "develop")
	run.Start()
	require.NoError(t, store.CreateRun(ctx, run))

	rt := &trackingRuntime{Runtime: local.NewRuntime("sh")}
	srv := server.NewClocheServer(store, rt)

	_, err = srv.DeleteContainer(ctx, &pb.DeleteContainerRequest{Id: "no-container-run"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no associated container")
}

func TestServer_RunWorkflow_EnsureImageFailure(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".cloche"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".cloche", "test.cloche"), []byte("#!/bin/sh\necho ok\n"), 0755))

	rt := &ensuringRuntime{
		Runtime:   local.NewRuntime("sh"),
		ensureErr: fmt.Errorf("build failed: Dockerfile syntax error"),
	}
	srv := server.NewClocheServerWithCaptures(store, store, rt, "test-image:latest")

	resp, err := srv.RunWorkflow(context.Background(), &pb.RunWorkflowRequest{
		WorkflowName: "test",
		ProjectDir:   dir,
	})
	require.NoError(t, err) // RPC returns immediately

	// Wait for the background goroutine to mark the run as failed
	deadline := time.Now().Add(5 * time.Second)
	var status *pb.GetStatusResponse
	for time.Now().Before(deadline) {
		status, err = srv.GetStatus(context.Background(), &pb.GetStatusRequest{RunId: resp.RunId})
		require.NoError(t, err)
		if status.State == "failed" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	assert.Equal(t, "failed", status.State)
	assert.Contains(t, status.ErrorMessage, "failed to ensure image")
}

func TestServer_LogIndexing(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	dir := t.TempDir()

	// Mock agent that outputs status messages and creates output files
	msgs := []protocol.StatusMessage{
		{Type: protocol.MsgStepStarted, StepName: "build"},
		{Type: protocol.MsgStepCompleted, StepName: "build", Result: "success"},
		{Type: protocol.MsgRunCompleted, Result: "succeeded"},
	}
	script := "#!/bin/sh\n"
	// Create output directory and files from within the script
	// Local runtime sets working dir to projectDir, so use relative paths
	script += "mkdir -p .cloche/output\n"
	script += "echo 'build script output' > .cloche/output/build.log\n"
	script += "echo 'build llm output' > .cloche/output/llm-build.log\n"
	script += "echo 'full unified log' > .cloche/output/full.log\n"
	for _, msg := range msgs {
		data, _ := json.Marshal(msg)
		script += "echo '" + string(data) + "'\n"
	}
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".cloche"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".cloche", "test.cloche"), []byte(script), 0755))

	rt := local.NewRuntime("sh")
	srv := server.NewClocheServerWithCaptures(store, store, rt, "")
	srv.SetLogStore(store)

	resp, err := srv.RunWorkflow(context.Background(), &pb.RunWorkflowRequest{
		WorkflowName: "test",
		ProjectDir:   dir,
	})
	require.NoError(t, err)

	// Poll until complete
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		status, err := srv.GetStatus(context.Background(), &pb.GetStatusRequest{RunId: resp.RunId})
		require.NoError(t, err)
		if status.State == "succeeded" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Wait for post-completion processing (log indexing happens after run completes)
	time.Sleep(500 * time.Millisecond)

	// Verify log files were indexed in the store
	logFiles, err := store.GetLogFiles(context.Background(), resp.RunId)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(logFiles), 3, "should have indexed at least full.log, build.log, llm-build.log")

	// Check that we can find by step name
	buildLogs, err := store.GetLogFilesByStep(context.Background(), resp.RunId, "build")
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(buildLogs), 2, "should have script and llm logs for build step")

	// Check file types
	var hasScript, hasLLM, hasFull bool
	for _, lf := range logFiles {
		switch lf.FileType {
		case "script":
			hasScript = true
		case "llm":
			hasLLM = true
		case "full":
			hasFull = true
		}
	}
	assert.True(t, hasScript, "should have script log")
	assert.True(t, hasLLM, "should have llm log")
	assert.True(t, hasFull, "should have full log")
}

func TestServer_StreamLogs_StepFilter(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	dir := t.TempDir()

	// Mock agent outputs status messages for two steps
	msgs := []protocol.StatusMessage{
		{Type: protocol.MsgStepStarted, StepName: "build"},
		{Type: protocol.MsgStepCompleted, StepName: "build", Result: "success"},
		{Type: protocol.MsgStepStarted, StepName: "test"},
		{Type: protocol.MsgStepCompleted, StepName: "test", Result: "success"},
		{Type: protocol.MsgRunCompleted, Result: "succeeded"},
	}
	script := "#!/bin/sh\n"
	script += "mkdir -p .cloche/output\n"
	script += "echo 'build step output' > .cloche/output/build.log\n"
	script += "echo 'test step output' > .cloche/output/test.log\n"
	script += "echo 'build llm conversation' > .cloche/output/llm-build.log\n"
	for _, msg := range msgs {
		data, _ := json.Marshal(msg)
		script += "echo '" + string(data) + "'\n"
	}
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".cloche"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".cloche", "test.cloche"), []byte(script), 0755))

	rt := local.NewRuntime("sh")
	srv := server.NewClocheServerWithCaptures(store, store, rt, "")
	srv.SetLogStore(store)

	resp, err := srv.RunWorkflow(context.Background(), &pb.RunWorkflowRequest{
		WorkflowName: "test",
		ProjectDir:   dir,
	})
	require.NoError(t, err)

	// Poll until complete
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		status, err := srv.GetStatus(context.Background(), &pb.GetStatusRequest{RunId: resp.RunId})
		require.NoError(t, err)
		if status.State == "succeeded" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(500 * time.Millisecond)

	// StreamLogs with step filter should return only that step's logs
	mock := &mockLogStream{ctx: context.Background()}
	err = srv.StreamLogs(&pb.StreamLogsRequest{
		RunId:    resp.RunId,
		StepName: "build",
	}, mock)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(mock.entries), 1, "should have at least one entry for build step")

	// All entries should be about the build step
	for _, e := range mock.entries {
		if e.StepName != "" {
			assert.Equal(t, "build", e.StepName, "filtered output should only contain build step")
		}
	}

	// StreamLogs with type filter
	mock2 := &mockLogStream{ctx: context.Background()}
	err = srv.StreamLogs(&pb.StreamLogsRequest{
		RunId:    resp.RunId,
		StepName: "build",
		LogType:  "llm",
	}, mock2)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(mock2.entries), 1, "should have at least one LLM log entry")

	for _, e := range mock2.entries {
		if e.Message != "" {
			assert.Contains(t, e.Message, "llm conversation", "should contain LLM output")
		}
	}
}

func TestServer_StreamLogs_FullLog(t *testing.T) {
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
	script += "mkdir -p .cloche/output\n"
	script += "echo 'unified log content here' > .cloche/output/full.log\n"
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

	// Poll until complete
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		status, err := srv.GetStatus(context.Background(), &pb.GetStatusRequest{RunId: resp.RunId})
		require.NoError(t, err)
		if status.State == "succeeded" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(500 * time.Millisecond)

	// StreamLogs with no filters should serve full.log content
	mock := &mockLogStream{ctx: context.Background()}
	err = srv.StreamLogs(&pb.StreamLogsRequest{RunId: resp.RunId}, mock)
	require.NoError(t, err)

	// Should have a full_log entry
	var foundFullLog bool
	for _, e := range mock.entries {
		if e.Type == "full_log" {
			foundFullLog = true
			assert.Contains(t, e.Message, "unified log content here")
		}
	}
	assert.True(t, foundFullLog, "should serve full.log as unified log when available")
}

func TestServer_StreamLogs_LiveStreaming(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	// Create a running run (no container needed — we test the broadcaster path directly).
	ctx := context.Background()
	run := domain.NewRun("live-test-1", "develop")
	run.ProjectDir = t.TempDir()
	run.Start()
	run.ContainerID = "fake"
	require.NoError(t, store.CreateRun(ctx, run))

	broadcaster := logstream.NewBroadcaster()
	srv := server.NewClocheServerWithCaptures(store, store, nil, "")
	srv.SetLogBroadcaster(broadcaster)

	// Use a cancellable context for the mock stream so we can abort if needed.
	streamCtx, streamCancel := context.WithTimeout(ctx, 5*time.Second)
	defer streamCancel()
	mock := &mockLogStream{ctx: streamCtx}

	// Publish log lines in a goroutine, then finish the broadcast.
	go func() {
		time.Sleep(50 * time.Millisecond)
		broadcaster.Publish("live-test-1", logstream.LogLine{
			Timestamp: "2026-03-10T10:00:00Z",
			Type:      "status",
			Content:   "step_started: implement",
			StepName:  "implement",
		})
		time.Sleep(50 * time.Millisecond)
		broadcaster.Publish("live-test-1", logstream.LogLine{
			Timestamp: "2026-03-10T10:00:01Z",
			Type:      "llm",
			Content:   "Reading the codebase...",
			StepName:  "implement",
		})
		time.Sleep(50 * time.Millisecond)
		broadcaster.Publish("live-test-1", logstream.LogLine{
			Timestamp: "2026-03-10T10:00:02Z",
			Type:      "llm",
			Content:   "Making changes now.",
			StepName:  "implement",
		})
		time.Sleep(50 * time.Millisecond)

		// Mark run as completed before finishing broadcast.
		r, _ := store.GetRun(ctx, "live-test-1")
		r.Complete(domain.RunStateSucceeded)
		_ = store.UpdateRun(ctx, r)

		broadcaster.Finish("live-test-1")
	}()

	err = srv.StreamLogs(&pb.StreamLogsRequest{RunId: "live-test-1"}, mock)
	require.NoError(t, err)

	// Should have received live log entries plus a run_completed entry.
	var logEntries []*pb.LogEntry
	var foundRunCompleted bool
	for _, e := range mock.entries {
		switch e.Type {
		case "log":
			logEntries = append(logEntries, e)
		case "run_completed":
			foundRunCompleted = true
			assert.Equal(t, "succeeded", e.Result)
		}
	}

	require.Len(t, logEntries, 3, "should receive 3 live log entries")
	assert.Equal(t, "step_started: implement", logEntries[0].Message)
	assert.Equal(t, "implement", logEntries[0].StepName)
	assert.Equal(t, "Reading the codebase...", logEntries[1].Message)
	assert.Equal(t, "Making changes now.", logEntries[2].Message)
	assert.True(t, foundRunCompleted, "should receive run_completed after broadcast finishes")
}

func TestServer_StreamLogs_LiveStreamingClientDisconnect(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	run := domain.NewRun("live-cancel-1", "develop")
	run.ProjectDir = t.TempDir()
	run.Start()
	run.ContainerID = "fake"
	require.NoError(t, store.CreateRun(ctx, run))

	broadcaster := logstream.NewBroadcaster()
	srv := server.NewClocheServerWithCaptures(store, store, nil, "")
	srv.SetLogBroadcaster(broadcaster)

	// Cancel the stream context immediately to simulate client disconnect.
	streamCtx, streamCancel := context.WithCancel(ctx)
	mock := &mockLogStream{ctx: streamCtx}

	// Ensure broadcaster has an active entry for the run.
	broadcaster.Subscribe("live-cancel-1")

	go func() {
		time.Sleep(50 * time.Millisecond)
		streamCancel()
	}()

	err = srv.StreamLogs(&pb.StreamLogsRequest{RunId: "live-cancel-1"}, mock)
	assert.ErrorIs(t, err, context.Canceled)
}

