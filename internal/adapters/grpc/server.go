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
	"github.com/cloche-dev/cloche/internal/runcontext"
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
	taskStore    ports.TaskStore    // optional; creates Task records
	attemptStore ports.AttemptStore // optional; creates Attempt records
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

// SetTaskStore attaches a task store so RunWorkflow can create Task records.
func (s *ClocheServer) SetTaskStore(ts ports.TaskStore) {
	s.taskStore = ts
}

// SetAttemptStore attaches an attempt store so RunWorkflow can create Attempt records.
func (s *ClocheServer) SetAttemptStore(as ports.AttemptStore) {
	s.attemptStore = as
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
	// Ensure per-project log migration has run for this project.
	if migrator, ok := s.store.(ports.ProjectMigrator); ok {
		_ = migrator.MigrateProjectLogs(req.ProjectDir)
	}

	// Check for resume mode via gRPC metadata.
	// Two paths: explicit run ID, or task/run ID that needs resolution.
	if resumeRunID := resumeRunIDFromContext(ctx); resumeRunID != "" {
		return s.resumeRun(ctx, resumeRunID, resumeStepFromContext(ctx))
	}
	if taskOrRunID := resumeTaskOrRunFromContext(ctx); taskOrRunID != "" {
		runID, err := s.resolveResumeTarget(ctx, taskOrRunID)
		if err != nil {
			return nil, err
		}
		return s.resumeRun(ctx, runID, "")
	}

	// Parse optional step from workflow_name ("workflow:step" format).
	workflowName, startStep, _ := strings.Cut(req.WorkflowName, ":")

	// Check if this is a host workflow (has host {} block).
	if hostWFs, err := host.FindHostWorkflows(req.ProjectDir); err == nil {
		if _, isHost := hostWFs[workflowName]; isHost {
			return s.runHostWorkflow(ctx, req)
		}
	}

	if s.container == nil {
		return nil, fmt.Errorf("no container runtime configured")
	}

	// Generate a unique run ID, retrying on collision
	var runID string
	for attempts := 0; attempts < 10; attempts++ {
		runID = domain.GenerateRunID(workflowName, "")
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

	run := domain.NewRun(runID, workflowName)
	run.ProjectDir = req.ProjectDir
	run.Title = req.Title
	run.TaskID = req.IssueId

	// Create or link Task and Attempt records for v2 tracking.
	attemptID := s.ensureTaskAndAttempt(ctx, req.IssueId, req.Title, req.ProjectDir)
	run.AttemptID = attemptID
	if run.TaskID == "" {
		// User-initiated run: task ID was synthesized during ensureTaskAndAttempt.
		// Derive it from the attempt prefix for backward compat with log paths.
		run.TaskID = "user-" + attemptID
	}

	// Write prompt to .cloche/runs/<task-id>/prompt.txt
	if req.Prompt != "" {
		promptPath := runcontext.PromptPath(req.ProjectDir, run.TaskID)
		if err := os.MkdirAll(filepath.Dir(promptPath), 0755); err != nil {
			return nil, fmt.Errorf("creating runs dir: %w", err)
		}
		if err := os.WriteFile(promptPath, []byte(req.Prompt), 0644); err != nil {
			return nil, fmt.Errorf("writing prompt: %w", err)
		}
	}
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
	go s.launchAndTrack(runID, image, req.KeepContainer, startStep, req)

	return &pb.RunWorkflowResponse{RunId: runID, TaskId: run.TaskID, AttemptId: run.AttemptID}, nil
}

// runHostWorkflow dispatches a host workflow via the host runner, returning
// immediately while the workflow runs in a background goroutine.
func (s *ClocheServer) runHostWorkflow(ctx context.Context, req *pb.RunWorkflowRequest) (*pb.RunWorkflowResponse, error) {
	// Check for a propagated attempt ID from a parent host executor.
	attemptID := attemptIDFromContext(ctx)

	// If no parent attempt, create Task and Attempt records for this host run.
	if attemptID == "" {
		attemptID = s.ensureTaskAndAttempt(ctx, req.IssueId, req.Title, req.ProjectDir)
	}

	taskID := req.IssueId
	if taskID == "" && attemptID != "" {
		taskID = "user-" + attemptID
	}

	hostWorkflowName, _, _ := strings.Cut(req.WorkflowName, ":")
	runID := domain.GenerateRunID(hostWorkflowName, "")

	runner := &host.Runner{
		Dispatcher:   s,
		Store:        s.store,
		Captures:     s.captures,
		LogBroadcast: s.logBroadcast,
		TaskID:       taskID,
		AttemptID:    attemptID,
	}

	go func() {
		runner.RunNamedWithID(context.Background(), req.ProjectDir, hostWorkflowName, runID)
	}()

	return &pb.RunWorkflowResponse{RunId: runID}, nil
}

// resumeRunIDFromContext extracts the resume run ID from gRPC metadata.
func resumeRunIDFromContext(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	vals := md.Get("x-cloche-resume-run-id")
	if len(vals) == 0 {
		return ""
	}
	return vals[0]
}

// resumeStepFromContext extracts the resume step name from gRPC metadata.
func resumeStepFromContext(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	vals := md.Get("x-cloche-resume-step")
	if len(vals) == 0 {
		return ""
	}
	return vals[0]
}

// resumeTaskOrRunFromContext extracts a task-or-run ID from gRPC metadata.
// This is used when the CLI passes a bare ID (no colons) that could be either
// a task ID or a run ID — the server resolves it.
func resumeTaskOrRunFromContext(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	vals := md.Get("x-cloche-resume-task-or-run")
	if len(vals) == 0 {
		return ""
	}
	return vals[0]
}

// attemptIDFromContext extracts a propagated attempt ID from gRPC metadata.
// This is set by the host executor when dispatching a child container workflow
// so the container run is linked to the parent host run's attempt.
func attemptIDFromContext(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	vals := md.Get(host.AttemptIDMetadataKey)
	if len(vals) == 0 {
		return ""
	}
	return vals[0]
}

// ensureTaskAndAttempt creates (or looks up) a Task record and creates a new
// running Attempt for it. Returns the attempt ID, or a generated fallback ID
// if the stores are not configured.
//
// When issueID is non-empty, the task has external source. When empty, a
// user-initiated task is created with a generated ID.
func (s *ClocheServer) ensureTaskAndAttempt(ctx context.Context, issueID, title, projectDir string) string {
	attemptID := domain.GenerateAttemptID()

	if s.taskStore == nil || s.attemptStore == nil {
		return attemptID
	}

	taskID := issueID
	taskSource := domain.TaskSourceExternal
	if taskID == "" {
		// User-initiated: synthesize a stable task ID from the attempt ID.
		taskID = "user-" + attemptID
		taskSource = domain.TaskSourceUserInitiated
	}

	// Ensure the task record exists.
	if _, err := s.taskStore.GetTask(ctx, taskID); err != nil {
		task := &domain.Task{
			ID:         taskID,
			Title:      title,
			Source:     taskSource,
			ProjectDir: projectDir,
			CreatedAt:  time.Now(),
		}
		if saveErr := s.taskStore.SaveTask(ctx, task); saveErr != nil {
			log.Printf("server: failed to save task %s: %v", taskID, saveErr)
			return attemptID
		}
	}

	// Create a new Attempt for this run.
	attempt := &domain.Attempt{
		ID:        attemptID,
		TaskID:    taskID,
		StartedAt: time.Now(),
		Result:    domain.AttemptResultRunning,
	}
	if err := s.attemptStore.SaveAttempt(ctx, attempt); err != nil {
		log.Printf("server: failed to save attempt %s for task %s: %v", attemptID, taskID, err)
	}

	return attemptID
}

// resolveResumeTarget takes a bare ID (no colons) and resolves it to a
// failed run ID. It first tries as a task ID (finds the latest attempt's
// failed run), then falls back to treating it as a direct run ID.
func (s *ClocheServer) resolveResumeTarget(ctx context.Context, id string) (string, error) {
	// Try as a task ID first via the task store (uses attempt tracking for
	// precise latest-attempt resolution).
	if s.taskStore != nil {
		if task, err := s.taskStore.GetTask(ctx, id); err == nil {
			if runID, err := s.findFailedRunForTask(ctx, task); err == nil {
				return runID, nil
			}
		}
	}

	// Also scan runs with this task_id directly. This handles v2 task IDs
	// even when the attempt store is not configured, since v2 runs carry the
	// task_id on the run record itself. Runs are ordered by started_at DESC so
	// the most recent run's AttemptID indicates the latest attempt.
	if runs, err := s.store.ListRunsFiltered(ctx, domain.RunListFilter{TaskID: id}); err == nil && len(runs) > 0 {
		if runID, err := pickFailedRun(runs); err == nil {
			return runID, nil
		}
	}

	// Fall back to treating it as a run ID.
	if _, err := s.store.GetRun(ctx, id); err == nil {
		return id, nil
	}

	return "", fmt.Errorf("no task or run found for %q", id)
}

// findFailedRunForTask finds the best failed run in the task's latest attempt.
// Host runs are preferred over child container runs so that resume restarts
// from the correct host-level step.
func (s *ClocheServer) findFailedRunForTask(ctx context.Context, task *domain.Task) (string, error) {
	if s.attemptStore == nil {
		return "", fmt.Errorf("attempt tracking not configured")
	}

	attempts, err := s.attemptStore.ListAttempts(ctx, task.ID)
	if err != nil || len(attempts) == 0 {
		return "", fmt.Errorf("no attempts found for task %q", task.ID)
	}

	// Find the latest attempt (last in the list).
	latest := attempts[len(attempts)-1]

	// Find all runs for this attempt and pick the best failed one.
	runs, err := s.store.ListRunsFiltered(ctx, domain.RunListFilter{
		AttemptID: latest.ID,
	})
	if err != nil {
		return "", fmt.Errorf("listing runs for attempt %s: %w", latest.ID, err)
	}

	runID, err := pickFailedRun(runs)
	if err != nil {
		return "", fmt.Errorf("no failed runs found for task %q (latest attempt %s)", task.ID, latest.ID)
	}
	return runID, nil
}

// pickFailedRun selects the best run ID to resume from a slice of runs.
// Host runs are preferred over container runs: the host run orchestrates the
// overall workflow, so resuming it re-dispatches from the correct host-level
// step. Steps with a 'fail' wired result are recorded on the host run, not
// on the child container run. Returns an error if no failed run is found.
func pickFailedRun(runs []*domain.Run) (string, error) {
	var hostRunID, containerRunID string
	for _, run := range runs {
		if run.State != domain.RunStateFailed {
			continue
		}
		if run.IsHost && hostRunID == "" {
			hostRunID = run.ID
		} else if !run.IsHost && containerRunID == "" {
			containerRunID = run.ID
		}
	}
	if hostRunID != "" {
		return hostRunID, nil
	}
	if containerRunID != "" {
		return containerRunID, nil
	}
	return "", fmt.Errorf("no failed runs found")
}

// resumeRun resumes a failed workflow run from a specific step.
func (s *ClocheServer) resumeRun(ctx context.Context, runID, stepName string) (*pb.RunWorkflowResponse, error) {
	run, err := s.store.GetRun(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("run %q not found: %w", runID, err)
	}

	if run.State != domain.RunStateFailed {
		return nil, fmt.Errorf("run %q is in state %q, only failed runs can be resumed", runID, run.State)
	}

	// Load step executions — GetRun only loads the run record, not its steps.
	if caps, err := s.captures.GetCaptures(ctx, runID); err == nil {
		run.StepExecutions = caps
	}

	// Determine the resume step: use provided step name, or find first failed step
	if stepName == "" {
		stepName = run.FindFirstFailedStep()
		if stepName == "" {
			return nil, fmt.Errorf("run %q has no failed step to resume from", runID)
		}
	}

	if run.IsHost {
		return s.resumeHostRun(ctx, run, stepName)
	}
	return s.resumeContainerRun(ctx, run, stepName)
}

// resumeHostRun resumes a failed host workflow run.
func (s *ClocheServer) resumeHostRun(ctx context.Context, run *domain.Run, stepName string) (*pb.RunWorkflowResponse, error) {
	runner := &host.Runner{
		Dispatcher:   s,
		Store:        s.store,
		Captures:     s.captures,
		LogBroadcast: s.logBroadcast,
		TaskID:       run.TaskID,
	}

	go func() {
		runner.ResumeRun(context.Background(), run, stepName)
	}()

	return &pb.RunWorkflowResponse{RunId: run.ID}, nil
}

// resumeContainerRun resumes a failed container workflow run.
func (s *ClocheServer) resumeContainerRun(ctx context.Context, run *domain.Run, stepName string) (*pb.RunWorkflowResponse, error) {
	if s.container == nil {
		return nil, fmt.Errorf("no container runtime configured")
	}

	// Verify container still exists
	cs, err := s.container.Inspect(ctx, run.ContainerID)
	if err != nil {
		return nil, fmt.Errorf("container %s not found (may have been cleaned up): %w", run.ContainerID, err)
	}
	if cs.Running {
		return nil, fmt.Errorf("container %s is still running", run.ContainerID)
	}

	// Commit the container to preserve its filesystem state
	committer, ok := s.container.(ports.ContainerCommitter)
	if !ok {
		return nil, fmt.Errorf("container runtime does not support resume (no commit capability)")
	}

	imageID, err := committer.Commit(ctx, run.ContainerID)
	if err != nil {
		return nil, fmt.Errorf("committing container state: %w", err)
	}

	// Copy updated scripts and overrides from host into the committed image
	// by starting a new container with the resume command
	resumeCmd := []string{
		"cloche-agent", ".cloche/" + run.WorkflowName + ".cloche",
		"--resume-from", stepName,
	}

	// Reset run state
	run.State = domain.RunStateRunning
	run.ErrorMessage = ""
	run.CompletedAt = time.Time{}
	run.ActiveSteps = nil
	_ = s.store.UpdateRun(ctx, run)

	// Start new container from committed image with resume command
	go s.launchResumeContainer(run, imageID, resumeCmd)

	return &pb.RunWorkflowResponse{RunId: run.ID}, nil
}

// launchResumeContainer starts a new container from a committed image with
// the resume command, then tracks it to completion.
func (s *ClocheServer) launchResumeContainer(run *domain.Run, image string, cmd []string) {
	ctx := context.Background()

	containerID, err := s.container.Start(ctx, ports.ContainerConfig{
		Image:        image,
		WorkflowName: run.WorkflowName,
		ProjectDir:   run.ProjectDir,
		RunID:        run.ID,
		NetworkAllow: []string{"*"},
		Cmd:          cmd,
	})
	if err != nil {
		run.Fail(fmt.Sprintf("failed to start resume container: %v", err))
		_ = s.store.UpdateRun(ctx, run)
		log.Printf("run %s: failed to start resume container: %v", run.ID, err)
		return
	}

	s.mu.Lock()
	s.runIDs[run.ID] = containerID
	s.mu.Unlock()

	run.ContainerID = containerID
	_ = s.store.UpdateRun(ctx, run)

	s.trackRun(run.ID, containerID, run.ProjectDir, run.WorkflowName, true)
}

// launchAndTrack starts the container and then tracks it to completion.
// It runs in a background goroutine with its own context, independent of the
// RPC context which may be cancelled after RunWorkflow returns.
// startStep, when non-empty, causes the agent to begin execution at that step.
func (s *ClocheServer) launchAndTrack(runID, image string, keepContainer bool, startStep string, req *pb.RunWorkflowRequest) {
	ctx := context.Background()

	// Parse the workflow name (strip any ":step" suffix that was already extracted).
	workflowName, _, _ := strings.Cut(req.WorkflowName, ":")

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

	// Look up the task ID from the run record for container env.
	var taskID string
	if r, err := s.store.GetRun(ctx, runID); err == nil && r != nil {
		taskID = r.TaskID
	}

	// Build container command, adding --start-step if a specific step was requested.
	var cmd []string
	if startStep != "" {
		cmd = []string{
			"cloche-agent", ".cloche/" + workflowName + ".cloche",
			"--start-step", startStep,
		}
	}

	containerID, err := s.container.Start(ctx, ports.ContainerConfig{
		Image:        image,
		WorkflowName: workflowName,
		ProjectDir:   req.ProjectDir,
		RunID:        runID,
		TaskID:       taskID,
		NetworkAllow: []string{"*"},
		Cmd:          cmd,
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

	s.trackRun(runID, containerID, req.ProjectDir, workflowName, keepContainer)
}

// runLogDir returns the directory where extracted log files for a run are stored.
// For v2 runs (with AttemptID and TaskID), uses .cloche/logs/<taskID>/<attemptID>/.
// Falls back to the legacy .cloche/<runID>/output/ path for older runs.
func runLogDir(run *domain.Run, projectDir, runID string) string {
	if run != nil && run.AttemptID != "" && run.TaskID != "" {
		return filepath.Join(projectDir, ".cloche", "logs", run.TaskID, run.AttemptID)
	}
	return filepath.Join(projectDir, ".cloche", runID, "output")
}

func (s *ClocheServer) trackRun(runID, containerID, projectDir, workflowName string, keepContainer bool) {
	ctx := context.Background()

	// Determine the log extraction directory upfront. v2 runs with AttemptID
	// use .cloche/logs/<taskID>/<attemptID>/; older runs use the legacy path.
	initialRun, _ := s.store.GetRun(ctx, runID)
	outputDst := runLogDir(initialRun, projectDir, runID)

	// Register run in broadcaster so IsActive returns true for live-stream callers.
	if s.logBroadcast != nil {
		s.logBroadcast.Start(runID)
	}

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
			// Eagerly extract step output so the Web UI can serve it
			// while the run is still active.
			if mkErr := os.MkdirAll(outputDst, 0755); mkErr == nil {
				_ = s.container.CopyFrom(ctx, containerID, "/workspace/.cloche/output/.", outputDst)
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

	// Wait for process exit
	exitCode, err := s.container.Wait(ctx, containerID)
	if err != nil {
		log.Printf("error waiting for run %s: %v", runID, err)
	}

	// Extract step output files from container before it's removed
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
		s.indexLogFiles(ctx, runID, outputDst, workflowName)
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

	// Signal live-stream subscribers that this run is done. This must happen
	// AFTER the state update so that subscribers see the final state when
	// their channel closes (prevents a race where the channel closes but
	// the store still says "running", causing StreamLogs to return empty).
	if s.logBroadcast != nil {
		s.logBroadcast.Finish(runID)
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

	// Unless --all is set, default to last hour
	if !req.All {
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

func (s *ClocheServer) ListTasks(ctx context.Context, req *pb.ListTasksRequest) (*pb.ListTasksResponse, error) {
	// Ensure per-project log migration has run.
	if req.ProjectDir != "" {
		if migrator, ok := s.store.(ports.ProjectMigrator); ok {
			_ = migrator.MigrateProjectLogs(req.ProjectDir)
		}
	}

	if s.taskStore == nil {
		return nil, fmt.Errorf("task store not configured")
	}
	tasks, err := s.taskStore.ListTasks(ctx, req.ProjectDir)
	if err != nil {
		return nil, fmt.Errorf("listing tasks: %w", err)
	}

	resp := &pb.ListTasksResponse{}
	for _, task := range tasks {
		sum := &pb.TaskSummary{
			TaskId:       task.ID,
			Title:        task.Title,
			Status:       string(task.Status),
			ProjectDir:   task.ProjectDir,
			CreatedAt:    task.CreatedAt.String(),
			AttemptCount: int32(len(task.Attempts)),
		}
		if la := task.LatestAttempt(); la != nil {
			sum.LatestAttemptId = la.ID
		}
		resp.Tasks = append(resp.Tasks, sum)
	}
	return resp, nil
}

func (s *ClocheServer) GetTask(ctx context.Context, req *pb.GetTaskRequest) (*pb.GetTaskResponse, error) {
	if s.taskStore == nil {
		return nil, fmt.Errorf("task store not configured")
	}
	task, err := s.taskStore.GetTask(ctx, req.TaskId)
	if err != nil {
		return nil, fmt.Errorf("getting task: %w", err)
	}

	resp := &pb.GetTaskResponse{
		TaskId:     task.ID,
		Title:      task.Title,
		Status:     string(task.Status),
		ProjectDir: task.ProjectDir,
	}
	for _, a := range task.Attempts {
		resp.Attempts = append(resp.Attempts, &pb.AttemptSummary{
			AttemptId: a.ID,
			TaskId:    a.TaskID,
			Result:    string(a.Result),
			StartedAt: a.StartedAt.String(),
			EndedAt:   a.EndedAt.String(),
		})
	}
	return resp, nil
}

func (s *ClocheServer) GetAttempt(ctx context.Context, req *pb.GetAttemptRequest) (*pb.GetAttemptResponse, error) {
	if s.attemptStore == nil {
		return nil, fmt.Errorf("attempt store not configured")
	}
	attempt, err := s.attemptStore.GetAttempt(ctx, req.AttemptId)
	if err != nil {
		return nil, fmt.Errorf("getting attempt: %w", err)
	}

	// Find the run associated with this attempt.
	var runID string
	runs, err := s.store.ListRunsFiltered(ctx, domain.RunListFilter{TaskID: attempt.TaskID})
	if err == nil {
		for _, r := range runs {
			if r.AttemptID == req.AttemptId {
				runID = r.ID
				break
			}
		}
	}

	return &pb.GetAttemptResponse{
		AttemptId: attempt.ID,
		TaskId:    attempt.TaskID,
		Result:    string(attempt.Result),
		StartedAt: attempt.StartedAt.String(),
		EndedAt:   attempt.EndedAt.String(),
		RunId:     runID,
	}, nil
}

// allSubcmds is the canonical list of cloche subcommands for shell completion.
var allSubcmds = []string{
	"complete", "delete", "get", "health", "help", "init", "list", "logs",
	"loop", "poll", "project", "resume", "run", "set", "shutdown", "status",
	"stop", "tasks", "validate", "workflow",
}

// Complete returns shell completion candidates for the given partial command line.
func (s *ClocheServer) Complete(ctx context.Context, req *pb.CompleteRequest) (*pb.CompleteResponse, error) {
	words := req.Words
	idx := int(req.CurIdx)
	projectDir := req.ProjectDir

	// Determine current token being completed (may be empty if just past a space).
	cur := ""
	if idx >= 0 && idx < len(words) {
		cur = words[idx]
	}

	// Position 1: completing the subcommand.
	if idx <= 1 {
		return &pb.CompleteResponse{Completions: filterPrefix(allSubcmds, cur)}, nil
	}

	// Position >= 2: completing argument for a subcommand.
	if len(words) < 2 {
		return &pb.CompleteResponse{}, nil
	}
	subcommand := words[1]

	// Previous token (for flag-value pairs).
	prev := ""
	if idx > 1 && idx-1 < len(words) {
		prev = words[idx-1]
	}

	var completions []string

	switch subcommand {
	case "run":
		switch prev {
		case "--workflow":
			completions = s.workflowNames(projectDir)
		default:
			completions = []string{"--workflow", "--prompt", "--title", "--issue", "--keep-container"}
		}

	case "status":
		completions = append(completions, s.taskAndAttemptIDs(ctx, projectDir)...)
		completions = append(completions, "--all")

	case "logs":
		if prev == "--step" || prev == "-s" {
			// No dynamic step completions without knowing the run; skip.
		} else if prev == "--type" {
			completions = []string{"full", "script", "llm"}
		} else if prev == "--limit" || prev == "-l" {
			// numeric; skip
		} else {
			completions = append(completions, s.taskAndAttemptIDs(ctx, projectDir)...)
			completions = append(completions, "--step", "--type", "--follow", "--limit")
		}

	case "stop":
		completions = s.runningTaskIDs(ctx, projectDir)

	case "delete":
		completions = s.recentRunIDs(ctx, projectDir)

	case "resume":
		completions = s.recentRunIDs(ctx, projectDir)

	case "list":
		if prev == "--state" || prev == "-s" {
			completions = []string{"running", "pending", "succeeded", "failed", "cancelled"}
		} else {
			completions = []string{"--all", "--runs", "--state", "--project", "--limit"}
		}

	case "loop":
		completions = []string{"stop", "resume", "--max"}

	case "workflow":
		completions = s.workflowNames(projectDir)

	case "poll":
		completions = s.recentRunIDs(ctx, projectDir)

	default:
		// No dynamic completions for other subcommands.
	}

	return &pb.CompleteResponse{Completions: filterPrefix(completions, cur)}, nil
}

// filterPrefix returns items from list that start with prefix.
func filterPrefix(list []string, prefix string) []string {
	if prefix == "" {
		return list
	}
	var out []string
	for _, s := range list {
		if strings.HasPrefix(s, prefix) {
			out = append(out, s)
		}
	}
	return out
}

// workflowNames returns workflow names from .cloche/*.cloche files in projectDir.
func (s *ClocheServer) workflowNames(projectDir string) []string {
	if projectDir == "" {
		return nil
	}
	clocheDir := filepath.Join(projectDir, ".cloche")
	entries, err := filepath.Glob(filepath.Join(clocheDir, "*.cloche"))
	if err != nil {
		return nil
	}
	seen := make(map[string]bool)
	var names []string
	for _, path := range entries {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		wfs, err := dsl.ParseAll(string(data))
		if err != nil {
			continue
		}
		for name := range wfs {
			if !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
		}
	}
	sort.Strings(names)
	return names
}

// taskAndAttemptIDs returns recent task IDs and attempt IDs from the store.
func (s *ClocheServer) taskAndAttemptIDs(ctx context.Context, projectDir string) []string {
	var ids []string

	// Task IDs from task store.
	if s.taskStore != nil {
		tasks, err := s.taskStore.ListTasks(ctx, projectDir)
		if err == nil {
			for _, t := range tasks {
				ids = append(ids, t.ID)
				// Also include attempt IDs for colon-delimited drill-down.
				for _, a := range t.Attempts {
					ids = append(ids, t.ID+":"+a.ID)
				}
			}
		}
	}

	// Fallback: recent run IDs.
	if len(ids) == 0 {
		ids = s.recentRunIDs(ctx, projectDir)
	}

	return ids
}

// runningIDs returns IDs of currently running or pending runs.
func (s *ClocheServer) runningIDs(ctx context.Context, projectDir string) []string {
	filter := domain.RunListFilter{ProjectDir: projectDir, State: domain.RunStateRunning}
	runs, err := s.store.ListRunsFiltered(ctx, filter)
	if err != nil {
		return nil
	}
	var ids []string
	for _, r := range runs {
		ids = append(ids, r.ID)
	}
	return ids
}

// runningTaskIDs returns task IDs of tasks that have running or pending runs.
func (s *ClocheServer) runningTaskIDs(ctx context.Context, projectDir string) []string {
	filter := domain.RunListFilter{ProjectDir: projectDir, State: domain.RunStateRunning}
	runs, err := s.store.ListRunsFiltered(ctx, filter)
	if err != nil {
		return nil
	}
	seen := make(map[string]bool)
	var ids []string
	for _, r := range runs {
		if r.TaskID != "" && !seen[r.TaskID] {
			seen[r.TaskID] = true
			ids = append(ids, r.TaskID)
		}
	}
	return ids
}

// recentRunIDs returns IDs of recent runs (last 20).
func (s *ClocheServer) recentRunIDs(ctx context.Context, projectDir string) []string {
	filter := domain.RunListFilter{ProjectDir: projectDir, Limit: 20}
	runs, err := s.store.ListRunsFiltered(ctx, filter)
	if err != nil {
		// Try unfiltered if project-filtered fails.
		runs, err = s.store.ListRunsFiltered(ctx, domain.RunListFilter{Limit: 20})
		if err != nil {
			return nil
		}
	}
	var ids []string
	for _, r := range runs {
		ids = append(ids, r.ID)
	}
	return ids
}

// resolveRunIDFromID resolves a colon-delimited ID to a (runID, stepName) pair.
// The id may be:
//   - a run_id                       → returns (runID, "", nil)
//   - a task_id                      → returns (latestRunID, "", nil)
//   - an attempt_id                  → returns (runID, "", nil) for the run with that attempt
//   - attempt_id:workflow_name       → returns (runID, "", nil) for that workflow run
//   - attempt_id:workflow_name:step  → returns (runID, stepName, nil)
//   - task_id:attempt_id             → returns (runID, "", nil) for that specific attempt
//   - task_id:attempt_id:step_name   → returns (runID, stepName, nil)
func (s *ClocheServer) resolveRunIDFromID(ctx context.Context, id string) (runID, stepName string, err error) {
	parts := strings.SplitN(id, ":", 3)

	switch len(parts) {
	case 1:
		// Try as run_id first
		if _, e := s.store.GetRun(ctx, id); e == nil {
			return id, "", nil
		}
		// Try as task_id (most recent run)
		runs, e := s.store.ListRunsFiltered(ctx, domain.RunListFilter{TaskID: id, Limit: 1})
		if e == nil && len(runs) > 0 {
			return runs[0].ID, "", nil
		}
		// Try as attempt_id
		run, e := s.findRunByAttemptID(ctx, id)
		if e == nil {
			return run.ID, "", nil
		}
		return "", "", fmt.Errorf("no run found for id %q", id)

	case 2:
		// Try attempt_id:workflow_name first
		run, e := s.findRunByAttemptAndWorkflow(ctx, parts[0], parts[1])
		if e == nil {
			return run.ID, "", nil
		}
		// Fall back to task_id:attempt_id
		run, e = s.findRunByTaskAndAttempt(ctx, parts[0], parts[1])
		if e != nil {
			return "", "", fmt.Errorf("no run found for %q: not a valid attempt:workflow or task:attempt pair", id)
		}
		return run.ID, "", nil

	case 3:
		// Try attempt_id:workflow_name:step_name first
		run, e := s.findRunByAttemptAndWorkflow(ctx, parts[0], parts[1])
		if e == nil {
			return run.ID, parts[2], nil
		}
		// Fall back to task_id:attempt_id:step_name
		run, e = s.findRunByTaskAndAttempt(ctx, parts[0], parts[1])
		if e != nil {
			return "", "", fmt.Errorf("no run found for task %q attempt %q: %w", parts[0], parts[1], e)
		}
		return run.ID, parts[2], nil
	}

	return "", "", fmt.Errorf("invalid id %q", id)
}

// findRunByAttemptID searches for a run whose AttemptID matches the given ID.
func (s *ClocheServer) findRunByAttemptID(ctx context.Context, attemptID string) (*domain.Run, error) {
	// If an attempt store is available, look up the task_id first to narrow the search.
	if s.attemptStore != nil {
		attempt, err := s.attemptStore.GetAttempt(ctx, attemptID)
		if err == nil {
			runs, err := s.store.ListRunsFiltered(ctx, domain.RunListFilter{TaskID: attempt.TaskID})
			if err == nil {
				for _, r := range runs {
					if r.AttemptID == attemptID {
						return r, nil
					}
				}
			}
		}
	}
	// Fallback: scan all recent runs
	runs, err := s.store.ListRunsFiltered(ctx, domain.RunListFilter{})
	if err != nil {
		return nil, err
	}
	for _, r := range runs {
		if r.AttemptID == attemptID {
			return r, nil
		}
	}
	return nil, fmt.Errorf("no run found with attempt ID %q", attemptID)
}

// findRunByAttemptAndWorkflow returns the run whose AttemptID and WorkflowName match.
func (s *ClocheServer) findRunByAttemptAndWorkflow(ctx context.Context, attemptID, workflowName string) (*domain.Run, error) {
	// Use attemptStore to narrow by task if available.
	if s.attemptStore != nil {
		attempt, err := s.attemptStore.GetAttempt(ctx, attemptID)
		if err == nil {
			runs, err := s.store.ListRunsFiltered(ctx, domain.RunListFilter{TaskID: attempt.TaskID})
			if err == nil {
				for _, r := range runs {
					if r.AttemptID == attemptID && r.WorkflowName == workflowName {
						return r, nil
					}
				}
			}
		}
	}
	// Fallback: scan all recent runs.
	runs, err := s.store.ListRunsFiltered(ctx, domain.RunListFilter{})
	if err != nil {
		return nil, err
	}
	for _, r := range runs {
		if r.AttemptID == attemptID && r.WorkflowName == workflowName {
			return r, nil
		}
	}
	return nil, fmt.Errorf("no run found with attempt ID %q and workflow %q", attemptID, workflowName)
}

// findRunByTaskAndAttempt returns the run for the given task and attempt.
func (s *ClocheServer) findRunByTaskAndAttempt(ctx context.Context, taskID, attemptID string) (*domain.Run, error) {
	runs, err := s.store.ListRunsFiltered(ctx, domain.RunListFilter{TaskID: taskID})
	if err != nil {
		return nil, err
	}
	for _, r := range runs {
		if r.AttemptID == attemptID {
			return r, nil
		}
	}
	return nil, fmt.Errorf("no run found for task %q with attempt %q", taskID, attemptID)
}

func (s *ClocheServer) GetStatus(ctx context.Context, req *pb.GetStatusRequest) (*pb.GetStatusResponse, error) {
	// Resolve the run to inspect. The Id field (task/attempt/step ID) takes
	// priority over the legacy run_id field for backward compatibility.
	runID := req.RunId
	if req.Id != "" {
		resolved, _, err := s.resolveRunIDFromID(ctx, req.Id)
		if err != nil {
			return nil, fmt.Errorf("resolving id %q: %w", req.Id, err)
		}
		runID = resolved
	}

	run, err := s.store.GetRun(ctx, runID)
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
		captures, err := s.captures.GetCaptures(ctx, run.ID)
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

	// Resolve run from the Id field if set (colon-delimited: task_id[:attempt_id[:step_name]]).
	// Takes priority over the legacy run_id + step_name fields.
	if req.Id != "" {
		runID, stepName, err := s.resolveRunIDFromID(ctx, req.Id)
		if err != nil {
			return fmt.Errorf("resolving id %q: %w", req.Id, err)
		}
		// Build a synthetic request with the resolved run_id and step_name.
		resolved := &pb.StreamLogsRequest{
			RunId:    runID,
			StepName: stepName,
			LogType:  req.LogType,
			Follow:   req.Follow,
			Limit:    req.Limit,
		}
		return s.StreamLogs(resolved, stream)
	}

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

	// Active runs stream live output when the broadcaster is tracking them.
	// If the broadcaster has no active entry (e.g. daemon restarted while run
	// was in progress, or Finish was already called during post-run cleanup),
	// fall through to serve static content instead of hanging forever.
	if isActive && s.logBroadcast != nil && s.logBroadcast.IsActive(req.RunId) {
		return s.streamFollowLogs(req.RunId, run, stream, limit)
	}

	// Check for full.log first — if it exists, serve it as the unified log
	// Try v2 path first, fall back to legacy path.
	fullLogPath := filepath.Join(runLogDir(run, run.ProjectDir, req.RunId), "full.log")
	if _, statErr := os.Stat(fullLogPath); os.IsNotExist(statErr) {
		fullLogPath = filepath.Join(run.ProjectDir, ".cloche", req.RunId, "output", "full.log")
	}
	if data, readErr := os.ReadFile(fullLogPath); readErr == nil && len(data) > 0 {
		msg := applyLimit(string(data), limit)
		if err := stream.Send(&pb.LogEntry{
			Type:    "full_log",
			Message: msg,
		}); err != nil {
			return err
		}
		return s.sendRunCompleted(ctx, req.RunId, stream)
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
			// Try v2 path (workflow-prefixed) first, then legacy path.
			var output string
			v2Dir := runLogDir(run, run.ProjectDir, req.RunId)
			v2Prefix := run.WorkflowName + "-"
			for _, ext := range []string{".log", ".out"} {
				v2Path := filepath.Join(v2Dir, v2Prefix+exec.StepName+ext)
				if data, err := os.ReadFile(v2Path); err == nil && len(data) > 0 {
					output = string(data)
					break
				}
			}
			if output == "" {
				for _, ext := range []string{".log", ".out"} {
					outputPath := filepath.Join(run.ProjectDir, ".cloche", req.RunId, "output", exec.StepName+ext)
					if data, err := os.ReadFile(outputPath); err == nil && len(data) > 0 {
						output = string(data)
						break
					}
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

	// Send run completion entry (re-read state in case it settled during static serving)
	return s.sendRunCompleted(ctx, req.RunId, stream)
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

// sendRunCompleted re-reads the run from the store and sends a run_completed entry
// if the run has reached a terminal state. This handles the case where the run
// state was updated between the initial read and now (e.g. during post-run cleanup).
func (s *ClocheServer) sendRunCompleted(ctx context.Context, runID string, stream rpcgrpc.ServerStreamingServer[pb.LogEntry]) error {
	r, err := s.store.GetRun(ctx, runID)
	if err != nil {
		return nil
	}
	if r.State == domain.RunStateRunning || r.State == domain.RunStatePending {
		return nil
	}
	return stream.Send(&pb.LogEntry{
		Type:      "run_completed",
		Result:    string(r.State),
		Timestamp: r.CompletedAt.String(),
		Message:   r.ErrorMessage,
	})
}

// streamFollowLogs sends existing log content then tails live output from the
// broadcaster. It combines snapshot + live streaming (like tail -f).
func (s *ClocheServer) streamFollowLogs(runID string, run *domain.Run, stream rpcgrpc.ServerStreamingServer[pb.LogEntry], limit int) error {
	// Subscribe first so we don't miss lines written between read and subscribe.
	// SubscribeWithHistory returns all previously published lines, giving
	// callers a gap-free view from the start of the run.
	sub, history := s.logBroadcast.SubscribeWithHistory(runID)
	defer s.logBroadcast.Unsubscribe(runID, sub)

	if len(history) > 0 {
		// Send historical lines so the caller sees output from before this
		// connection, including step_name for per-step routing.
		start := 0
		if limit > 0 && len(history) > limit {
			start = len(history) - limit
		}
		for _, line := range history[start:] {
			if err := stream.Send(&pb.LogEntry{
				Type:      "log",
				StepName:  line.StepName,
				Message:   line.Content,
				Timestamp: line.Timestamp,
			}); err != nil {
				return err
			}
		}
	} else {
		// Fallback: broadcaster has no history (e.g. daemon restarted
		// mid-run). Send existing full.log content if available.
		flp885 := filepath.Join(runLogDir(run, run.ProjectDir, runID), "full.log")
		if _, err885 := os.Stat(flp885); os.IsNotExist(err885) {
			flp885 = filepath.Join(run.ProjectDir, ".cloche", runID, "output", "full.log")
		}
		fullLogPath := flp885
		if data, err := os.ReadFile(fullLogPath); err == nil && len(data) > 0 {
			msg := applyLimit(string(data), limit)
			if err := stream.Send(&pb.LogEntry{
				Type:    "full_log",
				Message: msg,
			}); err != nil {
				return err
			}
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
				if err == nil {
					if r.State != domain.RunStateRunning && r.State != domain.RunStatePending {
						_ = stream.Send(&pb.LogEntry{
							Type:      "run_completed",
							Result:    string(r.State),
							Timestamp: r.CompletedAt.String(),
							Message:   r.ErrorMessage,
						})
					} else {
						// Safety net: broadcast finished but state not yet settled.
						// Send whatever static content exists so the user isn't left
						// with an empty stream.
						flp919 := filepath.Join(runLogDir(r, r.ProjectDir, runID), "full.log")
						if _, err919 := os.Stat(flp919); os.IsNotExist(err919) {
							flp919 = filepath.Join(r.ProjectDir, ".cloche", runID, "output", "full.log")
						}
						fullLogPath := flp919
						if data, readErr := os.ReadFile(fullLogPath); readErr == nil && len(data) > 0 {
							_ = stream.Send(&pb.LogEntry{
								Type:    "full_log",
								Message: applyLimit(string(data), limit),
							})
						}
					}
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
	// Legacy output directory (v1 path); new v2 runs use runLogDir.
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

	// Fall back to file path conventions (try v2 path first, then legacy)
	if req.StepName != "" {
		v2LogDir := runLogDir(run, run.ProjectDir, req.RunId)
		wfPrefix := run.WorkflowName + "-"
		var candidates []string
		switch req.LogType {
		case "llm":
			candidates = []string{
				filepath.Join(v2LogDir, wfPrefix+"llm-"+req.StepName+".log"),
				filepath.Join(outputDir, "llm-"+req.StepName+".log"),
			}
		default:
			// Try v2 path (workflow-prefixed) then legacy path for .log and .out
			candidates = []string{
				filepath.Join(v2LogDir, wfPrefix+req.StepName+".log"),
				filepath.Join(v2LogDir, wfPrefix+req.StepName+".out"),
				filepath.Join(outputDir, req.StepName+".log"),
				filepath.Join(outputDir, req.StepName+".out"),
			}
		}

		for _, logPath := range candidates {
			data, err := os.ReadFile(logPath)
			if err != nil || len(data) == 0 {
				continue
			}
			return stream.Send(&pb.LogEntry{
				Type:     "step_log",
				StepName: req.StepName,
				Message:  applyLimit(string(data), limit),
			})
		}
		return fmt.Errorf("log file not found for step %q", req.StepName)
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
	taskID := req.TaskId
	if taskID == "" {
		return nil, fmt.Errorf("task_id is required")
	}

	// Find all active runs for this task.
	activeRuns, err := s.store.ListRunsFiltered(ctx, domain.RunListFilter{TaskID: taskID})
	if err != nil {
		return nil, fmt.Errorf("listing runs for task %q: %w", taskID, err)
	}

	var stopped int
	for _, run := range activeRuns {
		if run.State != domain.RunStatePending && run.State != domain.RunStateRunning {
			continue
		}

		s.mu.Lock()
		containerID, ok := s.runIDs[run.ID]
		s.mu.Unlock()

		if ok {
			if stopErr := s.container.Stop(ctx, containerID); stopErr != nil {
				log.Printf("server: stop task %s: stopping container for run %s: %v", taskID, run.ID, stopErr)
			}
		}

		run.Complete(domain.RunStateCancelled)
		_ = s.store.UpdateRun(ctx, run)
		stopped++
	}

	if stopped == 0 {
		return nil, fmt.Errorf("task %q has no active runs", taskID)
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

	// Ensure per-project log migration has run.
	if migrator, ok := s.store.(ports.ProjectMigrator); ok {
		_ = migrator.MigrateProjectLogs(projectDir)
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
		ProjectDir:             projectDir,
		MaxConcurrent:          maxConc,
		StaggerDelay:           stagger,
		DedupTimeout:           dedupTimeout,
		StopOnError:            projCfg.Orchestration.StopOnError,
		MaxConsecutiveFailures: projCfg.Orchestration.MaxConsecutiveFailures,
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
	mainFn := func(ctx context.Context, projDir string, taskID string, attemptID string) (*host.RunResult, error) {
		runner := &host.Runner{
			Dispatcher:   s,
			Store:        s.store,
			Captures:     s.captures,
			LogBroadcast: s.logBroadcast,
			TaskID:       taskID,
			AttemptID:    attemptID,
		}
		return runner.RunNamed(ctx, projDir, "main")
	}

	// Phase 3: finalize function (optional — only if a finalize host workflow exists)
	var finalizeFn host.FinalizeFunc
	if hostWFs, err := host.FindHostWorkflows(projectDir); err == nil {
		if _, hasFinalize := hostWFs["finalize"]; hasFinalize {
			finalizeFn = func(ctx context.Context, projDir string, taskID string, attemptID string, mainResult *host.RunResult) (*host.RunResult, error) {
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
					AttemptID:    attemptID,
					ParentRunID:  mainRunID, // nest finalize under the main run in the UI
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
	loop := host.NewPhaseLoop(loopCfg, s.store, listTasksFn, mainFn, finalizeFn)
	if s.attemptStore != nil {
		loop.SetAttemptStore(s.attemptStore)
	}
	if s.taskStore != nil {
		loop.SetTaskStore(s.taskStore)
	}
	return loop
}

// createLegacyLoop creates a legacy single-function orchestration loop.
func (s *ClocheServer) createLegacyLoop(loopCfg host.LoopConfig, projectDir string, projCfg *config.Config, dedupTimeout time.Duration) *host.Loop {
	runFn := func(ctx context.Context, projDir string, taskID string, attemptID string) (*host.RunResult, error) {
		runner := &host.Runner{
			Dispatcher:   s,
			Store:        s.store,
			Captures:     s.captures,
			LogBroadcast: s.logBroadcast,
			TaskID:       taskID,
			AttemptID:    attemptID,
		}
		return runner.Run(ctx, projDir)
	}

	loop := host.NewLoop(loopCfg, s.store, runFn)

	// Configure daemon-managed task assignment if a list-tasks command is set.
	if cmd := projCfg.Orchestration.ListTasksCommand; cmd != "" {
		loop.SetTaskAssigner(&host.ScriptTaskAssigner{Command: cmd})
		log.Printf("orchestration loop: task assignment enabled for %s (command=%q, dedup=%s)", projectDir, cmd, dedupTimeout)
	}

	if s.attemptStore != nil {
		loop.SetAttemptStore(s.attemptStore)
	}
	if s.taskStore != nil {
		loop.SetTaskStore(s.taskStore)
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
		StopOnError:            cfg.Orchestration.StopOnError,
		MaxConsecutiveFailures: int32(cfg.Orchestration.MaxConsecutiveFailures),
		ErrorHalted:            errorHalted,
		HaltError:              haltError,
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
// workflowName is used to parse v2 filenames (e.g. develop-build.log -> step "build").
// Both v2 (workflow-prefixed) and v1 (bare step name) filename formats are supported.
func (s *ClocheServer) indexLogFiles(ctx context.Context, runID, outputDir, workflowName string) {
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
		case workflowName != "" && strings.HasPrefix(base, workflowName+"-llm-"):
			// v2 workflow-prefixed LLM log: <workflow>-llm-<step>.log
			fileType = "llm"
			stepName = strings.TrimPrefix(base, workflowName+"-llm-")
		case workflowName != "" && strings.HasPrefix(base, workflowName+"-"):
			// v2 workflow-prefixed script log: <workflow>-<step>.log
			fileType = "script"
			stepName = strings.TrimPrefix(base, workflowName+"-")
		case strings.HasPrefix(name, "llm-"):
			// v1 legacy LLM log: llm-<step>.log
			fileType = "llm"
			stepName = strings.TrimPrefix(base, "llm-")
		default:
			// v1 legacy script log: <step>.log
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
