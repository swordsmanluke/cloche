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

func (s *ClocheServer) RunWorkflow(ctx context.Context, req *pb.RunWorkflowRequest) (*pb.RunWorkflowResponse, error) {
	if s.container == nil {
		return nil, fmt.Errorf("no container runtime configured")
	}

	// Create run in store
	runID := fmt.Sprintf("run-%d", time.Now().UnixNano())

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
	if err := s.store.CreateRun(ctx, run); err != nil {
		return nil, fmt.Errorf("creating run: %w", err)
	}

	// Resolve image: request-level override, then server default
	image := req.Image
	if image == "" {
		image = s.defaultImage
	}

	// Start agent process
	containerID, err := s.container.Start(ctx, ports.ContainerConfig{
		Image:        image,
		WorkflowName: req.WorkflowName,
		ProjectDir:   req.ProjectDir,
		RunID:        runID,
		NetworkAllow: []string{"*"},
	})
	if err != nil {
		run.Complete(domain.RunStateFailed)
		_ = s.store.UpdateRun(ctx, run)
		return nil, fmt.Errorf("starting agent: %w", err)
	}

	// Track the mapping
	s.mu.Lock()
	s.runIDs[runID] = containerID
	s.mu.Unlock()

	// Mark run as started
	run.Start()
	_ = s.store.UpdateRun(ctx, run)

	// Launch background goroutine to track status
	go s.trackRun(runID, containerID, req.ProjectDir, req.WorkflowName)

	return &pb.RunWorkflowResponse{RunId: runID}, nil
}

func (s *ClocheServer) trackRun(runID, containerID, projectDir, workflowName string) {
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
					StepName:   msg.StepName,
					StartedAt:  msg.Timestamp,
					PromptText: msg.PromptText,
				})
			}
		case protocol.MsgStepCompleted:
			run.RecordStepComplete(msg.StepName, msg.Result)
			if s.captures != nil {
				_ = s.captures.SaveCapture(ctx, runID, &domain.StepExecution{
					StepName:      msg.StepName,
					Result:        msg.Result,
					CompletedAt:   msg.Timestamp,
					AgentOutput:   msg.AgentOutput,
					AttemptNumber: msg.AttemptNumber,
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

	// Ensure run is marked complete
	run, err := s.store.GetRun(ctx, runID)
	if err != nil {
		return
	}
	if run.State == domain.RunStateRunning {
		if exitCode == 0 {
			run.Complete(domain.RunStateSucceeded)
		} else {
			run.Complete(domain.RunStateFailed)
		}
		_ = s.store.UpdateRun(ctx, run)
	}

	// Fire evolution trigger if configured
	if s.evolution != nil {
		s.evolution.Fire(projectDir, workflowName, runID)
	}

	// Cleanup mapping
	s.mu.Lock()
	delete(s.runIDs, runID)
	s.mu.Unlock()
}

func (s *ClocheServer) ListRuns(ctx context.Context, req *pb.ListRunsRequest) (*pb.ListRunsResponse, error) {
	runs, err := s.store.ListRuns(ctx)
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
		// Captures are stored as separate rows: one for step_started (has PromptText,
		// no Result) and one for step_completed (has Result and AgentOutput).
		if exec.Result == "" {
			// This is a step_started capture
			entry := &pb.LogEntry{
				Type:      "step_started",
				StepName:  exec.StepName,
				Timestamp: exec.StartedAt.String(),
				Message:   exec.PromptText,
			}
			if err := stream.Send(entry); err != nil {
				return err
			}
		} else {
			// This is a step_completed capture
			entry := &pb.LogEntry{
				Type:      "step_completed",
				StepName:  exec.StepName,
				Result:    exec.Result,
				Timestamp: exec.CompletedAt.String(),
				Message:   exec.AgentOutput,
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
