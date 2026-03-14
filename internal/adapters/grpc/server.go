package grpc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	pb "github.com/cloche-dev/cloche/api/clochepb"
	"github.com/cloche-dev/cloche/internal/adapters/docker"
	"github.com/cloche-dev/cloche/internal/adapters/web"
	"github.com/cloche-dev/cloche/internal/config"
	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/dsl"
	"github.com/cloche-dev/cloche/internal/evolution"
	"github.com/cloche-dev/cloche/internal/host"
	"github.com/cloche-dev/cloche/internal/logstream"
	"github.com/cloche-dev/cloche/internal/ports"
	"github.com/cloche-dev/cloche/internal/protocol"
	"github.com/cloche-dev/cloche/internal/version"
	rpcgrpc "google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type ClocheServer struct {
	pb.UnimplementedClocheServiceServer
	store        ports.RunStore
	captures     ports.CaptureStore
	logStore     ports.LogStore
	container    ports.ContainerRuntime
	defaultImage string
	evolution    *evolution.Trigger
	logBroadcast *logstream.Broadcaster
	shutdownFn   func()
	mu           sync.Mutex
	runIDs       map[string]string      // run_id -> container_id
	loops        map[string]*host.Loop  // project_dir -> orchestration loop
}

func NewClocheServer(store ports.RunStore, container ports.ContainerRuntime) *ClocheServer {
	return &ClocheServer{
		store:     store,
		container: container,
		runIDs:    make(map[string]string),
		loops:     make(map[string]*host.Loop),
	}
}

func NewClocheServerWithCaptures(store ports.RunStore, captures ports.CaptureStore, container ports.ContainerRuntime, defaultImage string) *ClocheServer {
	return &ClocheServer{
		store:        store,
		captures:     captures,
		container:    container,
		defaultImage: defaultImage,
		runIDs:       make(map[string]string),
		loops:        make(map[string]*host.Loop),
	}
}

// SetLogStore attaches a log store to the server for indexing extracted log files.
func (s *ClocheServer) SetLogStore(ls ports.LogStore) {
	s.logStore = ls
}


// SetEvolution attaches an evolution trigger to the server.
func (s *ClocheServer) SetEvolution(trigger *evolution.Trigger) {
	s.evolution = trigger
}

// SetLogBroadcaster attaches a log broadcaster for live-streaming LLM output.
func (s *ClocheServer) SetLogBroadcaster(b *logstream.Broadcaster) {
	s.logBroadcast = b
}

// SetShutdownFunc sets the callback invoked when the Shutdown RPC is called.
func (s *ClocheServer) SetShutdownFunc(fn func()) {
	s.shutdownFn = fn
}

func (s *ClocheServer) RunWorkflow(ctx context.Context, req *pb.RunWorkflowRequest) (*pb.RunWorkflowResponse, error) {
	// Check if this is a host workflow (has host {} block).
	if hostWFs, err := host.FindHostWorkflows(req.ProjectDir); err == nil {
		if _, isHost := hostWFs[req.WorkflowName]; isHost {
			return s.runHostWorkflow(ctx, req)
		}
	}

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
	run.Title = req.Title
	run.TaskID = req.IssueId
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

// runHostWorkflow dispatches a host workflow via the host runner, returning
// immediately while the workflow runs in a background goroutine.
func (s *ClocheServer) runHostWorkflow(ctx context.Context, req *pb.RunWorkflowRequest) (*pb.RunWorkflowResponse, error) {
	runID := domain.GenerateRunID(req.WorkflowName)

	runner := &host.Runner{
		Dispatcher:   s,
		Store:        s.store,
		Captures:     s.captures,
		LogBroadcast: s.logBroadcast,
		TaskID:       req.IssueId,
	}

	go func() {
		runner.RunNamedWithID(context.Background(), req.ProjectDir, req.WorkflowName, runID)
	}()

	return &pb.RunWorkflowResponse{RunId: runID}, nil
}

// launchAndTrack starts the container and then tracks it to completion.
// It runs in a background goroutine with its own context, independent of the
// RPC context which may be cancelled after RunWorkflow returns.
func (s *ClocheServer) launchAndTrack(runID, image string, keepContainer bool, req *pb.RunWorkflowRequest) {
	ctx := context.Background()

	// Auto-rebuild image if the project Dockerfile has changed since last build.
	if ensurer, ok := s.container.(ports.ImageEnsurer); ok {
		if err := ensurer.EnsureImage(ctx, req.ProjectDir, image); err != nil {
			run, _ := s.store.GetRun(ctx, runID)
			if run != nil {
				run.Fail(fmt.Sprintf("failed to ensure image: %v", err))
				_ = s.store.UpdateRun(ctx, run)
			}
			log.Printf("run %s: failed to ensure image: %v", runID, err)
				return
		}
	}

	baseSHA := gitHEAD(req.ProjectDir)

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
		run.BaseSHA = baseSHA
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
	var reportedResult string // captured from MsgRunCompleted, persisted after branch extraction
	var reportedError string  // captured from MsgError, used to set ErrorMessage on failed runs
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024) // 1MB max to handle large log messages
	for scanner.Scan() {
		var msg protocol.StatusMessage
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}

		// Fast path: log messages don't need store interaction.
		if msg.Type == protocol.MsgLog {
			if s.logBroadcast != nil {
				s.logBroadcast.Publish(runID, logstream.LogLine{
					Timestamp: msg.Timestamp.Format(time.RFC3339),
					Type:      "llm",
					Content:   msg.Message,
					StepName:  msg.StepName,
				})
			}
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
			if s.logBroadcast != nil {
				s.logBroadcast.Publish(runID, logstream.LogLine{
					Timestamp: msg.Timestamp.Format(time.RFC3339),
					Type:      "status",
					Content:   "step_started: " + msg.StepName,
					StepName:  msg.StepName,
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
			if s.logBroadcast != nil {
				s.logBroadcast.Publish(runID, logstream.LogLine{
					Timestamp: msg.Timestamp.Format(time.RFC3339),
					Type:      "status",
					Content:   "step_completed: " + msg.StepName + " -> " + msg.Result,
					StepName:  msg.StepName,
				})
			}
		case protocol.MsgRunTitle:
			if run.Title == "" {
				run.Title = msg.Message
			}
		case protocol.MsgError:
			reportedError = msg.Message
			continue // Don't persist; used during finalization
		case protocol.MsgRunCompleted:
			reportedResult = msg.Result
			continue // Don't persist terminal state yet; branch extraction must finish first
		}

		_ = s.store.UpdateRun(ctx, run)
	}
	reader.Close()

	// Signal live-stream subscribers that this run is done.
	if s.logBroadcast != nil {
		s.logBroadcast.Finish(runID)
	}

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

	// Index extracted log files in the log store
	if s.logStore != nil {
		s.indexLogFiles(ctx, runID, outputDst)
	}

	// Extract results to git branch BEFORE finalizing state, so that
	// WaitRun callers (e.g. host workflow merge step) see branches exist
	// before the run is marked complete.
	resultLabel := reportedResult
	if resultLabel == "" {
		if exitCode == 0 {
			resultLabel = "succeeded"
		} else {
			resultLabel = "failed"
		}
	}
	{
		extractRun, _ := s.store.GetRun(ctx, runID)
		if extractRun != nil && extractRun.BaseSHA != "" {
			log.Printf("run %s: extracting results to branch cloche/%s (baseSHA=%s)", runID, runID, extractRun.BaseSHA)
			if err := docker.ExtractResults(ctx, containerID, extractRun.ProjectDir, runID, extractRun.BaseSHA, workflowName, resultLabel); err != nil {
				log.Printf("run %s: failed to extract results to branch: %v", runID, err)
			} else {
				log.Printf("run %s: branch cloche/%s created successfully", runID, runID)
			}
		} else {
			log.Printf("run %s: skipping branch extraction (baseSHA empty or run not found)", runID)
		}
	}

	// Now finalize run state
	run, err := s.store.GetRun(ctx, runID)
	if err != nil {
		return
	}
	if run.State == domain.RunStateRunning {
		if reportedResult == "succeeded" {
			run.Complete(domain.RunStateSucceeded)
		} else if reportedResult != "" {
			if reportedError != "" {
				run.Fail(reportedError)
			} else {
				run.Complete(domain.RunStateFailed)
			}
		} else if exitCode == 0 {
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

	// Merge is handled as a workflow step in host.cloche, not here.

	// Container retention policy:
	// - Failed runs: always keep the container for debugging
	// - Successful runs + --keep-container: keep the container
	// - Successful runs without --keep-container: remove the container
	runFinal, _ := s.store.GetRun(ctx, runID)
	runFailed := runFinal != nil && (runFinal.State == domain.RunStateFailed || runFinal.State == domain.RunStateCancelled)

	if keepContainer || runFailed {
		reason := "--keep-container"
		if runFailed {
			reason = "run failed"
		}
		log.Printf("run %s: keeping container %s (%s)", runID, containerID, reason)
		if runFinal != nil {
			runFinal.ContainerKept = true
			_ = s.store.UpdateRun(ctx, runFinal)
		}
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
	filter := domain.RunListFilter{
		ProjectDir: req.ProjectDir,
		State:      domain.RunState(req.State),
		TaskID:     req.TaskId,
		Limit:      int(req.Limit),
	}

	// When no project filter and not --all, default to last hour
	if filter.ProjectDir == "" && !req.All {
		filter.Since = time.Now().Add(-1 * time.Hour)
	}

	runs, err := s.store.ListRunsFiltered(ctx, filter)
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
			Title:        run.Title,
			IsHost:       run.IsHost,
			ProjectDir:   run.ProjectDir,
			TaskId:       run.TaskID,
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
		Title:        run.Title,
		IsHost:       run.IsHost,
	}

	// Check container liveness
	if run.ContainerID != "" && s.container != nil {
		if cs, err := s.container.Inspect(ctx, run.ContainerID); err == nil {
			resp.ContainerAlive = cs.Running
			if !cs.Running && !cs.FinishedAt.IsZero() {
				resp.ContainerDeadSince = cs.FinishedAt.Format(time.RFC3339Nano)
			}
		}
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

	_ = followFromContext(ctx) // follow is implicit for active runs
	limit := limitFromContext(ctx)

	// Verify run exists
	run, err := s.store.GetRun(ctx, req.RunId)
	if err != nil {
		return fmt.Errorf("run %q not found: %w", req.RunId, err)
	}

	// If step_name or log_type filter is set, serve content directly from the log index
	if req.StepName != "" || req.LogType != "" {
		return s.streamFilteredLogs(ctx, req, run, stream, limit)
	}

	isActive := run.State == domain.RunStateRunning || run.State == domain.RunStatePending

	// Active runs always stream live output (follow mode is implicit).
	// The -f flag is accepted but redundant for active runs.
	if isActive && s.logBroadcast != nil {
		return s.streamFollowLogs(req.RunId, run, stream, limit)
	}

	// Check for full.log first — if it exists, serve it as the unified log
	fullLogPath := filepath.Join(run.ProjectDir, ".cloche", req.RunId, "output", "full.log")
	if data, readErr := os.ReadFile(fullLogPath); readErr == nil && len(data) > 0 {
		msg := applyLimit(string(data), limit)
		if err := stream.Send(&pb.LogEntry{
			Type:    "full_log",
			Message: msg,
		}); err != nil {
			return err
		}
		if !isActive {
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

	if s.captures == nil {
		return fmt.Errorf("captures store not configured")
	}

	// Fall back to capture-based streaming
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
			// Read step output from per-step output file.
			// Container runs write .log, host runs write .out — try both.
			var output string
			for _, ext := range []string{".log", ".out"} {
				outputPath := filepath.Join(run.ProjectDir, ".cloche", req.RunId, "output", exec.StepName+ext)
				if data, err := os.ReadFile(outputPath); err == nil && len(data) > 0 {
					output = string(data)
					break
				}
			}
			entry := &pb.LogEntry{
				Type:      "step_completed",
				StepName:  exec.StepName,
				Result:    exec.Result,
				Timestamp: exec.CompletedAt.String(),
				Message:   applyLimit(output, limit),
			}
			if err := stream.Send(entry); err != nil {
				return err
			}
		}
	}

	// Send run completion entry
	if !isActive {
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

// followFromContext checks for the follow flag in gRPC metadata.
func followFromContext(ctx context.Context) bool {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return false
	}
	vals := md.Get("x-cloche-follow")
	return len(vals) > 0 && vals[0] == "true"
}

// limitFromContext reads the line limit from gRPC metadata.
// Returns 0 when no limit is set.
func limitFromContext(ctx context.Context) int {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return 0
	}
	vals := md.Get("x-cloche-limit")
	if len(vals) == 0 {
		return 0
	}
	n, err := strconv.Atoi(vals[0])
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// applyLimit returns the last n lines of content. If n <= 0, returns content unchanged.
func applyLimit(content string, n int) string {
	if n <= 0 {
		return content
	}
	lines := strings.Split(content, "\n")
	// Remove trailing empty line from final newline
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) <= n {
		return content
	}
	return strings.Join(lines[len(lines)-n:], "\n") + "\n"
}

// streamFollowLogs sends existing log content then tails live output from the
// broadcaster. It combines snapshot + live streaming (like tail -f).
func (s *ClocheServer) streamFollowLogs(runID string, run *domain.Run, stream rpcgrpc.ServerStreamingServer[pb.LogEntry], limit int) error {
	// Subscribe first so we don't miss lines written between read and subscribe.
	sub := s.logBroadcast.Subscribe(runID)
	defer s.logBroadcast.Unsubscribe(runID, sub)

	// Send existing full.log content if available.
	fullLogPath := filepath.Join(run.ProjectDir, ".cloche", runID, "output", "full.log")
	if data, err := os.ReadFile(fullLogPath); err == nil && len(data) > 0 {
		msg := applyLimit(string(data), limit)
		if err := stream.Send(&pb.LogEntry{
			Type:    "full_log",
			Message: msg,
		}); err != nil {
			return err
		}
	}

	// Now tail live output from the broadcaster.
	ctx := stream.Context()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case line, ok := <-sub.C:
			if !ok {
				// Run completed — send terminal entry.
				r, err := s.store.GetRun(context.Background(), runID)
				if err == nil && r.State != domain.RunStateRunning && r.State != domain.RunStatePending {
					_ = stream.Send(&pb.LogEntry{
						Type:      "run_completed",
						Result:    string(r.State),
						Timestamp: r.CompletedAt.String(),
						Message:   r.ErrorMessage,
					})
				}
				return nil
			}
			if err := stream.Send(&pb.LogEntry{
				Type:      "log",
				StepName:  line.StepName,
				Message:   line.Content,
				Timestamp: line.Timestamp,
			}); err != nil {
				return err
			}
		}
	}
}

// streamFilteredLogs serves log content filtered by step name and/or log type.
// It uses the log index when available, falling back to file path conventions.
func (s *ClocheServer) streamFilteredLogs(ctx context.Context, req *pb.StreamLogsRequest, run *domain.Run, stream rpcgrpc.ServerStreamingServer[pb.LogEntry], limit int) error {
	outputDir := filepath.Join(run.ProjectDir, ".cloche", req.RunId, "output")

	// Try log index first
	if s.logStore != nil {
		var logFiles []*ports.LogFileEntry
		var err error

		if req.StepName != "" && req.LogType != "" {
			// Filter by both step and type
			logFiles, err = s.logStore.GetLogFilesByStep(ctx, req.RunId, req.StepName)
			if err == nil {
				filtered := logFiles[:0]
				for _, lf := range logFiles {
					if lf.FileType == req.LogType {
						filtered = append(filtered, lf)
					}
				}
				logFiles = filtered
			}
		} else if req.StepName != "" {
			logFiles, err = s.logStore.GetLogFilesByStep(ctx, req.RunId, req.StepName)
		} else {
			logFiles, err = s.logStore.GetLogFileByType(ctx, req.RunId, req.LogType)
		}

		if err == nil && len(logFiles) > 0 {
			for _, lf := range logFiles {
				data, readErr := os.ReadFile(lf.FilePath)
				if readErr != nil {
					continue
				}
				if err := stream.Send(&pb.LogEntry{
					Type:     lf.FileType + "_log",
					StepName: lf.StepName,
					Message:  applyLimit(string(data), limit),
				}); err != nil {
					return err
				}
			}
			return nil
		}
	}

	// Fall back to file path conventions
	if req.StepName != "" {
		var logPath string
		switch req.LogType {
		case "llm":
			logPath = filepath.Join(outputDir, "llm-"+req.StepName+".log")
		default:
			logPath = filepath.Join(outputDir, req.StepName+".log")
		}

		data, err := os.ReadFile(logPath)
		if err != nil {
			return fmt.Errorf("log file not found for step %q: %w", req.StepName, err)
		}
		return stream.Send(&pb.LogEntry{
			Type:     "step_log",
			StepName: req.StepName,
			Message:  applyLimit(string(data), limit),
		})
	}

	return fmt.Errorf("no log files found matching filter")
}

// streamLiveLogs subscribes to the broadcaster and streams log lines in real
// time for an active run. The stream closes when the run completes or the
// client disconnects.
func (s *ClocheServer) streamLiveLogs(runID string, stream rpcgrpc.ServerStreamingServer[pb.LogEntry]) error {
	sub := s.logBroadcast.Subscribe(runID)
	defer s.logBroadcast.Unsubscribe(runID, sub)

	ctx := stream.Context()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case line, ok := <-sub.C:
			if !ok {
				// Run completed — send terminal entry.
				run, err := s.store.GetRun(context.Background(), runID)
				if err == nil && run.State != domain.RunStateRunning && run.State != domain.RunStatePending {
					_ = stream.Send(&pb.LogEntry{
						Type:      "run_completed",
						Result:    string(run.State),
						Timestamp: run.CompletedAt.String(),
						Message:   run.ErrorMessage,
					})
				}
				return nil
			}
			if err := stream.Send(&pb.LogEntry{
				Type:      "log",
				StepName:  line.StepName,
				Message:   line.Content,
				Timestamp: line.Timestamp,
			}); err != nil {
				return err
			}
		}
	}
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

func (s *ClocheServer) DeleteContainer(ctx context.Context, req *pb.DeleteContainerRequest) (*pb.DeleteContainerResponse, error) {
	if s.container == nil {
		return nil, fmt.Errorf("no container runtime configured")
	}

	id := req.Id
	if id == "" {
		return nil, fmt.Errorf("id is required")
	}

	// Try to resolve as a run ID first
	containerID := id
	run, err := s.store.GetRun(ctx, id)
	if err == nil {
		// It's a run ID — use the container ID from the run record
		if run.ContainerID == "" {
			return nil, fmt.Errorf("run %q has no associated container", id)
		}
		containerID = run.ContainerID
	}

	// Remove the container
	if err := s.container.Remove(ctx, containerID); err != nil {
		return nil, fmt.Errorf("removing container: %w", err)
	}

	// If we resolved a run, clear the container_kept flag
	if run != nil {
		run.ContainerKept = false
		_ = s.store.UpdateRun(ctx, run)
	}

	return &pb.DeleteContainerResponse{}, nil
}

func (s *ClocheServer) EnableLoop(ctx context.Context, req *pb.EnableLoopRequest) (*pb.EnableLoopResponse, error) {
	projectDir := req.ProjectDir
	if projectDir == "" {
		return nil, fmt.Errorf("project_dir is required")
	}

	// Verify at least one host workflow exists in the project.
	hostWorkflows, err := host.FindHostWorkflows(projectDir)
	if err != nil || len(hostWorkflows) == 0 {
		return nil, fmt.Errorf("no host workflows found in %s/.cloche/", projectDir)
	}

	// Load project config for defaults.
	projCfg, _ := config.Load(projectDir)

	maxConc := int(req.MaxConcurrent)
	if maxConc <= 0 {
		maxConc = projCfg.Orchestration.Concurrency
	}
	if maxConc <= 0 {
		maxConc = 1
	}

	stagger := time.Duration(float64(time.Second) * projCfg.Orchestration.StaggerSeconds)

	// Compute dedup timeout from config (default 5 minutes).
	dedupTimeout := time.Duration(float64(time.Second) * projCfg.Orchestration.DedupSeconds)

	loopCfg := host.LoopConfig{
		ProjectDir:    projectDir,
		MaxConcurrent: maxConc,
		StaggerDelay:  stagger,
		DedupTimeout:  dedupTimeout,
		StopOnError:   projCfg.Orchestration.StopOnError,
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Stop existing loop if running.
	if existing, ok := s.loops[projectDir]; ok {
		existing.Stop()
	}

	// Check if project defines a list-tasks workflow for three-phase mode.
	var loop *host.Loop
	if _, hasListTasks := hostWorkflows["list-tasks"]; hasListTasks {
		loop = s.createPhaseLoop(loopCfg, projectDir, dedupTimeout)
	} else {
		loop = s.createLegacyLoop(loopCfg, projectDir, projCfg, dedupTimeout)
	}

	s.loops[projectDir] = loop
	loop.Start()

	return &pb.EnableLoopResponse{}, nil
}

// createPhaseLoop creates a three-phase orchestration loop using list-tasks,
// main, and finalize host workflows from any .cloche file.
func (s *ClocheServer) createPhaseLoop(loopCfg host.LoopConfig, projectDir string, dedupTimeout time.Duration) *host.Loop {
	// Phase 1: list-tasks function
	listTasksFn := func(ctx context.Context, projDir string) ([]host.Task, error) {
		runner := &host.Runner{
			Dispatcher: s,
			Store:      s.store,
		}
		tasks, _, err := host.RunListTasksWorkflow(ctx, runner, projDir)
		return tasks, err
	}

	// Phase 2: main function
	mainFn := func(ctx context.Context, projDir string, taskID string) (*host.RunResult, error) {
		runner := &host.Runner{
			Dispatcher:   s,
			Store:        s.store,
			Captures:     s.captures,
			LogBroadcast: s.logBroadcast,
			TaskID:       taskID,
		}
		return runner.RunNamed(ctx, projDir, "main")
	}

	// Phase 3: finalize function (optional — only if a finalize host workflow exists)
	var finalizeFn host.FinalizeFunc
	if hostWFs, err := host.FindHostWorkflows(projectDir); err == nil {
		if _, hasFinalize := hostWFs["finalize"]; hasFinalize {
			finalizeFn = func(ctx context.Context, projDir string, taskID string, mainResult *host.RunResult) (*host.RunResult, error) {
				mainRunID := ""
				mainOutcome := "failed"
				if mainResult != nil {
					mainRunID = mainResult.RunID
					mainOutcome = string(mainResult.State)
				}
				runner := &host.Runner{
					Dispatcher:   s,
					Store:        s.store,
					Captures:     s.captures,
					LogBroadcast: s.logBroadcast,
					TaskID:       taskID,
					ExtraEnv: []string{
						"CLOCHE_MAIN_RUN_ID=" + mainRunID,
						"CLOCHE_MAIN_OUTCOME=" + mainOutcome,
					},
				}
				return runner.RunNamed(ctx, projDir, "finalize")
			}
		}
	}

	log.Printf("orchestration loop: three-phase mode enabled for %s (list-tasks + main + finalize, dedup=%s)", projectDir, dedupTimeout)
	return host.NewPhaseLoop(loopCfg, s.store, listTasksFn, mainFn, finalizeFn)
}

// createLegacyLoop creates a legacy single-function orchestration loop.
func (s *ClocheServer) createLegacyLoop(loopCfg host.LoopConfig, projectDir string, projCfg *config.Config, dedupTimeout time.Duration) *host.Loop {
	runFn := func(ctx context.Context, projDir string, taskID string) (*host.RunResult, error) {
		runner := &host.Runner{
			Dispatcher:   s,
			Store:        s.store,
			Captures:     s.captures,
			LogBroadcast: s.logBroadcast,
			TaskID:       taskID,
		}
		return runner.Run(ctx, projDir)
	}

	loop := host.NewLoop(loopCfg, s.store, runFn)

	// Configure daemon-managed task assignment if a list-tasks command is set.
	if cmd := projCfg.Orchestration.ListTasksCommand; cmd != "" {
		loop.SetTaskAssigner(&host.ScriptTaskAssigner{Command: cmd})
		log.Printf("orchestration loop: task assignment enabled for %s (command=%q, dedup=%s)", projectDir, cmd, dedupTimeout)
	}

	return loop
}

// GetLoopTasks returns the current task pipeline state for a project's
// orchestration loop, formatted as web.TaskEntry values. Returns nil if no
// loop is active for the project.
func (s *ClocheServer) GetLoopTasks(projectDir string) []web.TaskEntry {
	s.mu.Lock()
	loop, ok := s.loops[projectDir]
	s.mu.Unlock()
	if !ok {
		return nil
	}
	snapshot := loop.GetTaskSnapshot()

	// Build a set of task IDs that have active (pending/running) runs.
	activeTaskIDs := make(map[string]bool)
	ctx := context.Background()
	allRuns, _ := s.store.ListRunsByProject(ctx, projectDir, time.Time{})
	for _, r := range allRuns {
		if r.TaskID != "" && (r.State == domain.RunStatePending || r.State == domain.RunStateRunning) {
			activeTaskIDs[r.TaskID] = true
		}
	}

	entries := make([]web.TaskEntry, len(snapshot))
	for i, e := range snapshot {
		entry := web.TaskEntry{
			ID:          e.Task.ID,
			Status:      e.Task.Status,
			Title:       e.Task.Title,
			Description: e.Task.Description,
			Metadata:    e.Task.Metadata,
			Assigned:    e.Assigned,
			RunID:       e.RunID,
		}
		if !e.AssignedAt.IsZero() {
			entry.AssignedAt = e.AssignedAt.Format(time.RFC3339)
		}
		// Stale: task is in_progress but has no active worker.
		if host.TaskStatus(e.Task.Status) == host.TaskStatusInProgress && !activeTaskIDs[e.Task.ID] {
			entry.Stale = true
		}
		entries[i] = entry
	}
	return entries
}

// ReleaseTask runs the release-task host workflow for a specific task,
// returning it to open status.
func (s *ClocheServer) ReleaseTask(ctx context.Context, projectDir string, taskID string) error {
	runner := &host.Runner{
		Dispatcher:   s,
		Store:        s.store,
		Captures:     s.captures,
		LogBroadcast: s.logBroadcast,
		TaskID:       taskID,
	}
	result, err := runner.RunNamed(ctx, projectDir, "release-task")
	if err != nil {
		return fmt.Errorf("release-task workflow failed: %w", err)
	}
	if result.State != domain.RunStateSucceeded {
		return fmt.Errorf("release-task workflow finished with state %s", result.State)
	}
	return nil
}

func (s *ClocheServer) DisableLoop(ctx context.Context, req *pb.DisableLoopRequest) (*pb.DisableLoopResponse, error) {
	projectDir := req.ProjectDir
	if projectDir == "" {
		return nil, fmt.Errorf("project_dir is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if loop, ok := s.loops[projectDir]; ok {
		loop.Stop()
		delete(s.loops, projectDir)
	}

	return &pb.DisableLoopResponse{}, nil
}

func (s *ClocheServer) ResumeLoop(ctx context.Context, req *pb.ResumeLoopRequest) (*pb.ResumeLoopResponse, error) {
	projectDir := req.ProjectDir
	if projectDir == "" {
		return nil, fmt.Errorf("project_dir is required")
	}

	s.mu.Lock()
	loop, ok := s.loops[projectDir]
	s.mu.Unlock()

	if !ok || loop == nil {
		return nil, fmt.Errorf("no orchestration loop for %s", projectDir)
	}

	loop.Resume()
	return &pb.ResumeLoopResponse{}, nil
}

func (s *ClocheServer) GetProjectInfo(ctx context.Context, req *pb.GetProjectInfoRequest) (*pb.GetProjectInfoResponse, error) {
	projectDir := req.ProjectDir

	// Resolve by name if provided.
	if req.Name != "" && projectDir == "" {
		projects, err := s.store.ListProjects(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing projects: %w", err)
		}
		labels := projectLabels(projects)
		found := false
		for dir, label := range labels {
			if label == req.Name {
				projectDir = dir
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("project %q not found", req.Name)
		}
	}

	if projectDir == "" {
		return nil, fmt.Errorf("project_dir or name is required")
	}

	// Derive project label.
	projects, _ := s.store.ListProjects(ctx)
	labels := projectLabels(projects)
	label := labels[projectDir]
	if label == "" {
		label = filepath.Base(projectDir)
	}

	// Load config.
	cfg, _ := config.Load(projectDir)

	// Check loop state.
	s.mu.Lock()
	loop, loopExists := s.loops[projectDir]
	s.mu.Unlock()
	loopRunning := loopExists && loop != nil && loop.Running()

	// Get active runs.
	allRuns, _ := s.store.ListRunsByProject(ctx, projectDir, time.Time{})
	var activeRuns []*pb.RunSummary
	for _, run := range allRuns {
		if run.State == domain.RunStatePending || run.State == domain.RunStateRunning {
			activeRuns = append(activeRuns, &pb.RunSummary{
				RunId:        run.ID,
				WorkflowName: run.WorkflowName,
				State:        string(run.State),
				StartedAt:    run.StartedAt.String(),
				Title:        run.Title,
				IsHost:       run.IsHost,
				ContainerId:  run.ContainerID,
			})
		}
	}

	// Discover workflows.
	var containerWorkflows, hostWorkflows []string
	clocheDir := filepath.Join(projectDir, ".cloche")
	entries, _ := filepath.Glob(filepath.Join(clocheDir, "*.cloche"))
	for _, path := range entries {
		base := filepath.Base(path)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if base == "host.cloche" {
			wfs, err := dsl.ParseAllForHost(string(data))
			if err != nil {
				continue
			}
			for name := range wfs {
				hostWorkflows = append(hostWorkflows, name)
			}
		} else {
			wf, err := dsl.ParseForContainer(string(data))
			if err != nil {
				continue
			}
			containerWorkflows = append(containerWorkflows, wf.Name)
		}
	}

	sort.Strings(containerWorkflows)
	sort.Strings(hostWorkflows)

	// Check if loop is halted due to an error.
	var errorHalted bool
	var haltError string
	if loopExists && loop != nil {
		errorHalted, haltError = loop.Halted()
	}

	return &pb.GetProjectInfoResponse{
		ProjectDir:         projectDir,
		Name:               label,
		Active:             cfg.Active,
		Concurrency:        int32(cfg.Orchestration.Concurrency),
		StaggerSeconds:     cfg.Orchestration.StaggerSeconds,
		DedupSeconds:       cfg.Orchestration.DedupSeconds,
		EvolutionEnabled:   cfg.Evolution.Enabled,
		LoopRunning:        loopRunning,
		ActiveRuns:         activeRuns,
		ContainerWorkflows: containerWorkflows,
		HostWorkflows:      hostWorkflows,
		StopOnError:        cfg.Orchestration.StopOnError,
		ErrorHalted:        errorHalted,
		HaltError:          haltError,
	}, nil
}

// projectLabels maps each project directory to a short display label.
// Uses basename unless there are conflicts, in which case parent/basename is used.
func projectLabels(dirs []string) map[string]string {
	labels := make(map[string]string, len(dirs))
	byBase := map[string][]string{}
	for _, d := range dirs {
		base := filepath.Base(d)
		byBase[base] = append(byBase[base], d)
	}
	for base, paths := range byBase {
		if len(paths) == 1 {
			labels[paths[0]] = base
		} else {
			for _, p := range paths {
				parent := filepath.Base(filepath.Dir(p))
				labels[p] = parent + "/" + base
			}
		}
	}
	return labels
}

func (s *ClocheServer) GetVersion(ctx context.Context, req *pb.GetVersionRequest) (*pb.GetVersionResponse, error) {
	return &pb.GetVersionResponse{Version: version.Version()}, nil
}

func (s *ClocheServer) Shutdown(ctx context.Context, req *pb.ShutdownRequest) (*pb.ShutdownResponse, error) {
	if !req.Force {
		// Check for active container runs.
		s.mu.Lock()
		activeRuns := len(s.runIDs)
		s.mu.Unlock()
		if activeRuns > 0 {
			return nil, fmt.Errorf("cannot shutdown: %d run(s) still active (use --force to override)", activeRuns)
		}
	}

	// Stop all orchestration loops.
	s.mu.Lock()
	for _, loop := range s.loops {
		loop.Stop()
	}
	s.loops = make(map[string]*host.Loop)
	s.mu.Unlock()

	if s.shutdownFn == nil {
		return nil, fmt.Errorf("shutdown not configured")
	}
	go s.shutdownFn()
	return &pb.ShutdownResponse{}, nil
}

// indexLogFiles scans the extracted output directory and creates log_files index entries.
func (s *ClocheServer) indexLogFiles(ctx context.Context, runID, outputDir string) {
	entries, err := os.ReadDir(outputDir)
	if err != nil {
		log.Printf("run %s: failed to read output dir for indexing: %v", runID, err)
		return
	}

	now := time.Now()
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".log") {
			continue
		}

		var fileType, stepName string
		base := strings.TrimSuffix(name, ".log")

		switch {
		case name == "full.log":
			fileType = "full"
		case name == "container.log":
			// container.log is internal, not indexed as a user-facing log type
			continue
		case strings.HasPrefix(name, "llm-"):
			fileType = "llm"
			stepName = strings.TrimPrefix(base, "llm-")
		default:
			fileType = "script"
			stepName = base
		}

		info, _ := entry.Info()
		var fileSize int64
		if info != nil {
			fileSize = info.Size()
		}

		logEntry := &ports.LogFileEntry{
			RunID:     runID,
			StepName:  stepName,
			FileType:  fileType,
			FilePath:  filepath.Join(outputDir, name),
			FileSize:  fileSize,
			CreatedAt: now,
		}
		if err := s.logStore.SaveLogFile(ctx, logEntry); err != nil {
			log.Printf("run %s: failed to index log file %s: %v", runID, name, err)
		}
	}
}

func gitHEAD(dir string) string {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
