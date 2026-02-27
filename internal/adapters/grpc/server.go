package grpc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	pb "github.com/cloche-dev/cloche/api/clochepb"
	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/evolution"
	"github.com/cloche-dev/cloche/internal/ports"
	"github.com/cloche-dev/cloche/internal/protocol"
	rpcgrpc "google.golang.org/grpc"
)

type ClocheServer struct {
	pb.UnimplementedClocheServiceServer
	store        ports.RunStore
	captures     ports.CaptureStore
	container    ports.ContainerRuntime
	defaultImage string
	evolution    *evolution.Trigger
	shutdownFn   func()
	mu           sync.Mutex
	runIDs       map[string]string // run_id -> container_id
}

func NewClocheServer(store ports.RunStore, container ports.ContainerRuntime) *ClocheServer {
	return &ClocheServer{
		store:     store,
		container: container,
		runIDs:    make(map[string]string),
	}
}

func NewClocheServerWithCaptures(store ports.RunStore, captures ports.CaptureStore, container ports.ContainerRuntime, defaultImage string) *ClocheServer {
	return &ClocheServer{
		store:        store,
		captures:     captures,
		container:    container,
		defaultImage: defaultImage,
		runIDs:       make(map[string]string),
	}
}

// SetEvolution attaches an evolution trigger to the server.
func (s *ClocheServer) SetEvolution(trigger *evolution.Trigger) {
	s.evolution = trigger
}

// SetShutdownFunc sets the callback invoked when the Shutdown RPC is called.
func (s *ClocheServer) SetShutdownFunc(fn func()) {
	s.shutdownFn = fn
}

func (s *ClocheServer) RunWorkflow(ctx context.Context, req *pb.RunWorkflowRequest) (*pb.RunWorkflowResponse, error) {
	if s.container == nil {
		return nil, fmt.Errorf("no container runtime configured")
	}

	// Generate a unique run ID, retrying on collision
	var runID string
	for attempts := 0; attempts < 10; attempts++ {
		runID = domain.GenerateRunID(req.WorkflowName)
		existing, err := s.store.GetRun(ctx, runID)
		if err != nil {
			break // ID is free
		}
		// Reuse if completed more than 1 hour ago
		if !existing.CompletedAt.IsZero() && time.Since(existing.CompletedAt) > time.Hour {
			_ = s.store.DeleteRun(ctx, runID)
			break
		}
	}

	// Write prompt to .cloche/<run-id>/prompt.txt (run-specific to avoid conflicts)
	if req.Prompt != "" {
		clocheDir := filepath.Join(req.ProjectDir, ".cloche", runID)
		if err := os.MkdirAll(clocheDir, 0755); err != nil {
			return nil, fmt.Errorf("creating .cloche dir: %w", err)
		}
		if err := os.WriteFile(filepath.Join(clocheDir, "prompt.txt"), []byte(req.Prompt), 0644); err != nil {
			return nil, fmt.Errorf("writing prompt: %w", err)
		}
	}
	run := domain.NewRun(runID, req.WorkflowName)
	run.ProjectDir = req.ProjectDir
	if err := s.store.CreateRun(ctx, run); err != nil {
		return nil, fmt.Errorf("creating run: %w", err)
	}

	// Resolve image: request-level override, then server default
	image := req.Image
	if image == "" {
		image = s.defaultImage
	}

	// Launch container start + tracking in background so the RPC returns immediately.
	// The run stays in "pending" state until the container is up.
	go s.launchAndTrack(runID, image, req.KeepContainer, req)

	return &pb.RunWorkflowResponse{RunId: runID}, nil
}

// launchAndTrack starts the container and then tracks it to completion.
// It runs in a background goroutine with its own context, independent of the
// RPC context which may be cancelled after RunWorkflow returns.
func (s *ClocheServer) launchAndTrack(runID, image string, keepContainer bool, req *pb.RunWorkflowRequest) {
	ctx := context.Background()

	containerID, err := s.container.Start(ctx, ports.ContainerConfig{
		Image:        image,
		WorkflowName: req.WorkflowName,
		ProjectDir:   req.ProjectDir,
		RunID:        runID,
		NetworkAllow: []string{"*"},
	})
	if err != nil {
		run, _ := s.store.GetRun(ctx, runID)
		if run != nil {
			run.Fail(fmt.Sprintf("failed to start container: %v", err))
			_ = s.store.UpdateRun(ctx, run)
		}
		log.Printf("run %s: failed to start container: %v", runID, err)
		return
	}

	s.mu.Lock()
	s.runIDs[runID] = containerID
	s.mu.Unlock()

	run, _ := s.store.GetRun(ctx, runID)
	if run != nil {
		run.Start()
		run.ContainerID = containerID
		_ = s.store.UpdateRun(ctx, run)
	}

	s.trackRun(runID, containerID, req.ProjectDir, req.WorkflowName, keepContainer)
}

func (s *ClocheServer) trackRun(runID, containerID, projectDir, workflowName string, keepContainer bool) {
	ctx := context.Background()

	// Attach to agent output
	reader, err := s.container.AttachOutput(ctx, containerID)
	if err != nil {
		log.Printf("failed to attach to output for run %s: %v", runID, err)
		return
	}

	// Parse JSON-lines status messages
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		var msg protocol.StatusMessage
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}

		run, err := s.store.GetRun(ctx, runID)
		if err != nil {
			continue
		}

		switch msg.Type {
		case protocol.MsgStepStarted:
			run.RecordStepStart(msg.StepName)
			if s.captures != nil {
				_ = s.captures.SaveCapture(ctx, runID, &domain.StepExecution{
					StepName:  msg.StepName,
					StartedAt: msg.Timestamp,
				})
			}
		case protocol.MsgStepCompleted:
			run.RecordStepComplete(msg.StepName, msg.Result)
			if s.captures != nil {
				_ = s.captures.SaveCapture(ctx, runID, &domain.StepExecution{
					StepName:    msg.StepName,
					Result:      msg.Result,
					CompletedAt: msg.Timestamp,
				})
			}
		case protocol.MsgRunCompleted:
			if msg.Result == "succeeded" {
				run.Complete(domain.RunStateSucceeded)
			} else {
				run.Complete(domain.RunStateFailed)
			}
		}

		_ = s.store.UpdateRun(ctx, run)
	}
	reader.Close()

	// Wait for process exit
	exitCode, err := s.container.Wait(ctx, containerID)
	if err != nil {
		log.Printf("error waiting for run %s: %v", runID, err)
	}

	// Extract step output files from container before it's removed
	outputDst := filepath.Join(projectDir, ".cloche", runID, "output")
	if err := os.MkdirAll(outputDst, 0755); err == nil {
		if cpErr := s.container.CopyFrom(ctx, containerID, "/workspace/.cloche/output/.", outputDst); cpErr != nil {
			log.Printf("run %s: failed to extract output: %v", runID, cpErr)
		}
	}

	// Capture full container stdout/stderr before the container is removed
	if logs, logErr := s.container.Logs(ctx, containerID); logErr == nil && logs != "" {
		containerLogPath := filepath.Join(outputDst, "container.log")
		if writeErr := os.WriteFile(containerLogPath, []byte(logs), 0644); writeErr != nil {
			log.Printf("run %s: failed to write container.log: %v", runID, writeErr)
		}
	}

	// Ensure run is marked complete
	run, err := s.store.GetRun(ctx, runID)
	if err != nil {
		return
	}
	if run.State == domain.RunStateRunning {
		if exitCode == 0 {
			run.Complete(domain.RunStateSucceeded)
		} else {
			run.Fail(fmt.Sprintf("container exited with code %d", exitCode))
		}
		_ = s.store.UpdateRun(ctx, run)
	}

	// Fire evolution trigger if configured
	if s.evolution != nil {
		s.evolution.Fire(projectDir, workflowName, runID)
	}

	// Auto-remove container unless --keep-container was set
	if keepContainer {
		log.Printf("run %s: keeping container %s (--keep-container)", runID, containerID)
	} else {
		if err := s.container.Remove(ctx, containerID); err != nil {
			log.Printf("run %s: failed to remove container %s: %v", runID, containerID, err)
		} else {
			log.Printf("run %s: removed container %s", runID, containerID)
		}
	}

	// Cleanup mapping
	s.mu.Lock()
	delete(s.runIDs, runID)
	s.mu.Unlock()
}

func (s *ClocheServer) ListRuns(ctx context.Context, req *pb.ListRunsRequest) (*pb.ListRunsResponse, error) {
	var since time.Time
	if !req.All {
		since = time.Now().Add(-1 * time.Hour)
	}
	runs, err := s.store.ListRuns(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("listing runs: %w", err)
	}

	resp := &pb.ListRunsResponse{}
	for _, run := range runs {
		resp.Runs = append(resp.Runs, &pb.RunSummary{
			RunId:        run.ID,
			WorkflowName: run.WorkflowName,
			State:        string(run.State),
			StartedAt:    run.StartedAt.String(),
			ErrorMessage: run.ErrorMessage,
			ContainerId:  run.ContainerID,
		})
	}
	return resp, nil
}

func (s *ClocheServer) GetStatus(ctx context.Context, req *pb.GetStatusRequest) (*pb.GetStatusResponse, error) {
	run, err := s.store.GetRun(ctx, req.RunId)
	if err != nil {
		return nil, fmt.Errorf("getting run: %w", err)
	}

	resp := &pb.GetStatusResponse{
		RunId:        run.ID,
		WorkflowName: run.WorkflowName,
		State:        string(run.State),
		CurrentStep:  strings.Join(run.ActiveSteps, ","),
		ErrorMessage: run.ErrorMessage,
		ContainerId:  run.ContainerID,
	}

	// Load step executions from captures store if available
	if s.captures != nil {
		captures, err := s.captures.GetCaptures(ctx, req.RunId)
		if err == nil {
			for _, exec := range captures {
				resp.StepExecutions = append(resp.StepExecutions, &pb.StepExecutionStatus{
					StepName:    exec.StepName,
					Result:      exec.Result,
					StartedAt:   exec.StartedAt.String(),
					CompletedAt: exec.CompletedAt.String(),
				})
			}
		}
	} else {
		for _, exec := range run.StepExecutions {
			resp.StepExecutions = append(resp.StepExecutions, &pb.StepExecutionStatus{
				StepName:    exec.StepName,
				Result:      exec.Result,
				StartedAt:   exec.StartedAt.String(),
				CompletedAt: exec.CompletedAt.String(),
			})
		}
	}

	return resp, nil
}

func (s *ClocheServer) StreamLogs(req *pb.StreamLogsRequest, stream rpcgrpc.ServerStreamingServer[pb.LogEntry]) error {
	ctx := stream.Context()

	// Verify run exists
	run, err := s.store.GetRun(ctx, req.RunId)
	if err != nil {
		return fmt.Errorf("run %q not found: %w", req.RunId, err)
	}

	if s.captures == nil {
		return fmt.Errorf("captures store not configured")
	}

	// Get persisted captures
	captures, err := s.captures.GetCaptures(ctx, req.RunId)
	if err != nil {
		return fmt.Errorf("getting captures: %w", err)
	}

	for _, exec := range captures {
		// Captures are stored as separate rows: one for step_started (no Result)
		// and one for step_completed (has Result).
		if exec.Result == "" {
			entry := &pb.LogEntry{
				Type:      "step_started",
				StepName:  exec.StepName,
				Timestamp: exec.StartedAt.String(),
			}
			if err := stream.Send(entry); err != nil {
				return err
			}
		} else {
			// Read step output from file, falling back to container.log
			var output string
			outputPath := filepath.Join(run.ProjectDir, ".cloche", req.RunId, "output", exec.StepName+".log")
			if data, err := os.ReadFile(outputPath); err == nil && len(data) > 0 {
				output = string(data)
			} else {
				containerLogPath := filepath.Join(run.ProjectDir, ".cloche", req.RunId, "output", "container.log")
				if data, err := os.ReadFile(containerLogPath); err == nil {
					output = string(data)
				}
			}
			entry := &pb.LogEntry{
				Type:      "step_completed",
				StepName:  exec.StepName,
				Result:    exec.Result,
				Timestamp: exec.CompletedAt.String(),
				Message:   output,
			}
			if err := stream.Send(entry); err != nil {
				return err
			}
		}
	}

	// Send run completion entry
	if run.State != domain.RunStateRunning && run.State != domain.RunStatePending {
		if err := stream.Send(&pb.LogEntry{
			Type:      "run_completed",
			Result:    string(run.State),
			Timestamp: run.CompletedAt.String(),
			Message:   run.ErrorMessage,
		}); err != nil {
			return err
		}
	}

	return nil
}

func (s *ClocheServer) StopRun(ctx context.Context, req *pb.StopRunRequest) (*pb.StopRunResponse, error) {
	s.mu.Lock()
	containerID, ok := s.runIDs[req.RunId]
	s.mu.Unlock()

	if !ok {
		return nil, fmt.Errorf("run %q not found or already completed", req.RunId)
	}

	if err := s.container.Stop(ctx, containerID); err != nil {
		return nil, fmt.Errorf("stopping run: %w", err)
	}

	// Mark as cancelled in store
	run, err := s.store.GetRun(ctx, req.RunId)
	if err == nil {
		run.Complete(domain.RunStateCancelled)
		_ = s.store.UpdateRun(ctx, run)
	}

	return &pb.StopRunResponse{}, nil
}

func (s *ClocheServer) Shutdown(ctx context.Context, req *pb.ShutdownRequest) (*pb.ShutdownResponse, error) {
	if s.shutdownFn == nil {
		return nil, fmt.Errorf("shutdown not configured")
	}
	go s.shutdownFn()
	return &pb.ShutdownResponse{}, nil
}
