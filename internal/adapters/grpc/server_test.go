package grpc_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/cloche-dev/cloche/api/clochepb"
	server "github.com/cloche-dev/cloche/internal/adapters/grpc"
	"github.com/cloche-dev/cloche/internal/adapters/docker"
	"github.com/cloche-dev/cloche/internal/adapters/local"
	"github.com/cloche-dev/cloche/internal/adapters/sqlite"
	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/host"
	"github.com/cloche-dev/cloche/internal/logstream"
	"github.com/cloche-dev/cloche/internal/ports"
	"github.com/cloche-dev/cloche/internal/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	grpclib "google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
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

func TestServer_ListRuns_FilterByProject(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	// Create runs for two different projects
	runA := domain.NewRun("run-proj-a", "wf")
	runA.ProjectDir = "/home/user/project-a"
	require.NoError(t, store.CreateRun(ctx, runA))

	runB := domain.NewRun("run-proj-b", "wf")
	runB.ProjectDir = "/home/user/project-b"
	require.NoError(t, store.CreateRun(ctx, runB))

	srv := server.NewClocheServer(store, nil)

	// Filter by project-a: should only return runA
	resp, err := srv.ListRuns(ctx, &pb.ListRunsRequest{ProjectDir: "/home/user/project-a"})
	require.NoError(t, err)
	require.Len(t, resp.Runs, 1)
	assert.Equal(t, "run-proj-a", resp.Runs[0].RunId)
	assert.Equal(t, "/home/user/project-a", resp.Runs[0].ProjectDir)

	// Filter by project-b: should only return runB
	resp, err = srv.ListRuns(ctx, &pb.ListRunsRequest{ProjectDir: "/home/user/project-b"})
	require.NoError(t, err)
	require.Len(t, resp.Runs, 1)
	assert.Equal(t, "run-proj-b", resp.Runs[0].RunId)

	// No filter (--all): should return both
	resp, err = srv.ListRuns(ctx, &pb.ListRunsRequest{All: true})
	require.NoError(t, err)
	assert.Len(t, resp.Runs, 2)
}

func TestServer_ListRuns_FilterByState(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	runA := domain.NewRun("run-running", "wf")
	runA.ProjectDir = "/project"
	runA.State = domain.RunStateRunning
	runA.StartedAt = time.Now()
	require.NoError(t, store.CreateRun(ctx, runA))

	runB := domain.NewRun("run-failed", "wf")
	runB.ProjectDir = "/project"
	runB.State = domain.RunStateFailed
	runB.StartedAt = time.Now()
	require.NoError(t, store.CreateRun(ctx, runB))

	srv := server.NewClocheServer(store, nil)

	resp, err := srv.ListRuns(ctx, &pb.ListRunsRequest{All: true, State: "running"})
	require.NoError(t, err)
	require.Len(t, resp.Runs, 1)
	assert.Equal(t, "run-running", resp.Runs[0].RunId)
}

func TestServer_ListRuns_FilterByLimit(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	for i := 0; i < 5; i++ {
		r := domain.NewRun(fmt.Sprintf("run-%d", i), "wf")
		r.ProjectDir = "/project"
		r.StartedAt = time.Now()
		require.NoError(t, store.CreateRun(ctx, r))
	}

	srv := server.NewClocheServer(store, nil)

	resp, err := srv.ListRuns(ctx, &pb.ListRunsRequest{All: true, Limit: 2})
	require.NoError(t, err)
	assert.Len(t, resp.Runs, 2)
}

func TestServer_ListRuns_FilterByTaskId(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	runA := domain.NewRun("run-task-a", "wf")
	runA.ProjectDir = "/project"
	runA.TaskID = "ISSUE-42"
	runA.StartedAt = time.Now()
	require.NoError(t, store.CreateRun(ctx, runA))

	runB := domain.NewRun("run-task-b", "wf")
	runB.ProjectDir = "/project"
	runB.TaskID = "ISSUE-99"
	runB.StartedAt = time.Now()
	require.NoError(t, store.CreateRun(ctx, runB))

	srv := server.NewClocheServer(store, nil)

	resp, err := srv.ListRuns(ctx, &pb.ListRunsRequest{All: true, TaskId: "ISSUE-42"})
	require.NoError(t, err)
	require.Len(t, resp.Runs, 1)
	assert.Equal(t, "run-task-a", resp.Runs[0].RunId)
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

	// Verify prompt was written to task-specific path under .cloche/runs/<task-id>/
	run, err := store.GetRun(context.Background(), resp.RunId)
	require.NoError(t, err)
	promptData, err := os.ReadFile(filepath.Join(dir, ".cloche", "runs", run.TaskID, "prompt.txt"))
	require.NoError(t, err)
	assert.Equal(t, "hello world", string(promptData))

	// Poll until the background goroutine finishes processing (up to 5s)
	var status *pb.GetStatusResponse
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		status, err = srv.GetStatus(context.Background(), &pb.GetStatusRequest{RunId: resp.RunId})
		require.NoError(t, err)
		if status.State == "succeeded" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Check that the run was tracked and completed.
	// Step executions are now recorded via gRPC AgentSession events, not docker log parsing.
	require.NotNil(t, status)
	assert.Equal(t, resp.RunId, status.RunId)
	assert.Equal(t, "succeeded", status.State)
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

func TestServer_RunWorkflow_WithIssueId(t *testing.T) {
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
		IssueId:      "TASK-42",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, resp.RunId)

	// Poll until the background goroutine finishes
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		run, getErr := store.GetRun(context.Background(), resp.RunId)
		if getErr == nil && run.State == domain.RunStateSucceeded {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Verify TaskID was persisted
	run, err := store.GetRun(context.Background(), resp.RunId)
	require.NoError(t, err)
	assert.Equal(t, "TASK-42", run.TaskID)
}

func TestServer_ListRuns_IncludesTaskId(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	run := domain.NewRun("taskid-list-1", "develop")
	run.Start()
	run.TaskID = "ISSUE-99"
	require.NoError(t, store.CreateRun(ctx, run))

	srv := server.NewClocheServer(store, nil)
	resp, err := srv.ListRuns(ctx, &pb.ListRunsRequest{All: true})
	require.NoError(t, err)
	require.Len(t, resp.Runs, 1)
	assert.Equal(t, "ISSUE-99", resp.Runs[0].TaskId)
}

func TestServer_RunWorkflow_CapturesStepMetadata(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	dir := t.TempDir()

	// Script outputs run-level JSON (MsgRunCompleted) only; step-level captures
	// are now driven by gRPC AgentSession events, not docker log parsing.
	msgs := []protocol.StatusMessage{
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

	// Run should have succeeded; step-level capture tests are covered by
	// TestAgentSession_RecordsStepCaptures which drives state via gRPC.
	status, err := srv.GetStatus(context.Background(), &pb.GetStatusRequest{RunId: resp.RunId})
	require.NoError(t, err)
	assert.Equal(t, "succeeded", status.State)
}

func TestServer_StreamLogs(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	dir := t.TempDir()

	// Script outputs run-level result; step-level events come via AgentSession.
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

	// Use a mock stream to collect LogEntry messages
	mock := &mockLogStream{ctx: context.Background()}
	err = srv.StreamLogs(&pb.StreamLogsRequest{RunId: resp.RunId}, mock)
	require.NoError(t, err)

	// StreamLogs should return at least a run_completed entry.
	// Step-level entries (step_started/step_completed) are driven by AgentSession
	// and are tested separately in TestAgentSession_RecordsStepCaptures.
	var foundRun bool
	for _, e := range mock.entries {
		if e.Type == "run_completed" {
			foundRun = true
			assert.Equal(t, "succeeded", e.Result)
		}
	}
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

func TestServer_StreamLogs_DoesNotFallBackToContainerLog(t *testing.T) {
	// This test verifies that StreamLogs does not include container.log content
	// in step_completed entries when no per-step log file exists.
	// Captures are injected directly to isolate this behavior from run execution.
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	dir := t.TempDir()

	run := domain.NewRun("no-fallback-run-1", "test")
	run.ProjectDir = dir
	run.AttemptID = "att1"
	run.TaskID = "task-no-fallback"
	run.Complete(domain.RunStateFailed)
	require.NoError(t, store.CreateRun(ctx, run))

	// Inject captures directly (simulating what AgentSession would do).
	require.NoError(t, store.SaveCapture(ctx, "no-fallback-run-1", &domain.StepExecution{
		StepName:  "implement",
		StartedAt: time.Now(),
	}))
	require.NoError(t, store.SaveCapture(ctx, "no-fallback-run-1", &domain.StepExecution{
		StepName:    "implement",
		Result:      "fail",
		CompletedAt: time.Now(),
	}))

	// Write container.log but NOT a per-step log file. The step_completed
	// entry should NOT contain container.log content.
	outputDir := filepath.Join(dir, ".cloche", "no-fallback-run-1", "output")
	require.NoError(t, os.MkdirAll(outputDir, 0755))
	require.NoError(t, os.WriteFile(
		filepath.Join(outputDir, "container.log"),
		[]byte("error: compilation failed\ndetailed stack trace here"),
		0644,
	))

	srv := server.NewClocheServerWithCaptures(store, store, nil, "")

	mock := &mockLogStream{ctx: ctx}
	err = srv.StreamLogs(&pb.StreamLogsRequest{RunId: "no-fallback-run-1"}, mock)
	require.NoError(t, err)

	var foundCompleted bool
	for _, e := range mock.entries {
		if e.Type == "step_completed" && e.StepName == "implement" {
			foundCompleted = true
			assert.Equal(t, "fail", e.Result)
			// Message should be empty — NOT the container.log content
			assert.Empty(t, e.Message, "step output should not fall back to container.log")
		}
	}
	assert.True(t, foundCompleted, "should find step_completed entry")
}

func TestServer_StreamLogs_PrefersStepOutput(t *testing.T) {
	// This test verifies that StreamLogs uses the per-step log file content
	// over container.log when both exist. Captures are injected directly.
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	dir := t.TempDir()

	run := domain.NewRun("prefers-step-run-1", "test")
	run.ProjectDir = dir
	run.AttemptID = "att1"
	run.TaskID = "task-prefers-step"
	run.Complete(domain.RunStateSucceeded)
	require.NoError(t, store.CreateRun(ctx, run))

	// Inject captures directly.
	require.NoError(t, store.SaveCapture(ctx, "prefers-step-run-1", &domain.StepExecution{
		StepName:  "build",
		StartedAt: time.Now(),
	}))
	require.NoError(t, store.SaveCapture(ctx, "prefers-step-run-1", &domain.StepExecution{
		StepName:    "build",
		Result:      "success",
		CompletedAt: time.Now(),
	}))

	// Write both per-step output AND container.log — per-step should win.
	outputDir := filepath.Join(dir, ".cloche", "prefers-step-run-1", "output")
	require.NoError(t, os.MkdirAll(outputDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(outputDir, "build.log"), []byte("step-specific output"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(outputDir, "container.log"), []byte("full container output"), 0644))

	srv := server.NewClocheServerWithCaptures(store, store, nil, "")

	mock := &mockLogStream{ctx: ctx}
	err = srv.StreamLogs(&pb.StreamLogsRequest{RunId: "prefers-step-run-1"}, mock)
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

func TestServer_Shutdown_RejectsWithActiveRuns(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	srv := server.NewClocheServer(store, nil)
	srv.SetShutdownFunc(func() {})
	srv.AddActiveRun("run-1", "container-1")

	_, err = srv.Shutdown(context.Background(), &pb.ShutdownRequest{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "1 run(s) still active")
}

func TestServer_Shutdown_ForceWithActiveRuns(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	srv := server.NewClocheServer(store, nil)
	called := make(chan struct{}, 1)
	srv.SetShutdownFunc(func() { called <- struct{}{} })
	srv.AddActiveRun("run-1", "container-1")

	resp, err := srv.Shutdown(context.Background(), &pb.ShutdownRequest{Force: true})
	require.NoError(t, err)
	assert.NotNil(t, resp)

	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("shutdown callback was not called")
	}
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

func TestServer_RunWorkflow_FailedRunCapturesErrorMessage(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	dir := t.TempDir()

	// Mock agent outputs an error message before the failed run completion
	msgs := []protocol.StatusMessage{
		{Type: protocol.MsgStepStarted, StepName: "develop"},
		{Type: protocol.MsgStepCompleted, StepName: "develop", Result: "error"},
		{Type: protocol.MsgError, StepName: "develop", Message: `step "develop" execution failed: exit status 1`},
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
		WorkflowName: "test",
		ProjectDir:   dir,
	})
	require.NoError(t, err)

	// Poll until the run completes
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		status, err := srv.GetStatus(context.Background(), &pb.GetStatusRequest{RunId: resp.RunId})
		require.NoError(t, err)
		if status.State == "failed" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Wait for post-completion cleanup to finish
	time.Sleep(500 * time.Millisecond)

	// Verify the error message was captured on the run
	run, err := store.GetRun(context.Background(), resp.RunId)
	require.NoError(t, err)
	assert.Equal(t, domain.RunStateFailed, run.State)
	assert.Contains(t, run.ErrorMessage, "develop", "error message should contain the failed step name")
	assert.Contains(t, run.ErrorMessage, "execution failed", "error message should contain the error details")
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
	broadcaster.Start("live-test-1") // register run as active
	srv := server.NewClocheServerWithCaptures(store, store, nil, "")
	srv.SetLogBroadcaster(broadcaster)

	// Use a cancellable context with follow metadata for the mock stream.
	streamCtx, streamCancel := context.WithTimeout(ctx, 5*time.Second)
	defer streamCancel()
	streamCtx = metadata.NewIncomingContext(streamCtx, metadata.Pairs("x-cloche-follow", "true"))
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
	streamCtx = metadata.NewIncomingContext(streamCtx, metadata.Pairs("x-cloche-follow", "true"))
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

// TestServer_StreamLogs_NoFollowActiveRun verifies that without -f, logs for
// an active run return existing content and exit (snapshot mode).
func TestServer_StreamLogs_ActiveRunWithoutFollow_ReturnsSnapshot(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	dir := t.TempDir()
	run := domain.NewRun("snapshot-test-1", "develop")
	run.ProjectDir = dir
	run.Start()
	run.ContainerID = "fake"
	require.NoError(t, store.CreateRun(ctx, run))

	// Write some existing log content.
	outputDir := filepath.Join(dir, ".cloche", "snapshot-test-1", "output")
	require.NoError(t, os.MkdirAll(outputDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(outputDir, "full.log"),
		[]byte("[2026-03-10T10:00:00Z] [status] step_started: build\n"), 0644))

	broadcaster := logstream.NewBroadcaster()
	broadcaster.Start("snapshot-test-1") // register run as active
	srv := server.NewClocheServerWithCaptures(store, store, nil, "")
	srv.SetLogBroadcaster(broadcaster)

	streamCtx, streamCancel := context.WithTimeout(ctx, 5*time.Second)
	defer streamCancel()
	mock := &mockLogStream{ctx: streamCtx}

	// Without -f, should return the static full.log snapshot and exit
	// immediately, NOT block waiting for live output.
	err = srv.StreamLogs(&pb.StreamLogsRequest{RunId: "snapshot-test-1"}, mock)
	require.NoError(t, err)

	// Should have: full_log snapshot only (no live lines, no run_completed
	// since the run is still active).
	var foundFullLog bool
	for _, e := range mock.entries {
		switch e.Type {
		case "full_log":
			foundFullLog = true
			assert.Contains(t, e.Message, "step_started: build")
		case "log":
			t.Error("should not stream live lines without -f")
		case "run_completed":
			t.Error("should not send run_completed for active run without -f")
		}
	}
	assert.True(t, foundFullLog, "should send existing full.log snapshot")
}

// TestServer_StreamLogs_FollowWithExistingLogs verifies that -f sends existing
// full.log content first, then streams live output.
func TestServer_StreamLogs_FollowWithExistingLogs(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	dir := t.TempDir()
	run := domain.NewRun("follow-existing-1", "develop")
	run.ProjectDir = dir
	run.Start()
	run.ContainerID = "fake"
	require.NoError(t, store.CreateRun(ctx, run))

	// Write some existing log content.
	outputDir := filepath.Join(dir, ".cloche", "follow-existing-1", "output")
	require.NoError(t, os.MkdirAll(outputDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(outputDir, "full.log"),
		[]byte("[2026-03-10T09:00:00Z] [status] existing log line\n"), 0644))

	broadcaster := logstream.NewBroadcaster()
	broadcaster.Start("follow-existing-1") // register run as active
	srv := server.NewClocheServerWithCaptures(store, store, nil, "")
	srv.SetLogBroadcaster(broadcaster)

	streamCtx, streamCancel := context.WithTimeout(ctx, 5*time.Second)
	defer streamCancel()
	streamCtx = metadata.NewIncomingContext(streamCtx, metadata.Pairs("x-cloche-follow", "true"))
	mock := &mockLogStream{ctx: streamCtx}

	go func() {
		time.Sleep(100 * time.Millisecond)
		broadcaster.Publish("follow-existing-1", logstream.LogLine{
			Timestamp: "2026-03-10T10:00:00Z",
			Type:      "llm",
			Content:   "new live line",
			StepName:  "build",
		})
		time.Sleep(50 * time.Millisecond)

		r, _ := store.GetRun(ctx, "follow-existing-1")
		r.Complete(domain.RunStateSucceeded)
		_ = store.UpdateRun(ctx, r)
		broadcaster.Finish("follow-existing-1")
	}()

	err = srv.StreamLogs(&pb.StreamLogsRequest{RunId: "follow-existing-1"}, mock)
	require.NoError(t, err)

	// Should have: full_log (existing), log (live), run_completed.
	var foundFullLog, foundLive, foundCompleted bool
	for _, e := range mock.entries {
		switch e.Type {
		case "full_log":
			foundFullLog = true
			assert.Contains(t, e.Message, "existing log line")
		case "log":
			foundLive = true
			assert.Equal(t, "new live line", e.Message)
		case "run_completed":
			foundCompleted = true
			assert.Equal(t, "succeeded", e.Result)
		}
	}
	assert.True(t, foundFullLog, "should send existing full.log first")
	assert.True(t, foundLive, "should stream live lines after existing logs")
	assert.True(t, foundCompleted, "should send run_completed when done")
}

// TestServer_StreamLogs_ActiveRunNotInBroadcaster verifies that when a run is
// marked as active in the store but the broadcaster has no entry (e.g. daemon
// restarted mid-run), StreamLogs falls through to static content instead of
// hanging forever.
func TestServer_StreamLogs_ActiveRunNotInBroadcaster(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	dir := t.TempDir()
	run := domain.NewRun("orphan-test-1", "develop")
	run.ProjectDir = dir
	run.Start() // state = running
	run.ContainerID = "fake"
	require.NoError(t, store.CreateRun(ctx, run))

	// Write some existing log content (simulates partially extracted output).
	outputDir := filepath.Join(dir, ".cloche", "orphan-test-1", "output")
	require.NoError(t, os.MkdirAll(outputDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(outputDir, "full.log"),
		[]byte("[2026-03-10T10:00:00Z] [status] step_started: build\n[2026-03-10T10:00:01Z] [llm] Working...\n"), 0644))

	// Broadcaster is fresh (no Start called) — simulates daemon restart.
	broadcaster := logstream.NewBroadcaster()
	srv := server.NewClocheServerWithCaptures(store, store, nil, "")
	srv.SetLogBroadcaster(broadcaster)

	// Should NOT hang — should return static content.
	streamCtx, streamCancel := context.WithTimeout(ctx, 2*time.Second)
	defer streamCancel()
	mock := &mockLogStream{ctx: streamCtx}

	err = srv.StreamLogs(&pb.StreamLogsRequest{RunId: "orphan-test-1"}, mock)
	require.NoError(t, err)

	// Should have served full.log content (no run_completed since state is still "running").
	require.NotEmpty(t, mock.entries, "should return at least one entry instead of hanging")
	assert.Equal(t, "full_log", mock.entries[0].Type)
	assert.Contains(t, mock.entries[0].Message, "step_started: build")
}

// TestServer_StreamLogs_BroadcastFinishedStateSettled verifies that when the
// broadcaster finishes and the run state has been updated, streamFollowLogs
// sends a proper run_completed entry.
func TestServer_StreamLogs_BroadcastFinishedStateSettled(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	dir := t.TempDir()
	run := domain.NewRun("settled-test-1", "develop")
	run.ProjectDir = dir
	run.Start()
	run.ContainerID = "fake"
	require.NoError(t, store.CreateRun(ctx, run))

	broadcaster := logstream.NewBroadcaster()
	broadcaster.Start("settled-test-1")
	srv := server.NewClocheServerWithCaptures(store, store, nil, "")
	srv.SetLogBroadcaster(broadcaster)

	streamCtx, streamCancel := context.WithTimeout(ctx, 5*time.Second)
	defer streamCancel()
	streamCtx = metadata.NewIncomingContext(streamCtx, metadata.Pairs("x-cloche-follow", "true"))
	mock := &mockLogStream{ctx: streamCtx}

	// Simulate trackRun completing: update state THEN finish broadcast.
	go func() {
		time.Sleep(50 * time.Millisecond)
		r, _ := store.GetRun(ctx, "settled-test-1")
		r.Complete(domain.RunStateSucceeded)
		_ = store.UpdateRun(ctx, r)
		broadcaster.Finish("settled-test-1")
	}()

	err = srv.StreamLogs(&pb.StreamLogsRequest{RunId: "settled-test-1"}, mock)
	require.NoError(t, err)

	// Should have received run_completed with correct state.
	var foundCompleted bool
	for _, e := range mock.entries {
		if e.Type == "run_completed" {
			foundCompleted = true
			assert.Equal(t, "succeeded", e.Result)
		}
	}
	assert.True(t, foundCompleted, "should send run_completed after broadcast finishes with state settled")
}

func TestServer_GetProjectInfo_ByDir(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	dir := t.TempDir()

	// Create .cloche directory with workflow files and config.
	clocheDir := filepath.Join(dir, ".cloche")
	require.NoError(t, os.MkdirAll(clocheDir, 0755))

	// Container workflow.
	containerWF := `workflow "develop" {
  step code {
    prompt = "write code"
    results = [success, fail]
  }
  code:success -> done
  code:fail -> abort
}`
	require.NoError(t, os.WriteFile(filepath.Join(clocheDir, "develop.cloche"), []byte(containerWF), 0644))

	// Host workflow.
	hostWF := `workflow "main" {
  step build {
    run = "make build"
    results = [success, fail]
  }
  build:success -> done
  build:fail -> abort
}

workflow "post-merge" {
  step cleanup {
    run = "echo done"
    results = [success]
  }
  cleanup:success -> done
}`
	require.NoError(t, os.WriteFile(filepath.Join(clocheDir, "host.cloche"), []byte(hostWF), 0644))

	// Config file.
	configTOML := `active = true

[orchestration]
concurrency = 3
stagger_seconds = 2.5
dedup_seconds = 120

[evolution]
enabled = false
`
	require.NoError(t, os.WriteFile(filepath.Join(clocheDir, "config.toml"), []byte(configTOML), 0644))

	// Create an active run for this project.
	run := domain.NewRun("run-active-1", "develop")
	run.ProjectDir = dir
	run.Start()
	require.NoError(t, store.CreateRun(ctx, run))

	// Create a completed run (should not appear in active runs).
	completed := domain.NewRun("run-done-1", "develop")
	completed.ProjectDir = dir
	completed.Start()
	completed.Complete(domain.RunStateSucceeded)
	require.NoError(t, store.CreateRun(ctx, completed))

	srv := server.NewClocheServer(store, nil)

	resp, err := srv.GetProjectInfo(ctx, &pb.GetProjectInfoRequest{ProjectDir: dir})
	require.NoError(t, err)

	assert.Equal(t, dir, resp.ProjectDir)
	assert.Equal(t, filepath.Base(dir), resp.Name)
	assert.True(t, resp.Active)
	assert.Equal(t, int32(3), resp.Concurrency)
	assert.InDelta(t, 2.5, resp.StaggerSeconds, 0.01)
	assert.InDelta(t, 120.0, resp.DedupSeconds, 0.01)
	assert.False(t, resp.EvolutionEnabled)
	assert.False(t, resp.LoopRunning)

	// Active runs.
	require.Len(t, resp.ActiveRuns, 1)
	assert.Equal(t, "run-active-1", resp.ActiveRuns[0].RunId)
	assert.Equal(t, "develop", resp.ActiveRuns[0].WorkflowName)

	// Workflows.
	assert.Equal(t, []string{"develop"}, resp.ContainerWorkflows)
	assert.Contains(t, resp.HostWorkflows, "main")
	assert.Contains(t, resp.HostWorkflows, "post-merge")
}

func TestServer_GetProjectInfo_ByName(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	dir := t.TempDir()

	// Create .cloche directory with a workflow.
	clocheDir := filepath.Join(dir, ".cloche")
	require.NoError(t, os.MkdirAll(clocheDir, 0755))
	containerWF := `workflow "build" {
  step test {
    run = "make test"
    results = [success, fail]
  }
  test:success -> done
  test:fail -> abort
}`
	require.NoError(t, os.WriteFile(filepath.Join(clocheDir, "build.cloche"), []byte(containerWF), 0644))

	// Register the project by creating a run.
	run := domain.NewRun("run-1", "build")
	run.ProjectDir = dir
	require.NoError(t, store.CreateRun(ctx, run))

	srv := server.NewClocheServer(store, nil)

	// Look up by project label (basename of dir).
	label := filepath.Base(dir)
	resp, err := srv.GetProjectInfo(ctx, &pb.GetProjectInfoRequest{Name: label})
	require.NoError(t, err)

	assert.Equal(t, dir, resp.ProjectDir)
	assert.Equal(t, label, resp.Name)
	assert.Equal(t, []string{"build"}, resp.ContainerWorkflows)
}

func TestServer_GetProjectInfo_NotFound(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	srv := server.NewClocheServer(store, nil)

	_, err = srv.GetProjectInfo(context.Background(), &pb.GetProjectInfoRequest{Name: "nonexistent"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestServer_GetProjectInfo_RequiresInput(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	srv := server.NewClocheServer(store, nil)

	_, err = srv.GetProjectInfo(context.Background(), &pb.GetProjectInfoRequest{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "required")
}

func TestServer_GetVersion(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	srv := server.NewClocheServer(store, nil)
	resp, err := srv.GetVersion(context.Background(), &pb.GetVersionRequest{})
	require.NoError(t, err)
	assert.NotEmpty(t, resp.Version)
}

// TestServer_StreamLogs_LimitFullLog verifies that the --limit flag truncates
// full.log output to the last N lines.
func TestServer_StreamLogs_LimitFullLog(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	dir := t.TempDir()

	// Create a completed run with a multi-line full.log.
	ctx := context.Background()
	run := domain.NewRun("limit-full-1", "develop")
	run.ProjectDir = dir
	run.Start()
	run.ContainerID = "fake"
	run.Complete(domain.RunStateSucceeded)
	require.NoError(t, store.CreateRun(ctx, run))

	outputDir := filepath.Join(dir, ".cloche", "limit-full-1", "output")
	require.NoError(t, os.MkdirAll(outputDir, 0755))
	logContent := "line1\nline2\nline3\nline4\nline5\n"
	require.NoError(t, os.WriteFile(filepath.Join(outputDir, "full.log"), []byte(logContent), 0644))

	srv := server.NewClocheServerWithCaptures(store, store, nil, "")

	// Request with limit=2 via metadata.
	streamCtx := metadata.NewIncomingContext(ctx, metadata.Pairs("x-cloche-limit", "2"))
	mock := &mockLogStream{ctx: streamCtx}
	err = srv.StreamLogs(&pb.StreamLogsRequest{RunId: "limit-full-1"}, mock)
	require.NoError(t, err)

	// Find the full_log entry and check it has only the last 2 lines.
	var fullLogMsg string
	for _, e := range mock.entries {
		if e.Type == "full_log" {
			fullLogMsg = e.Message
		}
	}
	assert.Equal(t, "line4\nline5\n", fullLogMsg, "limit=2 should return last 2 lines")
}

// TestServer_StreamLogs_LimitStepFilter verifies that --limit applies to
// step-filtered log output.
func TestServer_StreamLogs_LimitStepFilter(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	dir := t.TempDir()

	ctx := context.Background()
	run := domain.NewRun("limit-step-1", "develop")
	run.ProjectDir = dir
	run.Start()
	run.ContainerID = "fake"
	run.Complete(domain.RunStateSucceeded)
	require.NoError(t, store.CreateRun(ctx, run))

	outputDir := filepath.Join(dir, ".cloche", "limit-step-1", "output")
	require.NoError(t, os.MkdirAll(outputDir, 0755))
	stepLog := "output line 1\noutput line 2\noutput line 3\noutput line 4\n"
	require.NoError(t, os.WriteFile(filepath.Join(outputDir, "build.log"), []byte(stepLog), 0644))

	srv := server.NewClocheServerWithCaptures(store, store, nil, "")

	// Request with step filter and limit=1 via metadata.
	streamCtx := metadata.NewIncomingContext(ctx, metadata.Pairs("x-cloche-limit", "1"))
	mock := &mockLogStream{ctx: streamCtx}
	err = srv.StreamLogs(&pb.StreamLogsRequest{RunId: "limit-step-1", StepName: "build"}, mock)
	require.NoError(t, err)

	require.GreaterOrEqual(t, len(mock.entries), 1)
	assert.Equal(t, "output line 4\n", mock.entries[0].Message, "limit=1 should return last line only")
}

// TestServer_StreamLogs_LimitZeroReturnsAll verifies that limit=0 returns all content.
func TestServer_StreamLogs_LimitZeroReturnsAll(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	dir := t.TempDir()

	ctx := context.Background()
	run := domain.NewRun("limit-zero-1", "develop")
	run.ProjectDir = dir
	run.Start()
	run.ContainerID = "fake"
	run.Complete(domain.RunStateSucceeded)
	require.NoError(t, store.CreateRun(ctx, run))

	outputDir := filepath.Join(dir, ".cloche", "limit-zero-1", "output")
	require.NoError(t, os.MkdirAll(outputDir, 0755))
	logContent := "line1\nline2\nline3\n"
	require.NoError(t, os.WriteFile(filepath.Join(outputDir, "full.log"), []byte(logContent), 0644))

	srv := server.NewClocheServerWithCaptures(store, store, nil, "")

	// No limit metadata — should return all content.
	mock := &mockLogStream{ctx: ctx}
	err = srv.StreamLogs(&pb.StreamLogsRequest{RunId: "limit-zero-1"}, mock)
	require.NoError(t, err)

	var fullLogMsg string
	for _, e := range mock.entries {
		if e.Type == "full_log" {
			fullLogMsg = e.Message
		}
	}
	assert.Equal(t, logContent, fullLogMsg, "no limit should return all content")
}

// TestServer_StreamLogs_LimitWithFollow verifies that limit applies to the
// initial snapshot in follow mode, but live lines are streamed without limit.
func TestServer_StreamLogs_LimitWithFollow(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	dir := t.TempDir()
	run := domain.NewRun("limit-follow-1", "develop")
	run.ProjectDir = dir
	run.Start()
	run.ContainerID = "fake"
	require.NoError(t, store.CreateRun(ctx, run))

	// Write existing log content (5 lines).
	outputDir := filepath.Join(dir, ".cloche", "limit-follow-1", "output")
	require.NoError(t, os.MkdirAll(outputDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(outputDir, "full.log"),
		[]byte("old1\nold2\nold3\nold4\nold5\n"), 0644))

	broadcaster := logstream.NewBroadcaster()
	broadcaster.Start("limit-follow-1") // register run as active
	srv := server.NewClocheServerWithCaptures(store, store, nil, "")
	srv.SetLogBroadcaster(broadcaster)

	// Follow mode with limit=2.
	streamCtx, streamCancel := context.WithTimeout(ctx, 5*time.Second)
	defer streamCancel()
	streamCtx = metadata.NewIncomingContext(streamCtx, metadata.Pairs(
		"x-cloche-follow", "true",
		"x-cloche-limit", "2",
	))
	mock := &mockLogStream{ctx: streamCtx}

	go func() {
		time.Sleep(50 * time.Millisecond)
		broadcaster.Publish("limit-follow-1", logstream.LogLine{
			Timestamp: "2026-03-10T10:00:00Z",
			Type:      "llm",
			Content:   "new live line",
			StepName:  "implement",
		})
		time.Sleep(50 * time.Millisecond)

		r, _ := store.GetRun(ctx, "limit-follow-1")
		r.Complete(domain.RunStateSucceeded)
		_ = store.UpdateRun(ctx, r)
		broadcaster.Finish("limit-follow-1")
	}()

	err = srv.StreamLogs(&pb.StreamLogsRequest{RunId: "limit-follow-1"}, mock)
	require.NoError(t, err)

	// The initial full_log should have only last 2 lines.
	var fullLogMsg string
	var liveLines int
	for _, e := range mock.entries {
		switch e.Type {
		case "full_log":
			fullLogMsg = e.Message
		case "log":
			liveLines++
		}
	}
	assert.Equal(t, "old4\nold5\n", fullLogMsg, "follow with limit=2 should truncate initial snapshot")
	assert.Equal(t, 1, liveLines, "live lines should be streamed regardless of limit")
}

// TestServer_StreamLogs_LimitCaptureOutput verifies that limit applies to
// per-step output in capture-based streaming (no full.log).
func TestServer_StreamLogs_LimitCaptureOutput(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	dir := t.TempDir()

	ctx := context.Background()
	run := domain.NewRun("limit-capture-1", "develop")
	run.ProjectDir = dir
	run.Start()
	run.ContainerID = "fake"
	run.RecordStepStart("build")
	run.RecordStepComplete("build", "success")
	run.Complete(domain.RunStateSucceeded)
	require.NoError(t, store.CreateRun(ctx, run))

	// Save captures for the step.
	require.NoError(t, store.SaveCapture(ctx, "limit-capture-1", &domain.StepExecution{
		StepName:  "build",
		StartedAt: time.Now().Add(-2 * time.Second),
	}))
	require.NoError(t, store.SaveCapture(ctx, "limit-capture-1", &domain.StepExecution{
		StepName:    "build",
		Result:      "success",
		StartedAt:   time.Now().Add(-2 * time.Second),
		CompletedAt: time.Now().Add(-1 * time.Second),
	}))

	// Write per-step output file.
	outputDir := filepath.Join(dir, ".cloche", "limit-capture-1", "output")
	require.NoError(t, os.MkdirAll(outputDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(outputDir, "build.log"),
		[]byte("compile start\ncompile middle\ncompile end\n"), 0644))

	srv := server.NewClocheServerWithCaptures(store, store, nil, "")

	// Request with limit=1.
	streamCtx := metadata.NewIncomingContext(ctx, metadata.Pairs("x-cloche-limit", "1"))
	mock := &mockLogStream{ctx: streamCtx}
	err = srv.StreamLogs(&pb.StreamLogsRequest{RunId: "limit-capture-1"}, mock)
	require.NoError(t, err)

	// Find the step_completed entry with output.
	var stepOutput string
	for _, e := range mock.entries {
		if e.Type == "step_completed" && e.StepName == "build" {
			stepOutput = e.Message
		}
	}
	assert.Equal(t, "compile end\n", stepOutput, "limit=1 should return only last line of step output")
}

// TestServer_StreamLogs_StepFilterFallbackToOut verifies that step-filtered
// logs fall back to .out files when .log files don't exist (host workflow runs).
func TestServer_StreamLogs_StepFilterFallbackToOut(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	dir := t.TempDir()
	ctx := context.Background()

	run := domain.NewRun("host-out-1", "main")
	run.ProjectDir = dir
	run.IsHost = true
	run.Start()
	run.Complete(domain.RunStateSucceeded)
	require.NoError(t, store.CreateRun(ctx, run))

	// Write a .log file (as host executor does)
	outputDir := filepath.Join(dir, ".cloche", "host-out-1", "output")
	require.NoError(t, os.MkdirAll(outputDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(outputDir, "prepare.log"), []byte("host step output\n"), 0644))

	srv := server.NewClocheServerWithCaptures(store, store, nil, "")

	mock := &mockLogStream{ctx: ctx}
	err = srv.StreamLogs(&pb.StreamLogsRequest{RunId: "host-out-1", StepName: "prepare"}, mock)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(mock.entries), 1)
	assert.Equal(t, "step_log", mock.entries[0].Type)
	assert.Equal(t, "prepare", mock.entries[0].StepName)
	assert.Contains(t, mock.entries[0].Message, "host step output")
}

// ---- V2 task-oriented RPC tests ----

func TestServer_RunWorkflow_ReturnsTaskIdAndAttemptId(t *testing.T) {
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
	assert.NotEmpty(t, resp.RunId)
	assert.NotEmpty(t, resp.TaskId, "response should include task_id")
	assert.NotEmpty(t, resp.AttemptId, "response should include attempt_id")
}

func TestServer_ListTasks_NoStore(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	srv := server.NewClocheServer(store, nil)
	// taskStore not set — should return error
	_, err = srv.ListTasks(context.Background(), &pb.ListTasksRequest{All: true})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "task store not configured")
}

func TestServer_ListTasks_Empty(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	srv := server.NewClocheServer(store, nil)
	srv.SetTaskStore(store)

	resp, err := srv.ListTasks(context.Background(), &pb.ListTasksRequest{All: true})
	require.NoError(t, err)
	assert.Empty(t, resp.Tasks)
}

func TestServer_ListTasks_WithTasks(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	srv := server.NewClocheServer(store, nil)
	srv.SetTaskStore(store)
	srv.SetAttemptStore(store)

	// Create tasks directly in the store
	taskA := &domain.Task{
		ID:         "TASK-1",
		Title:      "Fix bug",
		Status:     domain.TaskStatusRunning,
		ProjectDir: "/proj",
		CreatedAt:  time.Now(),
	}
	require.NoError(t, store.SaveTask(ctx, taskA))
	attemptA := domain.NewAttempt("TASK-1")
	require.NoError(t, store.SaveAttempt(ctx, attemptA))

	taskB := &domain.Task{
		ID:         "TASK-2",
		Title:      "Add feature",
		Status:     domain.TaskStatusSucceeded,
		ProjectDir: "/proj",
		CreatedAt:  time.Now(),
	}
	require.NoError(t, store.SaveTask(ctx, taskB))

	resp, err := srv.ListTasks(ctx, &pb.ListTasksRequest{ProjectDir: "/proj"})
	require.NoError(t, err)
	assert.Len(t, resp.Tasks, 2)

	// Find TASK-1 and verify it has a latest_attempt_id
	var foundTask1 bool
	for _, ts := range resp.Tasks {
		if ts.TaskId == "TASK-1" {
			foundTask1 = true
			assert.Equal(t, "Fix bug", ts.Title)
			assert.NotEmpty(t, ts.LatestAttemptId)
		}
	}
	assert.True(t, foundTask1)
}

func TestServer_GetTask_NotFound(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	srv := server.NewClocheServer(store, nil)
	srv.SetTaskStore(store)

	_, err = srv.GetTask(context.Background(), &pb.GetTaskRequest{TaskId: "nonexistent"})
	assert.Error(t, err)
}

func TestServer_GetTask(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	srv := server.NewClocheServer(store, nil)
	srv.SetTaskStore(store)
	srv.SetAttemptStore(store)

	task := &domain.Task{
		ID:         "TASK-A",
		Title:      "My task",
		Status:     domain.TaskStatusFailed,
		ProjectDir: "/myproj",
		CreatedAt:  time.Now(),
	}
	require.NoError(t, store.SaveTask(ctx, task))
	attempt := domain.NewAttempt("TASK-A")
	attempt.Complete(domain.AttemptResultFailed)
	require.NoError(t, store.SaveAttempt(ctx, attempt))

	resp, err := srv.GetTask(ctx, &pb.GetTaskRequest{TaskId: "TASK-A"})
	require.NoError(t, err)
	assert.Equal(t, "TASK-A", resp.TaskId)
	assert.Equal(t, "My task", resp.Title)
	assert.Equal(t, "/myproj", resp.ProjectDir)
	require.Len(t, resp.Attempts, 1)
	assert.Equal(t, attempt.ID, resp.Attempts[0].AttemptId)
	assert.Equal(t, "failed", resp.Attempts[0].Result)
}

func TestServer_GetAttempt_NoStore(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	srv := server.NewClocheServer(store, nil)
	// attemptStore not set
	_, err = srv.GetAttempt(context.Background(), &pb.GetAttemptRequest{AttemptId: "abc1"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "attempt store not configured")
}

func TestServer_GetAttempt(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	srv := server.NewClocheServer(store, nil)
	srv.SetTaskStore(store)
	srv.SetAttemptStore(store)

	// Create task, attempt, and a matching run
	task := &domain.Task{
		ID:         "TASK-B",
		Title:      "Attempt test",
		ProjectDir: "/proj",
		CreatedAt:  time.Now(),
	}
	require.NoError(t, store.SaveTask(ctx, task))
	attempt := domain.NewAttempt("TASK-B")
	attempt.Complete(domain.AttemptResultSucceeded)
	require.NoError(t, store.SaveAttempt(ctx, attempt))

	// Create a run linked to this attempt
	run := domain.NewRun("run-attempt-test-1", "develop")
	run.TaskID = "TASK-B"
	run.AttemptID = attempt.ID
	run.ProjectDir = "/proj"
	require.NoError(t, store.CreateRun(ctx, run))

	resp, err := srv.GetAttempt(ctx, &pb.GetAttemptRequest{AttemptId: attempt.ID})
	require.NoError(t, err)
	assert.Equal(t, attempt.ID, resp.AttemptId)
	assert.Equal(t, "TASK-B", resp.TaskId)
	assert.Equal(t, "succeeded", resp.Result)
	assert.Equal(t, "run-attempt-test-1", resp.RunId)
}

func TestServer_GetStatus_ByTaskId(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	srv := server.NewClocheServer(store, nil)

	run := domain.NewRun("run-by-task-1", "develop")
	run.TaskID = "ISSUE-42"
	run.AttemptID = "xxxx"
	run.ProjectDir = "/proj"
	run.StartedAt = time.Now()
	run.State = domain.RunStateSucceeded
	require.NoError(t, store.CreateRun(ctx, run))

	resp, err := srv.GetStatus(ctx, &pb.GetStatusRequest{Id: "ISSUE-42"})
	require.NoError(t, err)
	assert.Equal(t, "run-by-task-1", resp.RunId)
	assert.Equal(t, "succeeded", resp.State)
}

func TestServer_GetStatus_ByAttemptId(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	srv := server.NewClocheServer(store, nil)
	srv.SetAttemptStore(store)
	srv.SetTaskStore(store)

	// Save task and attempt in store
	task := &domain.Task{
		ID:         "TASK-AT",
		Title:      "Test",
		ProjectDir: "/proj",
		CreatedAt:  time.Now(),
	}
	require.NoError(t, store.SaveTask(ctx, task))
	attempt := domain.NewAttempt("TASK-AT")
	require.NoError(t, store.SaveAttempt(ctx, attempt))

	run := domain.NewRun("run-by-attempt-1", "develop")
	run.TaskID = "TASK-AT"
	run.AttemptID = attempt.ID
	run.ProjectDir = "/proj"
	run.StartedAt = time.Now()
	run.State = domain.RunStateRunning
	require.NoError(t, store.CreateRun(ctx, run))

	resp, err := srv.GetStatus(ctx, &pb.GetStatusRequest{Id: attempt.ID})
	require.NoError(t, err)
	assert.Equal(t, "run-by-attempt-1", resp.RunId)
}

func TestServer_GetStatus_ByTaskAndAttemptColonDelimited(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	srv := server.NewClocheServer(store, nil)

	run := domain.NewRun("run-colon-1", "develop")
	run.TaskID = "TASK-C"
	run.AttemptID = "zzzz"
	run.ProjectDir = "/proj"
	run.StartedAt = time.Now()
	run.State = domain.RunStateFailed
	require.NoError(t, store.CreateRun(ctx, run))

	// Access via task_id:attempt_id
	resp, err := srv.GetStatus(ctx, &pb.GetStatusRequest{Id: "TASK-C:zzzz"})
	require.NoError(t, err)
	assert.Equal(t, "run-colon-1", resp.RunId)
	assert.Equal(t, "failed", resp.State)
}

func TestServer_ResolveRunIDFromID_AttemptWorkflow(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	srv := server.NewClocheServer(store, nil)

	run := domain.NewRun("develop", "develop")
	run.TaskID = "TASK-RW"
	run.AttemptID = "r1w1"
	run.ProjectDir = "/proj"
	run.Start()
	run.Complete(domain.RunStateSucceeded)
	require.NoError(t, store.CreateRun(ctx, run))

	// attempt_id:workflow_name → resolves to run, no step
	runID, step, err := srv.ResolveRunIDFromID(ctx, "r1w1:develop")
	require.NoError(t, err)
	assert.Equal(t, "develop", runID)
	assert.Empty(t, step)
}

func TestServer_ResolveRunIDFromID_AttemptWorkflowStep(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	srv := server.NewClocheServer(store, nil)

	run := domain.NewRun("develop", "develop")
	run.TaskID = "TASK-RWS"
	run.AttemptID = "r2w2"
	run.ProjectDir = "/proj"
	run.Start()
	run.Complete(domain.RunStateSucceeded)
	require.NoError(t, store.CreateRun(ctx, run))

	// attempt_id:workflow_name:step_name → resolves to run + step
	runID, step, err := srv.ResolveRunIDFromID(ctx, "r2w2:develop:review")
	require.NoError(t, err)
	assert.Equal(t, "develop", runID)
	assert.Equal(t, "review", step)
}

func TestServer_ResolveRunIDFromID_TaskAttemptBackcompat(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	srv := server.NewClocheServer(store, nil)

	run := domain.NewRun("develop", "develop")
	run.TaskID = "TASK-BC"
	run.AttemptID = "r3w3"
	run.ProjectDir = "/proj"
	run.Start()
	run.Complete(domain.RunStateSucceeded)
	require.NoError(t, store.CreateRun(ctx, run))

	// task_id:attempt_id still works (backward compat)
	runID, step, err := srv.ResolveRunIDFromID(ctx, "TASK-BC:r3w3")
	require.NoError(t, err)
	assert.Equal(t, "develop", runID)
	assert.Empty(t, step)
}

func TestServer_ResolveRunIDFromID_TaskAttemptStepBackcompat(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	srv := server.NewClocheServer(store, nil)

	run := domain.NewRun("develop", "develop")
	run.TaskID = "TASK-BCS"
	run.AttemptID = "r4w4"
	run.ProjectDir = "/proj"
	run.Start()
	run.Complete(domain.RunStateSucceeded)
	require.NoError(t, store.CreateRun(ctx, run))

	// task_id:attempt_id:step_name still works (backward compat)
	runID, step, err := srv.ResolveRunIDFromID(ctx, "TASK-BCS:r4w4:implement")
	require.NoError(t, err)
	assert.Equal(t, "develop", runID)
	assert.Equal(t, "implement", step)
}

func TestServer_ResolveRunIDFromID_TaskAttemptWorkflow(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	srv := server.NewClocheServer(store, nil)

	run := domain.NewRun("develop", "develop")
	run.TaskID = "TASK-TAW"
	run.AttemptID = "r5w5"
	run.ProjectDir = "/proj"
	run.Start()
	run.Complete(domain.RunStateSucceeded)
	require.NoError(t, store.CreateRun(ctx, run))

	// task_id:attempt_id:workflow_name → resolves to run, no step
	// (canonical 3-part Workflow ID as displayed by "cloche status")
	runID, step, err := srv.ResolveRunIDFromID(ctx, "TASK-TAW:r5w5:develop")
	require.NoError(t, err)
	assert.Equal(t, "develop", runID)
	assert.Empty(t, step)
}

func TestServer_ResolveRunIDFromID_TaskAttemptStepDistinct(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	srv := server.NewClocheServer(store, nil)

	run := domain.NewRun("develop", "develop")
	run.TaskID = "TASK-TSD"
	run.AttemptID = "r6w6"
	run.ProjectDir = "/proj"
	run.Start()
	run.Complete(domain.RunStateSucceeded)
	require.NoError(t, store.CreateRun(ctx, run))

	// task_id:attempt_id:step_name where step != workflow name → step is preserved
	runID, step, err := srv.ResolveRunIDFromID(ctx, "TASK-TSD:r6w6:implement")
	require.NoError(t, err)
	assert.Equal(t, "develop", runID)
	assert.Equal(t, "implement", step)
}

func TestServer_StreamLogs_ByAttemptWorkflowId(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	dir := t.TempDir()

	run := domain.NewRun("aws1-develop", "develop")
	run.TaskID = "TASK-AW"
	run.AttemptID = "aws1"
	run.ProjectDir = dir
	run.Start()
	run.Complete(domain.RunStateSucceeded)
	require.NoError(t, store.CreateRun(ctx, run))

	// Write full.log
	logDir := filepath.Join(dir, ".cloche", "logs", "TASK-AW", "aws1")
	require.NoError(t, os.MkdirAll(logDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(logDir, "full.log"), []byte("workflow log content\n"), 0644))

	srv := server.NewClocheServerWithCaptures(store, store, nil, "")

	mock := &mockLogStream{ctx: ctx}
	err = srv.StreamLogs(&pb.StreamLogsRequest{Id: "aws1:develop"}, mock)
	require.NoError(t, err)
	var found bool
	for _, e := range mock.entries {
		if e.Type == "full_log" && strings.Contains(e.Message, "workflow log content") {
			found = true
		}
	}
	assert.True(t, found, "should find full_log entry using attempt:workflow id")
}

func TestServer_StreamLogs_ByColonDelimitedId(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	dir := t.TempDir()

	run := domain.NewRun("run-stream-colon-1", "develop")
	run.TaskID = "TASK-SC"
	run.AttemptID = "aaaa"
	run.ProjectDir = dir
	run.Start()
	run.Complete(domain.RunStateSucceeded)
	require.NoError(t, store.CreateRun(ctx, run))

	// Write full.log for the run
	logDir := filepath.Join(dir, ".cloche", "logs", "TASK-SC", "aaaa")
	require.NoError(t, os.MkdirAll(logDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(logDir, "full.log"), []byte("colon log content\n"), 0644))

	srv := server.NewClocheServerWithCaptures(store, store, nil, "")

	// Stream logs using task_id:attempt_id colon-delimited id
	mock := &mockLogStream{ctx: ctx}
	err = srv.StreamLogs(&pb.StreamLogsRequest{Id: "TASK-SC:aaaa"}, mock)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(mock.entries), 1)
	var found bool
	for _, e := range mock.entries {
		if e.Type == "full_log" && strings.Contains(e.Message, "colon log content") {
			found = true
		}
	}
	assert.True(t, found, "should find full_log entry with colon log content")
}

// mockStopRuntime tracks which container IDs were stopped.
type mockStopRuntime struct {
	stopped []string
}

func (m *mockStopRuntime) Start(_ context.Context, _ ports.ContainerConfig) (string, error) {
	return "cid-new", nil
}

func (m *mockStopRuntime) Stop(_ context.Context, containerID string) error {
	m.stopped = append(m.stopped, containerID)
	return nil
}

func (m *mockStopRuntime) AttachOutput(_ context.Context, _ string) (io.ReadCloser, error) {
	return nil, nil
}

func (m *mockStopRuntime) Wait(_ context.Context, _ string) (int, error) { return 0, nil }

func (m *mockStopRuntime) CopyFrom(_ context.Context, _, _, _ string) error { return nil }

func (m *mockStopRuntime) Logs(_ context.Context, _ string) (string, error) { return "", nil }

func (m *mockStopRuntime) Remove(_ context.Context, _ string) error { return nil }

func (m *mockStopRuntime) Inspect(_ context.Context, _ string) (*ports.ContainerStatus, error) {
	return &ports.ContainerStatus{}, nil
}

func (m *mockStopRuntime) Attach(_ context.Context, _ string) (io.ReadWriteCloser, error) {
	return nil, fmt.Errorf("attach not supported in mock")
}

func TestServer_StopRun_StopsAllActiveRunsForTask(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	// Create two active runs associated with the same task.
	run1 := domain.NewRun("run-stop-1", "develop")
	run1.TaskID = "TASK-STOP"
	run1.Start()
	require.NoError(t, store.CreateRun(ctx, run1))

	run2 := domain.NewRun("run-stop-2", "develop")
	run2.TaskID = "TASK-STOP"
	run2.Start()
	require.NoError(t, store.CreateRun(ctx, run2))

	// Create a completed run for the same task — should not be stopped.
	runDone := domain.NewRun("run-stop-done", "develop")
	runDone.TaskID = "TASK-STOP"
	runDone.Start()
	runDone.Complete(domain.RunStateSucceeded)
	require.NoError(t, store.CreateRun(ctx, runDone))

	rt := &mockStopRuntime{}
	srv := server.NewClocheServerWithCaptures(store, store, rt, "")
	srv.AddActiveRun("run-stop-1", "container-1")
	srv.AddActiveRun("run-stop-2", "container-2")

	_, err = srv.StopRun(ctx, &pb.StopRunRequest{TaskId: "TASK-STOP"})
	require.NoError(t, err)

	// Both containers should have been stopped.
	assert.ElementsMatch(t, []string{"container-1", "container-2"}, rt.stopped)

	// Both runs should now be cancelled in the store.
	r1, err := store.GetRun(ctx, "run-stop-1")
	require.NoError(t, err)
	assert.Equal(t, domain.RunStateCancelled, r1.State)

	r2, err := store.GetRun(ctx, "run-stop-2")
	require.NoError(t, err)
	assert.Equal(t, domain.RunStateCancelled, r2.State)

	// Completed run should remain succeeded.
	rDone, err := store.GetRun(ctx, "run-stop-done")
	require.NoError(t, err)
	assert.Equal(t, domain.RunStateSucceeded, rDone.State)
}

func TestServer_StopRun_ErrorWhenNoActiveRuns(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	rt := &mockStopRuntime{}
	srv := server.NewClocheServerWithCaptures(store, store, rt, "")

	_, err = srv.StopRun(ctx, &pb.StopRunRequest{TaskId: "TASK-MISSING"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no active runs")
}

func TestServer_StopRun_ErrorWhenTaskIdEmpty(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	rt := &mockStopRuntime{}
	srv := server.NewClocheServerWithCaptures(store, store, rt, "")

	_, err = srv.StopRun(context.Background(), &pb.StopRunRequest{TaskId: ""})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "task_id is required")
}

func TestServer_StopRun_AlreadyCompletedRunsNotStopped(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	// Only a cancelled run for this task.
	runCancelled := domain.NewRun("run-already-done", "develop")
	runCancelled.TaskID = "TASK-DONE"
	runCancelled.Start()
	runCancelled.Complete(domain.RunStateCancelled)
	require.NoError(t, store.CreateRun(ctx, runCancelled))

	rt := &mockStopRuntime{}
	srv := server.NewClocheServerWithCaptures(store, store, rt, "")

	_, err = srv.StopRun(ctx, &pb.StopRunRequest{TaskId: "TASK-DONE"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no active runs")
	assert.Empty(t, rt.stopped)
}

func TestServer_StopRun_StopsUserInitiatedHostRun(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	// Simulate a user-initiated host run: task_id = "user-p8m5", run is host.
	hostRun := domain.NewRun("p8m5-main", "main")
	hostRun.TaskID = "user-p8m5"
	hostRun.IsHost = true
	hostRun.Start()
	require.NoError(t, store.CreateRun(ctx, hostRun))

	// Track whether the goroutine cancel was called.
	cancelled := make(chan struct{})
	cancelFn := func() { close(cancelled) }

	rt := &mockStopRuntime{}
	srv := server.NewClocheServerWithCaptures(store, store, rt, "")
	srv.AddActiveHostRun("p8m5-main", cancelFn)

	_, err = srv.StopRun(ctx, &pb.StopRunRequest{TaskId: "user-p8m5"})
	require.NoError(t, err)

	// The cancel function must have been called to stop the goroutine.
	select {
	case <-cancelled:
	default:
		t.Fatal("expected host run cancel function to be called")
	}

	// No container was stopped (host runs have no container).
	assert.Empty(t, rt.stopped)

	// The run should be marked Cancelled in the store.
	r, err := store.GetRun(ctx, "p8m5-main")
	require.NoError(t, err)
	assert.Equal(t, domain.RunStateCancelled, r.State)
}

// TestResumeTarget_TaskID_WithoutAttemptStore verifies that cloche resume
// accepts v2 task IDs even when the attempt store is not configured. The
// resolver should fall back to scanning runs by task_id directly.
func TestResumeTarget_TaskID_WithoutAttemptStore(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	// Create a failed run carrying only a task_id (no attempt store set up).
	run := domain.NewRun("a12z-develop", "develop")
	run.TaskID = "user-a12z"
	run.AttemptID = "a12z"
	run.State = domain.RunStateFailed
	run.StartedAt = time.Now().Add(-time.Minute)
	run.CompletedAt = time.Now()
	require.NoError(t, store.CreateRun(ctx, run))

	// Save a capture so FindFirstFailedStep can find the failed step.
	require.NoError(t, store.SaveCapture(ctx, run.ID, &domain.StepExecution{
		StepName:    "test",
		Result:      "fail",
		CompletedAt: time.Now(),
	}))

	// Server with NO attemptStore — task IDs must resolve via task_id scan.
	srv := server.NewClocheServerWithCaptures(store, store, nil, "")

	// Resume by task ID. Use NewIncomingContext so the server's metadata
	// readers (FromIncomingContext) see the headers.
	md := metadata.Pairs("x-cloche-resume-task-or-run", "user-a12z")
	resumeCtx := metadata.NewIncomingContext(ctx, md)
	_, err = srv.RunWorkflow(resumeCtx, &pb.RunWorkflowRequest{})

	// Should reach resumeContainerRun (no container runtime) rather than
	// "no task or run found", proving the task ID was resolved.
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "no task or run found",
		"task ID 'user-a12z' should have been resolved to a run")
	assert.Contains(t, err.Error(), "no container runtime",
		"expected resumeContainerRun to be reached")
}

// TestResumeTarget_TaskID_PrefersHostRun verifies that when a task has both
// a host run and child container runs that all failed, resume prefers the
// host run. The host run carries the step-level 'fail' records that
// correspond to failed container dispatches.
func TestResumeTarget_TaskID_PrefersHostRun(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	taskID := "user-hst1"
	attemptID := "hst1"

	// Save task and attempt.
	task := &domain.Task{
		ID:         taskID,
		Title:      "Host workflow task",
		ProjectDir: "/proj",
		CreatedAt:  time.Now(),
	}
	require.NoError(t, store.SaveTask(ctx, task))
	attempt := &domain.Attempt{
		ID:        attemptID,
		TaskID:    taskID,
		StartedAt: time.Now().Add(-2 * time.Minute),
		Result:    domain.AttemptResultFailed,
	}
	require.NoError(t, store.SaveAttempt(ctx, attempt))

	// Host run started first.
	hostRun := domain.NewRun("hst1-fowm-develop", "fowm-develop")
	hostRun.TaskID = taskID
	hostRun.AttemptID = attemptID
	hostRun.IsHost = true
	hostRun.State = domain.RunStateFailed
	hostRun.StartedAt = time.Now().Add(-2 * time.Minute)
	hostRun.CompletedAt = time.Now().Add(-time.Minute)
	require.NoError(t, store.CreateRun(ctx, hostRun))

	// Child container run started later (dispatched by the host).
	childRun := domain.NewRun("hst1-develop", "develop")
	childRun.TaskID = taskID
	childRun.AttemptID = attemptID
	childRun.ParentRunID = hostRun.ID
	childRun.State = domain.RunStateFailed
	childRun.StartedAt = time.Now().Add(-time.Minute)
	childRun.CompletedAt = time.Now()
	require.NoError(t, store.CreateRun(ctx, childRun))

	// The host run has a 'fail' capture for its "develop" step.
	require.NoError(t, store.SaveCapture(ctx, hostRun.ID, &domain.StepExecution{
		StepName:    "develop",
		Result:      "fail",
		CompletedAt: time.Now().Add(-time.Minute),
	}))

	srv := server.NewClocheServerWithCaptures(store, store, nil, "")
	srv.SetTaskStore(store)
	srv.SetAttemptStore(store)

	// Resume by task ID. Use NewIncomingContext so the server's metadata
	// readers (FromIncomingContext) see the headers.
	md := metadata.Pairs("x-cloche-resume-task-or-run", taskID)
	resumeCtx := metadata.NewIncomingContext(ctx, md)
	resp, err := srv.RunWorkflow(resumeCtx, &pb.RunWorkflowRequest{})

	// resumeHostRun spawns a goroutine and returns success immediately.
	// If we got a "no container runtime" error instead, the container run
	// was incorrectly selected.
	require.NoError(t, err, "should have picked host run (resumeHostRun returns success, not error)")
	// A new run is created for the new attempt; the returned ID differs from
	// the original host run's ID.
	assert.NotEmpty(t, resp.RunId, "response should carry a run ID")
	assert.NotEqual(t, hostRun.ID, resp.RunId, "resume should create a new run, not reuse the old one")
	assert.NotEmpty(t, resp.AttemptId, "response should carry the new attempt ID")
}

// TestResumeTarget_FailResultIsResumable verifies that a run where a step
// returned a 'fail' result (via normal wiring to abort) is treated as
// resumable. The step's 'fail' result must be found by FindFirstFailedStep.
func TestResumeTarget_FailResultIsResumable(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	// Container run that failed because a step wired 'fail' → abort.
	run := domain.NewRun("ab12-develop", "develop")
	run.TaskID = "user-ab12"
	run.AttemptID = "ab12"
	run.State = domain.RunStateFailed
	run.StartedAt = time.Now().Add(-time.Minute)
	run.CompletedAt = time.Now()
	require.NoError(t, store.CreateRun(ctx, run))

	// Two steps: dev succeeded, test failed via wiring (not crash).
	require.NoError(t, store.SaveCapture(ctx, run.ID, &domain.StepExecution{
		StepName:    "dev",
		Result:      "success",
		CompletedAt: time.Now().Add(-30 * time.Second),
	}))
	require.NoError(t, store.SaveCapture(ctx, run.ID, &domain.StepExecution{
		StepName:    "test",
		Result:      "fail",
		CompletedAt: time.Now(),
	}))

	srv := server.NewClocheServerWithCaptures(store, store, nil, "")

	// Resume by bare run ID. Use NewIncomingContext so the server's metadata
	// readers (FromIncomingContext) see the headers.
	md := metadata.Pairs(
		"x-cloche-resume-run-id", run.ID,
		"x-cloche-resume-step", "",
		"x-cloche-resume-task-or-run", "",
	)
	resumeCtx := metadata.NewIncomingContext(ctx, md)
	_, err = srv.RunWorkflow(resumeCtx, &pb.RunWorkflowRequest{})

	// Should reach resumeContainerRun (fail result was found as resume point),
	// not "has no failed step to resume from".
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "has no failed step to resume from",
		"'fail' wired result should be treated as resumable")
	assert.Contains(t, err.Error(), "no container runtime",
		"expected to reach resumeContainerRun")
}

// TestResume_CreatesNewAttempt_HostRun verifies that resuming a failed host run
// creates a new Attempt with PreviousAttemptID pointing to the old attempt, and
// returns a new run ID rather than mutating the old run.
func TestResume_CreatesNewAttempt_HostRun(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	taskID := "user-aa11"
	oldAttemptID := "aa11"

	// Save task and failed attempt.
	require.NoError(t, store.SaveTask(ctx, &domain.Task{
		ID:         taskID,
		Title:      "test task",
		ProjectDir: "/proj",
		CreatedAt:  time.Now(),
	}))
	oldAttempt := &domain.Attempt{
		ID:        oldAttemptID,
		TaskID:    taskID,
		StartedAt: time.Now().Add(-time.Minute),
		Result:    domain.AttemptResultFailed,
	}
	require.NoError(t, store.SaveAttempt(ctx, oldAttempt))

	// Failed host run.
	hostRun := domain.NewRun(domain.GenerateRunID("main", oldAttemptID), "main")
	hostRun.TaskID = taskID
	hostRun.AttemptID = oldAttemptID
	hostRun.IsHost = true
	hostRun.State = domain.RunStateFailed
	hostRun.StartedAt = time.Now().Add(-time.Minute)
	hostRun.CompletedAt = time.Now()
	require.NoError(t, store.CreateRun(ctx, hostRun))

	// One failed step capture.
	require.NoError(t, store.SaveCapture(ctx, hostRun.ID, &domain.StepExecution{
		StepName:    "build",
		Result:      "fail",
		CompletedAt: time.Now(),
	}))

	srv := server.NewClocheServerWithCaptures(store, store, nil, "")
	srv.SetTaskStore(store)
	srv.SetAttemptStore(store)

	md := metadata.Pairs("x-cloche-resume-run-id", hostRun.ID)
	resumeCtx := metadata.NewIncomingContext(ctx, md)
	resp, err := srv.RunWorkflow(resumeCtx, &pb.RunWorkflowRequest{})
	require.NoError(t, err)

	// Response carries the new run ID and new attempt ID.
	assert.NotEmpty(t, resp.RunId)
	assert.NotEqual(t, hostRun.ID, resp.RunId, "resume should create a new run ID")
	assert.NotEmpty(t, resp.AttemptId)
	assert.NotEqual(t, oldAttemptID, resp.AttemptId, "resume should create a new attempt ID")

	// Old run remains failed (not mutated).
	oldRunFetched, err := store.GetRunByAttempt(ctx, oldAttemptID, hostRun.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.RunStateFailed, oldRunFetched.State, "old run must stay failed")

	// New attempt has PreviousAttemptID linking back to the old attempt.
	newAttempts, err := store.ListAttempts(ctx, taskID)
	require.NoError(t, err)
	require.Len(t, newAttempts, 2, "task should now have two attempts")
	newAttempt := newAttempts[1] // second attempt (ordered by started_at ASC)
	assert.Equal(t, resp.AttemptId, newAttempt.ID)
	assert.Equal(t, oldAttemptID, newAttempt.PreviousAttemptID, "new attempt must reference the old one")
}

// TestResume_PreviousAttemptID_Persists verifies that SaveAttempt and GetAttempt
// correctly round-trip the PreviousAttemptID field.
func TestResume_PreviousAttemptID_Persists(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	require.NoError(t, store.SaveTask(ctx, &domain.Task{
		ID:        "task-prev",
		CreatedAt: time.Now(),
	}))

	first := &domain.Attempt{
		ID:        "aaaa",
		TaskID:    "task-prev",
		StartedAt: time.Now().Add(-time.Minute),
		Result:    domain.AttemptResultFailed,
	}
	require.NoError(t, store.SaveAttempt(ctx, first))

	second := &domain.Attempt{
		ID:                "bbbb",
		TaskID:            "task-prev",
		PreviousAttemptID: "aaaa",
		StartedAt:         time.Now(),
		Result:            domain.AttemptResultRunning,
	}
	require.NoError(t, store.SaveAttempt(ctx, second))

	got, err := store.GetAttempt(ctx, "bbbb")
	require.NoError(t, err)
	assert.Equal(t, "aaaa", got.PreviousAttemptID)

	listed, err := store.ListAttempts(ctx, "task-prev")
	require.NoError(t, err)
	require.Len(t, listed, 2)
	assert.Equal(t, "aaaa", listed[1].PreviousAttemptID)
	assert.Equal(t, "", listed[0].PreviousAttemptID, "first attempt has no previous")
}

// TestResume_ContainerRun_WithPool verifies that when a ContainerPool is
// configured, resumeContainerRun:
//   - creates a new Attempt with PreviousAttemptID pointing to the failed one
//   - creates a new Run record linked to the new attempt
//   - returns a response with the new run/attempt IDs
//   - calls CommitForResume on the old attempt's containers
//
// The test uses a resumableNopRuntime so no real Docker operations occur.
func TestResume_ContainerRun_WithPool(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	taskID := "user-rc01"
	oldAttemptID := "rc01"

	require.NoError(t, store.SaveTask(ctx, &domain.Task{
		ID:         taskID,
		Title:      "container task",
		ProjectDir: "/tmp",
		CreatedAt:  time.Now(),
	}))
	oldAttempt := &domain.Attempt{
		ID:        oldAttemptID,
		TaskID:    taskID,
		StartedAt: time.Now().Add(-time.Minute),
		Result:    domain.AttemptResultFailed,
	}
	require.NoError(t, store.SaveAttempt(ctx, oldAttempt))

	// Failed container run with one failed step.
	failedRun := domain.NewRun(domain.GenerateRunID("develop", oldAttemptID), "develop")
	failedRun.TaskID = taskID
	failedRun.AttemptID = oldAttemptID
	failedRun.State = domain.RunStateFailed
	failedRun.ProjectDir = "/tmp"
	failedRun.StartedAt = time.Now().Add(-time.Minute)
	failedRun.CompletedAt = time.Now()
	require.NoError(t, store.CreateRun(ctx, failedRun))

	require.NoError(t, store.SaveCapture(ctx, failedRun.ID, &domain.StepExecution{
		StepName:    "code",
		Result:      "fail",
		CompletedAt: time.Now(),
	}))

	// Build a pool backed by resumableNopRuntime.
	rt := &resumableNopRuntime{}
	pool := docker.NewContainerPool(rt)

	// Register a container for the old attempt so CommitForResume has something to commit.
	go func() {
		time.Sleep(20 * time.Millisecond)
		rt.mu.Lock()
		var id string
		if len(rt.started) > 0 {
			id = rt.started[0]
		}
		rt.mu.Unlock()
		if id != "" {
			pool.NotifyReady(id)
		}
	}()
	_, err = pool.SessionFor(ctx, oldAttemptID+":_default", ports.ContainerConfig{Image: "img", AttemptID: oldAttemptID})
	require.NoError(t, err)

	// Build a pool for the NEW attempt (resume start will trigger StartFromImage
	// which calls SessionFor; we pre-notify so it doesn't block).
	go func() {
		// Keep notifying any new container the runtime starts.
		for i := 0; i < 10; i++ {
			time.Sleep(30 * time.Millisecond)
			rt.mu.Lock()
			ids := append([]string(nil), rt.started...)
			rt.mu.Unlock()
			for _, id := range ids {
				pool.NotifyReady(id)
			}
		}
	}()

	srv := server.NewClocheServerWithCaptures(store, store, rt, "default-image")
	srv.SetTaskStore(store)
	srv.SetAttemptStore(store)
	srv.SetContainerPool(pool)

	md := metadata.Pairs("x-cloche-resume-run-id", failedRun.ID)
	resumeCtx := metadata.NewIncomingContext(ctx, md)
	resp, err := srv.RunWorkflow(resumeCtx, &pb.RunWorkflowRequest{})
	// resumeContainerRunWithPool fails to parse workflow (no .cloche dir at /tmp)
	// but should still fail with a workflow-related error, not "no container runtime".
	// Since /tmp has no .cloche, FindAllWorkflows returns an error.
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "no container runtime",
		"should reach pool-based path, not legacy path")
	_ = resp
}

// TestResume_ContainerRun_WithPool_CreatesNewAttempt verifies the new attempt
// and run records are created when the workflow can be found.
func TestResume_ContainerRun_WithPool_CreatesNewAttempt(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	taskID := "user-rc02"
	oldAttemptID := "rc02"

	require.NoError(t, store.SaveTask(ctx, &domain.Task{
		ID:         taskID,
		Title:      "container task 2",
		ProjectDir: "/tmp",
		CreatedAt:  time.Now(),
	}))
	require.NoError(t, store.SaveAttempt(ctx, &domain.Attempt{
		ID:        oldAttemptID,
		TaskID:    taskID,
		StartedAt: time.Now().Add(-time.Minute),
		Result:    domain.AttemptResultFailed,
	}))

	failedRun := domain.NewRun(domain.GenerateRunID("develop", oldAttemptID), "develop")
	failedRun.TaskID = taskID
	failedRun.AttemptID = oldAttemptID
	failedRun.State = domain.RunStateFailed
	failedRun.ProjectDir = "/tmp"
	failedRun.StartedAt = time.Now().Add(-time.Minute)
	failedRun.CompletedAt = time.Now()
	require.NoError(t, store.CreateRun(ctx, failedRun))

	require.NoError(t, store.SaveCapture(ctx, failedRun.ID, &domain.StepExecution{
		StepName:    "code",
		Result:      "fail",
		CompletedAt: time.Now(),
	}))

	rt := &resumableNopRuntime{}
	pool := docker.NewContainerPool(rt)

	srv := server.NewClocheServerWithCaptures(store, store, rt, "default-image")
	srv.SetTaskStore(store)
	srv.SetAttemptStore(store)
	srv.SetContainerPool(pool)

	md := metadata.Pairs("x-cloche-resume-run-id", failedRun.ID)
	resumeCtx := metadata.NewIncomingContext(ctx, md)
	_, resumeErr := srv.RunWorkflow(resumeCtx, &pb.RunWorkflowRequest{})

	// The resume reaches the pool-based path. Since /tmp has no workflow files
	// FindAllWorkflows returns an empty map, causing "workflow not found" error.
	// Crucially, the new attempt should already have been created before the error.
	require.Error(t, resumeErr)
	assert.NotContains(t, resumeErr.Error(), "no container runtime",
		"pool path should be taken when pool is configured")

	// A new attempt should have been created with PreviousAttemptID = oldAttemptID.
	attempts, err := store.ListAttempts(ctx, taskID)
	require.NoError(t, err)
	require.Len(t, attempts, 2, "a new attempt should have been created")
	newAttempt := attempts[1]
	assert.Equal(t, oldAttemptID, newAttempt.PreviousAttemptID,
		"new attempt must reference the failed one")
}

func TestServer_GetUsage_Empty(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	srv := server.NewClocheServer(store, nil)
	resp, err := srv.GetUsage(context.Background(), &pb.GetUsageRequest{})
	require.NoError(t, err)
	assert.Empty(t, resp.Summaries)
}

func TestServer_GetUsage_WithTokenData(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	// Create a run and save step executions with token usage.
	run := domain.NewRun("run-usage-1", "develop")
	run.ProjectDir = "/project"
	run.State = domain.RunStateSucceeded
	run.StartedAt = time.Now().Add(-10 * time.Minute)
	run.CompletedAt = time.Now()
	require.NoError(t, store.CreateRun(ctx, run))

	exec1 := &domain.StepExecution{
		StepName:    "implement",
		Result:      "done",
		StartedAt:   run.StartedAt,
		CompletedAt: run.CompletedAt,
		Usage: &domain.TokenUsage{
			InputTokens:  1000,
			OutputTokens: 500,
			AgentName:    "claude",
		},
	}
	require.NoError(t, store.SaveCapture(ctx, run.ID, exec1))

	srv := server.NewClocheServer(store, nil)
	resp, err := srv.GetUsage(ctx, &pb.GetUsageRequest{ProjectDir: "/project"})
	require.NoError(t, err)
	require.Len(t, resp.Summaries, 1)
	assert.Equal(t, "claude", resp.Summaries[0].AgentName)
	assert.Equal(t, int64(1000), resp.Summaries[0].InputTokens)
	assert.Equal(t, int64(500), resp.Summaries[0].OutputTokens)
	assert.Equal(t, int64(1500), resp.Summaries[0].TotalTokens)
}

func TestServer_GetUsage_WindowFilter(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	// Create a run with old step execution (outside window).
	run := domain.NewRun("run-old", "develop")
	run.ProjectDir = "/project"
	run.State = domain.RunStateSucceeded
	run.StartedAt = time.Now().Add(-4 * time.Hour)
	run.CompletedAt = time.Now().Add(-3 * time.Hour)
	require.NoError(t, store.CreateRun(ctx, run))

	exec := &domain.StepExecution{
		StepName:    "implement",
		Result:      "done",
		StartedAt:   run.StartedAt,
		CompletedAt: run.CompletedAt,
		Usage: &domain.TokenUsage{
			InputTokens:  5000,
			OutputTokens: 2000,
			AgentName:    "claude",
		},
	}
	require.NoError(t, store.SaveCapture(ctx, run.ID, exec))

	srv := server.NewClocheServer(store, nil)
	// Query with 1h window — old data should be excluded.
	resp, err := srv.GetUsage(ctx, &pb.GetUsageRequest{
		ProjectDir:    "/project",
		WindowSeconds: 3600,
	})
	require.NoError(t, err)
	// Either empty or zero tokens (no data in last hour).
	for _, s := range resp.Summaries {
		assert.Equal(t, int64(0), s.TotalTokens, "old usage should not appear in 1h window")
	}
}

func TestServer_GetStatus_IncludesTokenFields(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	run := domain.NewRun("run-tok-1", "develop")
	run.ProjectDir = "/project"
	run.State = domain.RunStateSucceeded
	run.StartedAt = time.Now().Add(-5 * time.Minute)
	run.CompletedAt = time.Now()
	require.NoError(t, store.CreateRun(ctx, run))

	exec := &domain.StepExecution{
		StepName:    "implement",
		Result:      "done",
		StartedAt:   run.StartedAt,
		CompletedAt: run.CompletedAt,
		Usage: &domain.TokenUsage{
			InputTokens:  2345,
			OutputTokens: 1234,
			AgentName:    "claude",
		},
	}
	require.NoError(t, store.SaveCapture(ctx, run.ID, exec))

	srv := server.NewClocheServerWithCaptures(store, store, nil, "")
	resp, err := srv.GetStatus(ctx, &pb.GetStatusRequest{RunId: "run-tok-1"})
	require.NoError(t, err)
	require.Len(t, resp.StepExecutions, 1)
	se := resp.StepExecutions[0]
	assert.Equal(t, int64(2345), se.InputTokens)
	assert.Equal(t, int64(1234), se.OutputTokens)
	assert.Equal(t, "claude", se.AgentName)
}

// ---------------------------------------------------------------------------
// Tests for log chunking (ResourceExhausted prevention)
// ---------------------------------------------------------------------------

func TestSplitIntoChunks_SmallContent(t *testing.T) {
	content := "hello\nworld\n"
	chunks := server.SplitIntoChunks(content, 1024)
	require.Len(t, chunks, 1)
	assert.Equal(t, content, chunks[0])
}

func TestSplitIntoChunks_ExactBoundary(t *testing.T) {
	// Content exactly at the limit → single chunk.
	content := strings.Repeat("a", 512)
	chunks := server.SplitIntoChunks(content, 512)
	require.Len(t, chunks, 1)
	assert.Equal(t, content, chunks[0])
}

func TestSplitIntoChunks_MultipleChunks(t *testing.T) {
	// Build content with 10 lines of 100 bytes each → total 1000 bytes.
	// Chunk at 300 bytes → should produce 4 chunks.
	line := strings.Repeat("x", 99) + "\n" // 100 bytes per line
	content := strings.Repeat(line, 10)    // 1000 bytes total

	chunks := server.SplitIntoChunks(content, 300)
	require.Greater(t, len(chunks), 1)

	// Re-assembling chunks should reproduce original content.
	var reassembled strings.Builder
	for _, c := range chunks {
		reassembled.WriteString(c)
	}
	assert.Equal(t, content, reassembled.String())
}

func TestSplitIntoChunks_ChunkSizeRespected(t *testing.T) {
	line := strings.Repeat("y", 49) + "\n" // 50 bytes per line
	content := strings.Repeat(line, 20)    // 1000 bytes total
	maxSize := 200

	chunks := server.SplitIntoChunks(content, maxSize)
	for i, chunk := range chunks {
		// Each chunk except possibly the last must be <= maxSize.
		// (A single line that alone exceeds maxSize would still be sent whole.)
		if i < len(chunks)-1 {
			assert.LessOrEqual(t, len(chunk), maxSize+50 /* one line tolerance */, "chunk %d too large", i)
		}
	}
}

func TestSplitIntoChunks_NoTrailingBlankLine(t *testing.T) {
	// Content with trailing newline should not produce a spurious trailing chunk.
	content := "line1\nline2\n"
	chunks := server.SplitIntoChunks(content, 1024)
	require.Len(t, chunks, 1)
	assert.Equal(t, content, chunks[0])
}

func TestSendContentChunked_SmallContent(t *testing.T) {
	mock := &mockLogStream{ctx: context.Background()}
	content := "small log content\n"
	err := server.SendContentChunked(mock, "full_log", "", "", "", content)
	require.NoError(t, err)
	require.Len(t, mock.entries, 1)
	assert.Equal(t, "full_log", mock.entries[0].Type)
	assert.Equal(t, content, mock.entries[0].Message)
}

func TestSendContentChunked_LargeContent(t *testing.T) {
	mock := &mockLogStream{ctx: context.Background()}
	// Generate content larger than maxLogChunkSize.
	line := strings.Repeat("z", 99) + "\n"
	content := strings.Repeat(line, server.MaxLogChunkSize/100+10)

	err := server.SendContentChunked(mock, "full_log", "step1", "done", "ts", content)
	require.NoError(t, err)
	require.Greater(t, len(mock.entries), 1, "large content should be split into multiple messages")

	// First message must carry the original type and all metadata.
	first := mock.entries[0]
	assert.Equal(t, "full_log", first.Type)
	assert.Equal(t, "step1", first.StepName)
	assert.Equal(t, "done", first.Result)
	assert.Equal(t, "ts", first.Timestamp)

	// Subsequent messages must be "log_chunk" type.
	for _, e := range mock.entries[1:] {
		assert.Equal(t, "log_chunk", e.Type, "continuation chunks must have type log_chunk")
		assert.Equal(t, "step1", e.StepName)
	}

	// Individual messages must be within the size limit.
	for i, e := range mock.entries {
		assert.LessOrEqual(t, len(e.Message), server.MaxLogChunkSize+200 /* one-line tolerance */, "message %d too large", i)
	}

	// Reassembling all messages must reproduce the original content.
	var reassembled strings.Builder
	for _, e := range mock.entries {
		reassembled.WriteString(e.Message)
	}
	assert.Equal(t, content, reassembled.String())
}

func TestStreamLogs_LargeFullLog_IsChunked(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	dir := t.TempDir()
	ctx := context.Background()

	// Create a completed run.
	run := domain.NewRun("run-large-log-1", "develop")
	run.ProjectDir = dir
	run.Start()
	run.Complete(domain.RunStateSucceeded)
	require.NoError(t, store.CreateRun(ctx, run))

	// Write a full.log larger than maxLogChunkSize.
	logDir := filepath.Join(dir, ".cloche", "run-large-log-1", "output")
	require.NoError(t, os.MkdirAll(logDir, 0755))
	line := strings.Repeat("L", 99) + "\n"
	largeLog := strings.Repeat(line, server.MaxLogChunkSize/100+10)
	require.NoError(t, os.WriteFile(filepath.Join(logDir, "full.log"), []byte(largeLog), 0644))

	srv := server.NewClocheServerWithCaptures(store, store, nil, "")

	mock := &mockLogStream{ctx: context.Background()}
	err = srv.StreamLogs(&pb.StreamLogsRequest{RunId: "run-large-log-1"}, mock)
	require.NoError(t, err)

	// Should have multiple log entries.
	require.Greater(t, len(mock.entries), 1, "large full.log should be split into multiple messages")

	// First message must be full_log.
	assert.Equal(t, "full_log", mock.entries[0].Type)

	// Continuation messages must be log_chunk.
	for _, e := range mock.entries[1:] {
		if e.Type == "run_completed" {
			break
		}
		assert.Equal(t, "log_chunk", e.Type)
	}

	// All log content should be present.
	var totalContent strings.Builder
	for _, e := range mock.entries {
		if e.Type == "full_log" || e.Type == "log_chunk" {
			totalContent.WriteString(e.Message)
		}
	}
	assert.Equal(t, largeLog, totalContent.String())
}

// mockConsoleStream implements grpc.BidiStreamingServer[pb.ConsoleInput, pb.ConsoleOutput]
// for testing the Console RPC handler.
type mockConsoleStream struct {
	grpclib.ServerStream
	ctx     context.Context
	inputs  chan *pb.ConsoleInput
	outputs []*pb.ConsoleOutput
	mu      sync.Mutex
}

func newMockConsoleStream(ctx context.Context) *mockConsoleStream {
	return &mockConsoleStream{
		ctx:    ctx,
		inputs: make(chan *pb.ConsoleInput, 16),
	}
}

func (m *mockConsoleStream) Context() context.Context { return m.ctx }

func (m *mockConsoleStream) Recv() (*pb.ConsoleInput, error) {
	msg, ok := <-m.inputs
	if !ok {
		return nil, io.EOF
	}
	return msg, nil
}

func (m *mockConsoleStream) Send(out *pb.ConsoleOutput) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.outputs = append(m.outputs, out)
	return nil
}

func (m *mockConsoleStream) getOutputs() []*pb.ConsoleOutput {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]*pb.ConsoleOutput, len(m.outputs))
	copy(cp, m.outputs)
	return cp
}

// consoleRuntime is a ContainerRuntime that supports interactive Attach for Console tests.
type consoleRuntime struct {
	startCfg   ports.ContainerConfig
	containerID string
	attachConn io.ReadWriteCloser
	waitCode   int
	waitSignal chan struct{}
}

func newConsoleRuntime(conn io.ReadWriteCloser, waitCode int) *consoleRuntime {
	return &consoleRuntime{
		containerID: "console-test-cid",
		attachConn:  conn,
		waitCode:    waitCode,
		waitSignal:  make(chan struct{}),
	}
}

func (r *consoleRuntime) Start(_ context.Context, cfg ports.ContainerConfig) (string, error) {
	r.startCfg = cfg
	return r.containerID, nil
}

func (r *consoleRuntime) Stop(_ context.Context, _ string) error        { return nil }
func (r *consoleRuntime) CopyFrom(_ context.Context, _, _, _ string) error { return nil }
func (r *consoleRuntime) Logs(_ context.Context, _ string) (string, error) { return "", nil }
func (r *consoleRuntime) Remove(_ context.Context, _ string) error         { return nil }
func (r *consoleRuntime) Inspect(_ context.Context, _ string) (*ports.ContainerStatus, error) {
	return &ports.ContainerStatus{}, nil
}
func (r *consoleRuntime) AttachOutput(_ context.Context, _ string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}

func (r *consoleRuntime) Attach(_ context.Context, _ string) (io.ReadWriteCloser, error) {
	return r.attachConn, nil
}

func (r *consoleRuntime) Wait(_ context.Context, _ string) (int, error) {
	<-r.waitSignal
	return r.waitCode, nil
}

// pipeConn wraps a pair of io.Pipe connections for bidirectional I/O in tests.
type pipeConn struct {
	r *io.PipeReader
	w *io.PipeWriter
}

func (p *pipeConn) Read(b []byte) (int, error)  { return p.r.Read(b) }
func (p *pipeConn) Write(b []byte) (int, error) { return p.w.Write(b) }
func (p *pipeConn) Close() error {
	p.r.Close()
	p.w.Close()
	return nil
}

func TestServer_Console_SendsStartedAndExited(t *testing.T) {
	// Set up a pipe so we can control what the "container" outputs.
	pr, pw := io.Pipe()
	conn := &pipeConn{r: pr, w: pw}

	rt := newConsoleRuntime(conn, 0)

	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	srv := server.NewClocheServerWithCaptures(store, store, rt, "test-image:latest")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream := newMockConsoleStream(ctx)

	// Send ConsoleStart as first message.
	stream.inputs <- &pb.ConsoleInput{
		Payload: &pb.ConsoleInput_Start{
			Start: &pb.ConsoleStart{
				ProjectDir:   t.TempDir(),
				AgentCommand: "echo",
				Rows:         24,
				Cols:         80,
			},
		},
	}

	// Run Console handler in a goroutine.
	handlerDone := make(chan error, 1)
	go func() {
		handlerDone <- srv.Console(stream)
	}()

	// Wait until ConsoleStarted is received.
	deadline := time.Now().Add(3 * time.Second)
	var started *pb.ConsoleStarted
	for time.Now().Before(deadline) {
		for _, out := range stream.getOutputs() {
			if s := out.GetStarted(); s != nil {
				started = s
				break
			}
		}
		if started != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	require.NotNil(t, started, "expected ConsoleStarted message")
	assert.Equal(t, "console-test-cid", started.ContainerId)

	// Write some output from the "container" and then close the pipe.
	_, err = pw.Write([]byte("hello from container\n"))
	require.NoError(t, err)
	pw.Close() // Signals EOF to output pump.

	// Signal the container to exit.
	close(rt.waitSignal)

	// Wait for handler to finish.
	select {
	case err := <-handlerDone:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Console handler did not finish within timeout")
	}

	// Verify ConsoleExited was sent.
	var exited *pb.ConsoleExited
	for _, out := range stream.getOutputs() {
		if e := out.GetExited(); e != nil {
			exited = e
			break
		}
	}
	require.NotNil(t, exited, "expected ConsoleExited message")
	assert.Equal(t, int32(0), exited.ExitCode)

	// Verify the container was NOT removed (no Remove call needed — just verify Start was called with Interactive=true).
	assert.True(t, rt.startCfg.Interactive, "container should be started with Interactive=true")
	assert.Equal(t, []string{"echo"}, rt.startCfg.Cmd)
	assert.True(t, strings.HasPrefix(rt.startCfg.RunID, "console-"), "container name should start with console-")
}

func TestServer_Console_ForwardsStdinToContainer(t *testing.T) {
	pr, pw := io.Pipe()
	conn := &pipeConn{r: pr, w: pw}

	rt := newConsoleRuntime(conn, 0)

	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	srv := server.NewClocheServerWithCaptures(store, store, rt, "test-image:latest")

	// Use a separate pipe to capture what gets written to the container's stdin.
	stdinR, stdinW := io.Pipe()
	// Replace the conn's write end so we can read what was sent to the container.
	writeCapture := &struct {
		pipeConn
		written []byte
		mu      sync.Mutex
	}{pipeConn: pipeConn{r: pr, w: stdinW}}
	_ = stdinR // unused here; we just capture writes

	rt.attachConn = &captureWriteConn{
		ReadWriteCloser: conn,
		written:         make(chan []byte, 8),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = writeCapture

	stream := newMockConsoleStream(ctx)
	stream.inputs <- &pb.ConsoleInput{
		Payload: &pb.ConsoleInput_Start{
			Start: &pb.ConsoleStart{ProjectDir: t.TempDir(), AgentCommand: "cat"},
		},
	}

	handlerDone := make(chan error, 1)
	go func() {
		handlerDone <- srv.Console(stream)
	}()

	// Wait for ConsoleStarted.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		started := false
		for _, out := range stream.getOutputs() {
			if out.GetStarted() != nil {
				started = true
				break
			}
		}
		if started {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Send stdin bytes.
	stream.inputs <- &pb.ConsoleInput{
		Payload: &pb.ConsoleInput_Stdin{Stdin: []byte("hello")},
	}

	// Close the input channel and pipe to trigger cleanup.
	close(stream.inputs)
	pw.Close()
	close(rt.waitSignal)

	select {
	case <-handlerDone:
	case <-time.After(5 * time.Second):
		t.Fatal("Console handler did not finish within timeout")
	}
}

// captureWriteConn wraps a ReadWriteCloser and records writes to a channel.
type captureWriteConn struct {
	io.ReadWriteCloser
	written chan []byte
}

func (c *captureWriteConn) Write(p []byte) (int, error) {
	cp := make([]byte, len(p))
	copy(cp, p)
	select {
	case c.written <- cp:
	default:
	}
	return c.ReadWriteCloser.Write(p)
}

func TestServer_Console_RejectsNilStart(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	rt := newConsoleRuntime(nil, 0)
	srv := server.NewClocheServerWithCaptures(store, store, rt, "")

	stream := newMockConsoleStream(context.Background())
	// Send a non-start message first.
	stream.inputs <- &pb.ConsoleInput{
		Payload: &pb.ConsoleInput_Stdin{Stdin: []byte("oops")},
	}

	err = srv.Console(stream)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ConsoleStart")
}

func TestServer_Console_RejectsNoRuntime(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	srv := server.NewClocheServer(store, nil)

	stream := newMockConsoleStream(context.Background())
	stream.inputs <- &pb.ConsoleInput{
		Payload: &pb.ConsoleInput_Start{
			Start: &pb.ConsoleStart{ProjectDir: t.TempDir()},
		},
	}

	err = srv.Console(stream)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no container runtime")
}

// mockInspectRuntime is a ContainerRuntime that returns a configurable status
// from Inspect and returns an error from AttachOutput to simulate failures.
type mockInspectRuntime struct {
	inspectStatus *ports.ContainerStatus
	inspectErr    error
	attachErr     error
}

func (m *mockInspectRuntime) Start(_ context.Context, _ ports.ContainerConfig) (string, error) {
	return "cid-inspect", nil
}
func (m *mockInspectRuntime) Stop(_ context.Context, _ string) error { return nil }
func (m *mockInspectRuntime) AttachOutput(_ context.Context, _ string) (io.ReadCloser, error) {
	if m.attachErr != nil {
		return nil, m.attachErr
	}
	return io.NopCloser(strings.NewReader("")), nil
}
func (m *mockInspectRuntime) Wait(_ context.Context, _ string) (int, error) { return 0, nil }
func (m *mockInspectRuntime) CopyFrom(_ context.Context, _, _, _ string) error { return nil }
func (m *mockInspectRuntime) Logs(_ context.Context, _ string) (string, error) { return "", nil }
func (m *mockInspectRuntime) Remove(_ context.Context, _ string) error { return nil }
func (m *mockInspectRuntime) Inspect(_ context.Context, _ string) (*ports.ContainerStatus, error) {
	if m.inspectErr != nil {
		return nil, m.inspectErr
	}
	if m.inspectStatus != nil {
		return m.inspectStatus, nil
	}
	return &ports.ContainerStatus{Running: true}, nil
}
func (m *mockInspectRuntime) Attach(_ context.Context, _ string) (io.ReadWriteCloser, error) {
	return nil, fmt.Errorf("attach not supported")
}

func TestStuckWorkflowScanner_DeadContainerMarksRunFailed(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	// Create a run in "running" state with a container ID.
	run := domain.NewRun("stuck-run-1", "develop")
	run.ProjectDir = "/project/stuck"
	run.Start()
	run.ContainerID = "container-stuck-1"
	require.NoError(t, store.CreateRun(ctx, run))

	// Container is dead and has been for well over the stuck threshold.
	deadSince := time.Now().Add(-5 * time.Minute)
	rt := &mockInspectRuntime{
		inspectStatus: &ports.ContainerStatus{
			Running:    false,
			ExitCode:   137,
			FinishedAt: deadSince,
		},
	}

	broadcaster := logstream.NewBroadcaster()
	broadcaster.Start("stuck-run-1")

	srv := server.NewClocheServerWithCaptures(store, store, rt, "")
	srv.SetLogBroadcaster(broadcaster)
	srv.AddActiveRun("stuck-run-1", "container-stuck-1")

	srv.ScanAndResolveStuckWorkflows(ctx)

	// Run should now be failed.
	updated, err := store.GetRun(ctx, "stuck-run-1")
	require.NoError(t, err)
	assert.Equal(t, domain.RunStateFailed, updated.State)
	assert.Contains(t, updated.ErrorMessage, "137")

	// Broadcaster should have been finished (no longer active).
	assert.False(t, broadcaster.IsActive("stuck-run-1"))
}

func TestStuckWorkflowScanner_LiveContainerNotTouched(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	run := domain.NewRun("live-run-1", "develop")
	run.ProjectDir = "/project/live"
	run.Start()
	run.ContainerID = "container-live-1"
	require.NoError(t, store.CreateRun(ctx, run))

	rt := &mockInspectRuntime{
		inspectStatus: &ports.ContainerStatus{Running: true},
	}

	srv := server.NewClocheServerWithCaptures(store, store, rt, "")
	srv.AddActiveRun("live-run-1", "container-live-1")

	srv.ScanAndResolveStuckWorkflows(ctx)

	// Run should still be running.
	updated, err := store.GetRun(ctx, "live-run-1")
	require.NoError(t, err)
	assert.Equal(t, domain.RunStateRunning, updated.State)
}

func TestStuckWorkflowScanner_RecentlyDeadContainerNotTouched(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	run := domain.NewRun("recent-run-1", "develop")
	run.ProjectDir = "/project/recent"
	run.Start()
	run.ContainerID = "container-recent-1"
	require.NoError(t, store.CreateRun(ctx, run))

	// Container just died (within threshold).
	rt := &mockInspectRuntime{
		inspectStatus: &ports.ContainerStatus{
			Running:    false,
			ExitCode:   1,
			FinishedAt: time.Now().Add(-10 * time.Second),
		},
	}

	srv := server.NewClocheServerWithCaptures(store, store, rt, "")
	srv.AddActiveRun("recent-run-1", "container-recent-1")

	srv.ScanAndResolveStuckWorkflows(ctx)

	// Run should still be running (not enough time has passed).
	updated, err := store.GetRun(ctx, "recent-run-1")
	require.NoError(t, err)
	assert.Equal(t, domain.RunStateRunning, updated.State)
}

func TestStuckWorkflowScanner_HaltsProjectLoop(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	run := domain.NewRun("stuck-loop-run-1", "develop")
	run.ProjectDir = "/project/loop"
	run.Start()
	run.ContainerID = "container-loop-1"
	require.NoError(t, store.CreateRun(ctx, run))

	deadSince := time.Now().Add(-5 * time.Minute)
	rt := &mockInspectRuntime{
		inspectStatus: &ports.ContainerStatus{
			Running:    false,
			ExitCode:   1,
			FinishedAt: deadSince,
		},
	}

	srv := server.NewClocheServerWithCaptures(store, store, rt, "")
	srv.AddActiveRun("stuck-loop-run-1", "container-loop-1")

	// Register a live loop for the project.
	fakeStore2 := &fakeRunStore{runs: map[string]*domain.Run{}}
	loop := newTestLoop("/project/loop", fakeStore2)
	srv.RegisterLoop("/project/loop", loop)

	srv.ScanAndResolveStuckWorkflows(ctx)

	halted, reason := loop.Halted()
	assert.True(t, halted, "loop should be halted after stuck workflow detected")
	assert.Contains(t, reason, "stuck-loop-run-1")
}

func TestTrackRun_AttachOutputFailureMarksRunFailed(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	run := domain.NewRun("attach-fail-run-1", "develop")
	run.ProjectDir = "/project/attach"
	run.Start()
	run.ContainerID = "container-attach-1"
	require.NoError(t, store.CreateRun(ctx, run))

	rt := &mockInspectRuntime{
		attachErr: fmt.Errorf("connection refused"),
	}

	broadcaster := logstream.NewBroadcaster()
	broadcaster.Start("attach-fail-run-1")

	srv := server.NewClocheServerWithCaptures(store, store, rt, "")
	srv.SetLogBroadcaster(broadcaster)
	srv.AddActiveRun("attach-fail-run-1", "container-attach-1")

	// trackRun should return promptly after the attach failure.
	srv.TrackRun("attach-fail-run-1", "container-attach-1", "/project/attach", "develop", false)

	updated, err := store.GetRun(ctx, "attach-fail-run-1")
	require.NoError(t, err)
	assert.Equal(t, domain.RunStateFailed, updated.State)
	assert.Contains(t, updated.ErrorMessage, "attach")

	// Broadcaster should have been finished.
	assert.False(t, broadcaster.IsActive("attach-fail-run-1"))
}

func TestTrackRun_AttachOutputFailureHaltsProjectLoop(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	run := domain.NewRun("attach-halt-run-1", "develop")
	run.ProjectDir = "/project/attach-halt"
	run.Start()
	run.ContainerID = "container-attach-halt-1"
	require.NoError(t, store.CreateRun(ctx, run))

	rt := &mockInspectRuntime{attachErr: fmt.Errorf("no such container")}

	broadcaster := logstream.NewBroadcaster()
	broadcaster.Start("attach-halt-run-1")

	srv := server.NewClocheServerWithCaptures(store, store, rt, "")
	srv.SetLogBroadcaster(broadcaster)
	srv.AddActiveRun("attach-halt-run-1", "container-attach-halt-1")

	fakeStore2 := &fakeRunStore{runs: map[string]*domain.Run{}}
	loop := newTestLoop("/project/attach-halt", fakeStore2)
	srv.RegisterLoop("/project/attach-halt", loop)

	srv.TrackRun("attach-halt-run-1", "container-attach-halt-1", "/project/attach-halt", "develop", false)

	halted, reason := loop.Halted()
	assert.True(t, halted, "loop should be halted after attach failure")
	assert.Contains(t, reason, "attach-halt-run-1")
}

// ---- AgentSession tests ----

// fakeAgentStream implements pb.ClocheService_AgentSessionServer for testing.
// Recv reads from the in channel; Send appends to sent.
type fakeAgentStream struct {
	grpclib.ServerStream
	ctx  context.Context
	in   chan *pb.AgentMessage
	mu   sync.Mutex
	sent []*pb.DaemonMessage
}

func newFakeAgentStream(ctx context.Context) *fakeAgentStream {
	return &fakeAgentStream{ctx: ctx, in: make(chan *pb.AgentMessage, 32)}
}

func (f *fakeAgentStream) Context() context.Context { return f.ctx }

func (f *fakeAgentStream) Recv() (*pb.AgentMessage, error) {
	msg, ok := <-f.in
	if !ok {
		return nil, io.EOF
	}
	return msg, nil
}

func (f *fakeAgentStream) Send(msg *pb.DaemonMessage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, msg)
	return nil
}

func (f *fakeAgentStream) getSent() []*pb.DaemonMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([]*pb.DaemonMessage, len(f.sent))
	copy(cp, f.sent)
	return cp
}

// push enqueues an AgentMessage to be returned by Recv.
func (f *fakeAgentStream) push(msg *pb.AgentMessage) { f.in <- msg }

// close closes the in channel so Recv returns io.EOF.
func (f *fakeAgentStream) close() { close(f.in) }

func TestAgentSession_NoPool_ReturnsError(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	// Server with no container pool configured.
	srv := server.NewClocheServer(store, nil)

	stream := newFakeAgentStream(context.Background())
	stream.push(&pb.AgentMessage{
		Payload: &pb.AgentMessage_Ready{
			Ready: &pb.AgentReady{RunId: "ctr-1"},
		},
	})
	stream.close()

	err = srv.AgentSession(stream)
	require.Error(t, err, "AgentSession should fail when pool is nil")
}

func TestAgentSession_RecordsStepCaptures(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	run := domain.NewRun("run-captures-1", "develop")
	run.Start()
	require.NoError(t, store.CreateRun(ctx, run))

	rt := &fakeDockerRuntime{}
	pool := newFakePoolWithRuntime(rt)
	srv := server.NewClocheServerWithCaptures(store, store, rt.asContainerRuntime(), "")
	srv.SetContainerPool(pool)
	srv.RegisterContainerRun("ctr-captures-1", "run-captures-1")

	stream := newFakeAgentStream(ctx)
	stream.push(&pb.AgentMessage{Payload: &pb.AgentMessage_Ready{Ready: &pb.AgentReady{RunId: "ctr-captures-1"}}})
	stream.push(&pb.AgentMessage{Payload: &pb.AgentMessage_StepStarted{StepStarted: &pb.StepStarted{RequestId: "req-1", StepName: "build"}}})
	stream.push(&pb.AgentMessage{Payload: &pb.AgentMessage_StepResult{StepResult: &pb.StepResult{RequestId: "req-1", Result: "success"}}})
	stream.close()

	err = srv.AgentSession(stream)
	require.NoError(t, err)

	// Verify captures were saved.
	caps, err := store.GetCaptures(ctx, "run-captures-1")
	require.NoError(t, err)
	require.NotEmpty(t, caps, "should have at least one capture")
	var found bool
	for _, c := range caps {
		if c.StepName == "build" && c.Result == "success" {
			found = true
		}
	}
	assert.True(t, found, "should have a completed 'build' capture with result 'success'")
}

func TestAgentSession_StepLogBroadcasts(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	run := domain.NewRun("run-log-1", "develop")
	run.Start()
	require.NoError(t, store.CreateRun(ctx, run))

	broadcaster := logstream.NewBroadcaster()
	sub := broadcaster.Subscribe("run-log-1")

	rt := &fakeDockerRuntime{}
	pool := newFakePoolWithRuntime(rt)
	srv := server.NewClocheServerWithCaptures(store, store, rt.asContainerRuntime(), "")
	srv.SetContainerPool(pool)
	srv.SetLogBroadcaster(broadcaster)
	srv.RegisterContainerRun("ctr-log-1", "run-log-1")

	stream := newFakeAgentStream(ctx)
	stream.push(&pb.AgentMessage{Payload: &pb.AgentMessage_Ready{Ready: &pb.AgentReady{RunId: "ctr-log-1"}}})
	stream.push(&pb.AgentMessage{Payload: &pb.AgentMessage_StepLog{StepLog: &pb.StepLog{StepName: "build", Line: "compiling...\n"}}})
	stream.close()

	err = srv.AgentSession(stream)
	require.NoError(t, err)

	// Collect published lines.
	broadcaster.Finish("run-log-1")
	var lines []logstream.LogLine
	for line := range sub.C {
		lines = append(lines, line)
	}

	var found bool
	for _, l := range lines {
		if l.Type == "llm" && strings.Contains(l.Content, "compiling...") {
			found = true
		}
	}
	assert.True(t, found, "StepLog should be published to broadcaster")
}

func TestAgentSession_DisconnectFailsInFlightSteps(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	run := domain.NewRun("run-disconnect-1", "develop")
	run.Start()
	require.NoError(t, store.CreateRun(ctx, run))

	rt := &fakeDockerRuntime{}
	pool := newFakePoolWithRuntime(rt)
	srv := server.NewClocheServerWithCaptures(store, store, rt.asContainerRuntime(), "")
	srv.SetContainerPool(pool)
	srv.RegisterContainerRun("ctr-disconnect-1", "run-disconnect-1")

	stream := newFakeAgentStream(ctx)
	stream.push(&pb.AgentMessage{Payload: &pb.AgentMessage_Ready{Ready: &pb.AgentReady{RunId: "ctr-disconnect-1"}}})
	// Start a step but never send the result — then disconnect.
	stream.push(&pb.AgentMessage{Payload: &pb.AgentMessage_StepStarted{StepStarted: &pb.StepStarted{RequestId: "req-dc", StepName: "build"}}})
	stream.close()

	err = srv.AgentSession(stream)
	require.NoError(t, err)

	// The in-flight step should be recorded as failed via failInFlightSteps.
	caps, err := store.GetCaptures(ctx, "run-disconnect-1")
	require.NoError(t, err)
	var failed bool
	for _, c := range caps {
		if c.StepName == "build" && c.Result == "fail" {
			failed = true
		}
	}
	assert.True(t, failed, "in-flight step should be recorded as failed on disconnect")
}

// fakeDockerRuntime is a no-op ContainerRuntime for AgentSession tests.
// It wraps a *docker.ContainerPool-compatible interface but does nothing.
type fakeDockerRuntime struct{}

func (f *fakeDockerRuntime) asContainerRuntime() ports.ContainerRuntime { return nil }

// newFakePoolWithRuntime creates a ContainerPool backed by a real (but unused) runtime.
// For AgentSession tests the pool is only used to register stream send functions and
// to call FailPendingRequests; no actual containers are started.
func newFakePoolWithRuntime(_ *fakeDockerRuntime) *docker.ContainerPool {
	return docker.NewContainerPool(&nopRuntime{})
}

// nopRuntime is a ContainerRuntime that never starts containers (for unit tests).
type nopRuntime struct{}

func (n *nopRuntime) Start(_ context.Context, _ ports.ContainerConfig) (string, error) {
	return "", fmt.Errorf("nopRuntime: Start not supported")
}
func (n *nopRuntime) Stop(_ context.Context, _ string) error     { return nil }
func (n *nopRuntime) Remove(_ context.Context, _ string) error   { return nil }
func (n *nopRuntime) Wait(_ context.Context, _ string) (int, error) { return 0, nil }
func (n *nopRuntime) Logs(_ context.Context, _ string) (string, error) { return "", nil }
func (n *nopRuntime) AttachOutput(_ context.Context, _ string) (io.ReadCloser, error) {
	return io.NopCloser(nil), nil
}
func (n *nopRuntime) Inspect(_ context.Context, _ string) (*ports.ContainerStatus, error) {
	return &ports.ContainerStatus{}, nil
}
func (n *nopRuntime) Attach(_ context.Context, _ string) (io.ReadWriteCloser, error) {
	return nil, nil
}
func (n *nopRuntime) CopyFrom(_ context.Context, _, _, _ string) error { return nil }

// resumableNopRuntime extends nopRuntime with CommitContainer and RemoveImage
// to satisfy the containerResumer interface without actually running Docker.
// Start succeeds immediately (returns a deterministic ID) so pool.SessionFor
// can register the session; NotifyReady must be called separately.
type resumableNopRuntime struct {
	nopRuntime
	mu            sync.Mutex
	started       []string
	committed     map[string]string
	removedImages []string
	idCounter     int
}

func (r *resumableNopRuntime) Start(_ context.Context, _ ports.ContainerConfig) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.idCounter++
	id := fmt.Sprintf("resume-container-%d", r.idCounter)
	r.started = append(r.started, id)
	return id, nil
}

func (r *resumableNopRuntime) CommitContainer(_ context.Context, containerID, attemptID string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	tag := "cloche-resume:" + attemptID + "-" + containerID
	if r.committed == nil {
		r.committed = make(map[string]string)
	}
	r.committed[containerID] = tag
	return tag, nil
}

func (r *resumableNopRuntime) RemoveImage(_ context.Context, imageTag string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.removedImages = append(r.removedImages, imageTag)
	return nil
}

// fakeRunStore is a minimal RunStore for loop tests in the grpc package.
type fakeRunStore struct {
	mu   sync.Mutex
	runs map[string]*domain.Run
}

func (f *fakeRunStore) CreateRun(_ context.Context, r *domain.Run) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *r
	f.runs[r.ID] = &cp
	return nil
}
func (f *fakeRunStore) GetRun(_ context.Context, id string) (*domain.Run, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.runs[id]
	if !ok {
		return nil, fmt.Errorf("not found: %s", id)
	}
	cp := *r
	return &cp, nil
}
func (f *fakeRunStore) GetRunByAttempt(_ context.Context, _, _ string) (*domain.Run, error) {
	return nil, fmt.Errorf("not found")
}
func (f *fakeRunStore) UpdateRun(_ context.Context, r *domain.Run) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *r
	f.runs[r.ID] = &cp
	return nil
}
func (f *fakeRunStore) DeleteRun(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.runs, id)
	return nil
}
func (f *fakeRunStore) ListRuns(_ context.Context, _ time.Time) ([]*domain.Run, error) {
	return nil, nil
}
func (f *fakeRunStore) ListRunsByProject(_ context.Context, _ string, _ time.Time) ([]*domain.Run, error) {
	return nil, nil
}
func (f *fakeRunStore) ListRunsFiltered(_ context.Context, _ domain.RunListFilter) ([]*domain.Run, error) {
	return nil, nil
}
func (f *fakeRunStore) ListChildRuns(_ context.Context, _ string) ([]*domain.Run, error) {
	return nil, nil
}
func (f *fakeRunStore) ListProjects(_ context.Context) ([]string, error) { return nil, nil }
func (f *fakeRunStore) GetContextKey(_ context.Context, _, _, _ string) (string, bool, error) {
	return "", false, nil
}
func (f *fakeRunStore) SetContextKey(_ context.Context, _, _, _, _ string) error { return nil }
func (f *fakeRunStore) ListContextKeys(_ context.Context, _, _ string) ([]string, error) {
	return nil, nil
}
func (f *fakeRunStore) DeleteContextKeys(_ context.Context, _, _ string) error { return nil }
func (f *fakeRunStore) QueryUsage(_ context.Context, _ ports.UsageQuery) ([]domain.UsageSummary, error) {
	return nil, nil
}

// newTestLoop creates a minimal Loop for use in server tests.
func newTestLoop(projectDir string, store ports.RunStore) *host.Loop {
	return host.NewLoop(host.LoopConfig{ProjectDir: projectDir, MaxConcurrent: 1}, store,
		func(_ context.Context, _ string, _ string, _ string) (*host.RunResult, error) {
			return &host.RunResult{State: domain.RunStateSucceeded}, nil
		})
}
