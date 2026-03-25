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
	"github.com/cloche-dev/cloche/internal/activitylog"
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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type ClocheServer struct {
	pb.UnimplementedClocheServiceServer
	store         ports.RunStore
	captures      ports.CaptureStore
	logStore      ports.LogStore
	taskStore     ports.TaskStore     // optional; creates Task records
	attemptStore  ports.AttemptStore  // optional; creates Attempt records
	activityStore ports.ActivityStore // optional; backs per-project activity loggers
	container     ports.ContainerRuntime
	pool          *docker.ContainerPool // optional; manages agent sessions for DaemonExecutor
	defaultImage  string
	evolution     *evolution.Trigger
	logBroadcast  *logstream.Broadcaster
	shutdownFn    func()
	mu              sync.Mutex
	runIDs          map[string]string             // run_id -> container_id
	hostCancels     map[string]context.CancelFunc // run_id -> cancel fn (for host runs)
	loops           map[string]*host.Loop         // project_dir -> orchestration loop
	activityLoggers map[string]*activitylog.Logger // project_dir -> activity logger
}

func NewClocheServer(store ports.RunStore, container ports.ContainerRuntime) *ClocheServer {
	return &ClocheServer{
		store:           store,
		container:       container,
		runIDs:          make(map[string]string),
		hostCancels:     make(map[string]context.CancelFunc),
		loops:           make(map[string]*host.Loop),
		activityLoggers: make(map[string]*activitylog.Logger),
	}
}

func NewClocheServerWithCaptures(store ports.RunStore, captures ports.CaptureStore, container ports.ContainerRuntime, defaultImage string) *ClocheServer {
	return &ClocheServer{
		store:           store,
		captures:        captures,
		container:       container,
		defaultImage:    defaultImage,
		runIDs:          make(map[string]string),
		hostCancels:     make(map[string]context.CancelFunc),
		loops:           make(map[string]*host.Loop),
		activityLoggers: make(map[string]*activitylog.Logger),
	}
}

// activityLoggerFor returns a cached activity logger for the given project directory,
// creating one on first access. Returns nil if projectDir is empty.
func (s *ClocheServer) activityLoggerFor(projectDir string) *activitylog.Logger {
	if projectDir == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.activityLoggerLocked(projectDir)
}

// haltProjectLoop halts the orchestration loop for the given project directory,
// if one is active. Used when container failures are detected to prevent
// the loop from spinning in a failure cycle.
func (s *ClocheServer) haltProjectLoop(projectDir, reason string) {
	if projectDir == "" {
		return
	}
	s.mu.Lock()
	loop, ok := s.loops[projectDir]
	s.mu.Unlock()
	if ok && loop != nil {
		loop.Halt(reason)
		log.Printf("orchestration loop halted for %s: %s", projectDir, reason)
	}
}

// activityLoggerLocked is like activityLoggerFor but assumes s.mu is already held.
func (s *ClocheServer) activityLoggerLocked(projectDir string) *activitylog.Logger {
	if projectDir == "" || s.activityStore == nil {
		return nil
	}
	if l, ok := s.activityLoggers[projectDir]; ok {
		return l
	}
	l := activitylog.NewLogger(projectDir, s.activityStore)
	s.activityLoggers[projectDir] = l
	return l
}

// SetActivityStore attaches an activity store so the server records step and
// attempt lifecycle events to the daemon's SQLite database.
func (s *ClocheServer) SetActivityStore(as ports.ActivityStore) {
	s.activityStore = as
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

// SetContainerPool attaches a ContainerPool so the AgentSession handler can
// register agent streams for step dispatch by the DaemonExecutor.
func (s *ClocheServer) SetContainerPool(pool *docker.ContainerPool) {
	s.pool = pool
}

// AgentSession handles the bidirectional gRPC stream between the daemon and an
// in-container agent. The agent sends AgentReady first, then receives
// ExecuteStep commands and sends back StepResult messages.
func (s *ClocheServer) AgentSession(stream pb.ClocheService_AgentSessionServer) error {
	// First message must be AgentReady.
	msg, err := stream.Recv()
	if err != nil {
		return fmt.Errorf("receiving AgentReady: %w", err)
	}
	ready := msg.GetReady()
	if ready == nil {
		return fmt.Errorf("first AgentSession message must be AgentReady")
	}

	containerID := ready.RunId // RunId carries the container/run identifier
	log.Printf("agent session: agent ready (containerID=%s attemptID=%s)", containerID, ready.AttemptId)

	if s.pool == nil {
		return fmt.Errorf("no container pool configured on server")
	}

	// Protect concurrent sends over the stream.
	var sendMu sync.Mutex
	sendFn := func(dmsg *pb.DaemonMessage) error {
		sendMu.Lock()
		defer sendMu.Unlock()
		return stream.Send(dmsg)
	}

	// Register the send function with the pool and unblock SessionFor.
	s.pool.NotifyReadyWithStream(containerID, sendFn)

	// Loop receiving messages from the agent.
	for {
		msg, err := stream.Recv()
		if err != nil {
			// Stream closed: normal shutdown or network error.
			log.Printf("agent session: stream closed (containerID=%s): %v", containerID, err)
			return nil
		}

		switch payload := msg.Payload.(type) {
		case *pb.AgentMessage_StepResult:
			result := payload.StepResult
			log.Printf("agent session: step result (containerID=%s requestID=%s result=%s)",
				containerID, result.RequestId, result.Result)
			s.pool.DeliverResult(containerID, result)

		case *pb.AgentMessage_StepLog:
			if s.logBroadcast != nil && payload.StepLog != nil {
				// TODO: associate log line with the correct run ID
				_ = payload.StepLog
			}

		case *pb.AgentMessage_StepStarted:
			// Informational; the engine tracks step state via StepResult.

		case *pb.AgentMessage_Ready:
			// Duplicate AgentReady: ignore.
			log.Printf("agent session: unexpected duplicate AgentReady (containerID=%s)", containerID)

		default:
			// Unknown message type: ignore for forward compatibility.
		}
	}
}

func (s *ClocheServer) RunWorkflow(ctx context.Context, req *pb.RunWorkflowRequest) (*pb.RunWorkflowResponse, error) {
	// Ensure per-project log migration has run for this project.
	if migrator, ok := s.store.(ports.ProjectMigrator); ok {
		_ = migrator.MigrateProjectLogs(req.ProjectDir)
	}

	// Check for resume mode via gRPC metadata.
	// Two paths: composite ID (colon-separated, resolved via resolveRunIDFromID),
	// or bare task/run ID that needs resolution via resolveResumeTarget.
	if resumeRunID := resumeRunIDFromContext(ctx); resumeRunID != "" {
		resolvedRunID, stepFromID, err := s.resolveRunIDFromID(ctx, resumeRunID)
		if err != nil {
			return nil, fmt.Errorf("resolving resume ID %q: %w", resumeRunID, err)
		}
		// Explicit step from metadata overrides step embedded in composite ID.
		step := resumeStepFromContext(ctx)
		if step == "" {
			step = stepFromID
		}
		return s.resumeRun(ctx, resolvedRunID, step)
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

	// Reuse a propagated attempt ID from a parent host executor, or create
	// a new Task/Attempt record for standalone container runs.
	attemptID := attemptIDFromContext(ctx)
	if attemptID == "" {
		attemptID = s.ensureTaskAndAttempt(ctx, req.IssueId, req.Title, req.ProjectDir)
	}

	runID := domain.GenerateRunID(workflowName, attemptID)

	run := domain.NewRun(runID, workflowName)
	run.ProjectDir = req.ProjectDir
	run.Title = req.Title
	run.TaskID = req.IssueId
	run.AttemptID = attemptID
	if run.TaskID == "" {
		// User-initiated run: task ID was synthesized during ensureTaskAndAttempt.
		// Derive it from the attempt prefix for backward compat with log paths.
		run.TaskID = "user-" + attemptID
	}

	// Write prompt to .cloche/runs/<task-id>/prompt.txt
	if req.Prompt != "" {
		promptPath := filepath.Join(req.ProjectDir, ".cloche", "runs", run.TaskID, "prompt.txt")
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

	// Resolve image: request-level override, per-project config, then server default.
	image := req.Image
	if image == "" {
		if projCfg, err := config.Load(req.ProjectDir); err == nil && projCfg.Daemon.Image != "" {
			image = projCfg.Daemon.Image
		} else {
			image = s.defaultImage
		}
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
	runID := domain.GenerateRunID(hostWorkflowName, attemptID)

	runner := &host.Runner{
		Store:        s.store,
		Captures:     s.captures,
		LogBroadcast: s.logBroadcast,
		ActivityLog:  s.activityLoggerFor(req.ProjectDir),
		TaskID:       taskID,
		AttemptID:    attemptID,
	}

	runCtx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.hostCancels[runID] = cancel
	s.mu.Unlock()
	go func() {
		defer func() {
			s.mu.Lock()
			delete(s.hostCancels, runID)
			s.mu.Unlock()
			cancel()
		}()
		runner.RunNamedWithID(runCtx, req.ProjectDir, hostWorkflowName, runID)
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
	// Check incoming metadata (set by gRPC transport for remote calls).
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if vals := md.Get(host.AttemptIDMetadataKey); len(vals) > 0 {
			return vals[0]
		}
	}
	// Check outgoing metadata (set by host executor for in-process calls
	// where the gRPC transport layer doesn't convert outgoing→incoming).
	if md, ok := metadata.FromOutgoingContext(ctx); ok {
		if vals := md.Get(host.AttemptIDMetadataKey); len(vals) > 0 {
			return vals[0]
		}
	}
	return ""
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

// createResumeAttempt creates a new Attempt that records lineage back to the
// previous attempt. If the attempt store is not configured, the attempt object
// is returned without being persisted (non-fatal).
func (s *ClocheServer) createResumeAttempt(ctx context.Context, oldRun *domain.Run) *domain.Attempt {
	newAttemptID := domain.GenerateAttemptID()
	attempt := &domain.Attempt{
		ID:                newAttemptID,
		TaskID:            oldRun.TaskID,
		PreviousAttemptID: oldRun.AttemptID,
		StartedAt:         time.Now(),
		Result:            domain.AttemptResultRunning,
	}
	if s.attemptStore != nil && oldRun.TaskID != "" {
		if err := s.attemptStore.SaveAttempt(ctx, attempt); err != nil {
			log.Printf("server: failed to save resume attempt for task %s: %v", oldRun.TaskID, err)
		}
	}
	return attempt
}

// resumeHostRun creates a new attempt and run for resuming a failed host workflow.
// The old run is left in its failed state for lineage tracing.
func (s *ClocheServer) resumeHostRun(ctx context.Context, run *domain.Run, stepName string) (*pb.RunWorkflowResponse, error) {
	newAttempt := s.createResumeAttempt(ctx, run)
	newRunID := domain.GenerateRunID(run.WorkflowName, newAttempt.ID)

	runner := &host.Runner{
		Store:        s.store,
		Captures:     s.captures,
		LogBroadcast: s.logBroadcast,
		ActivityLog:  s.activityLoggerFor(run.ProjectDir),
		TaskID:       run.TaskID,
		AttemptID:    newAttempt.ID,
	}

	resumeCtx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.hostCancels[newRunID] = cancel
	s.mu.Unlock()
	go func() {
		defer func() {
			s.mu.Lock()
			delete(s.hostCancels, newRunID)
			s.mu.Unlock()
			cancel()
		}()
		result, runErr := runner.ResumeRunAsNewAttempt(resumeCtx, run, stepName, newRunID)
		s.completeAttemptFromResult(newAttempt.ID, newAttempt.TaskID, result, runErr)
	}()

	return &pb.RunWorkflowResponse{RunId: newRunID, AttemptId: newAttempt.ID}, nil
}

// completeAttemptFromResult marks an attempt as complete based on the outcome
// of a host run. No-op when attempt tracking is not configured or IDs are empty.
func (s *ClocheServer) completeAttemptFromResult(attemptID, taskID string, result *host.RunResult, runErr error) {
	if s.attemptStore == nil || attemptID == "" || taskID == "" {
		return
	}
	ctx := context.Background()
	attempt, err := s.attemptStore.GetAttempt(ctx, attemptID)
	if err != nil {
		log.Printf("server: failed to get attempt %s for completion: %v", attemptID, err)
		return
	}
	var ar domain.AttemptResult
	switch {
	case runErr == nil && result != nil && result.State == domain.RunStateSucceeded:
		ar = domain.AttemptResultSucceeded
	case runErr == nil && result != nil && result.State == domain.RunStateCancelled:
		ar = domain.AttemptResultCancelled
	default:
		ar = domain.AttemptResultFailed
	}
	attempt.Complete(ar)
	if err := s.attemptStore.SaveAttempt(ctx, attempt); err != nil {
		log.Printf("server: failed to complete attempt %s: %v", attemptID, err)
	}
}

// resumeContainerRun creates a new attempt and run for resuming a failed
// container workflow. The committed container image captures workspace state;
// the new run starts from that image and re-executes from the failed step.
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

	resumeCmd := []string{
		"cloche-agent", ".cloche/" + run.WorkflowName + ".cloche",
		"--resume-from", stepName,
	}

	// Create a new attempt with lineage back to the previous one.
	newAttempt := s.createResumeAttempt(ctx, run)
	newRunID := domain.GenerateRunID(run.WorkflowName, newAttempt.ID)

	// Create a new run record for the new attempt; the old run stays failed.
	newRun := domain.NewRun(newRunID, run.WorkflowName)
	newRun.ProjectDir = run.ProjectDir
	newRun.TaskID = run.TaskID
	newRun.TaskTitle = run.TaskTitle
	newRun.AttemptID = newAttempt.ID
	newRun.ParentRunID = run.ParentRunID
	if err := s.store.CreateRun(ctx, newRun); err != nil {
		return nil, fmt.Errorf("creating resume run record: %w", err)
	}

	// Start new container from committed image with resume command
	go s.launchResumeContainer(newRun, imageID, resumeCmd)

	return &pb.RunWorkflowResponse{RunId: newRunID, AttemptId: newAttempt.ID}, nil
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
		TaskID:       run.TaskID,
		AttemptID:    run.AttemptID,
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
			if s.logBroadcast != nil {
				s.logBroadcast.Finish(runID)
			}
			log.Printf("run %s: failed to ensure image: %v", runID, err)
			s.haltProjectLoop(req.ProjectDir, fmt.Sprintf("image build failed for run %s: %v", runID, err))
			return
		}
	}

	baseSHA := gitHEAD(req.ProjectDir)

	// Look up the task/attempt IDs from the run record for container env and naming.
	var taskID, attemptID string
	if r, err := s.store.GetRun(ctx, runID); err == nil && r != nil {
		taskID = r.TaskID
		attemptID = r.AttemptID
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
		AttemptID:    attemptID,
		NetworkAllow: []string{"*"},
		Cmd:          cmd,
		Prompt:       req.Prompt,
	})
	if err != nil {
		run, _ := s.store.GetRun(ctx, runID)
		if run != nil {
			run.Fail(fmt.Sprintf("failed to start container: %v", err))
			_ = s.store.UpdateRun(ctx, run)
		}
		if s.logBroadcast != nil {
			s.logBroadcast.Finish(runID)
		}
		log.Printf("run %s: failed to start container: %v", runID, err)
		s.haltProjectLoop(req.ProjectDir, fmt.Sprintf("container failed to start for run %s: %v", runID, err))
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
		if run, rerr := s.store.GetRun(ctx, runID); rerr == nil && run != nil && run.State == domain.RunStateRunning {
			run.Fail(fmt.Sprintf("failed to attach to container output: %v", err))
			_ = s.store.UpdateRun(ctx, run)
		}
		if s.logBroadcast != nil {
			s.logBroadcast.Finish(runID)
		}
		s.mu.Lock()
		delete(s.runIDs, runID)
		s.mu.Unlock()
		s.haltProjectLoop(projectDir, fmt.Sprintf("container output attach failed for run %s: %v", runID, err))
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
				exec := &domain.StepExecution{
					StepName:    msg.StepName,
					Result:      msg.Result,
					CompletedAt: msg.Timestamp,
				}
				if msg.InputTokens > 0 || msg.OutputTokens > 0 || msg.AgentName != "" {
					exec.Usage = &domain.TokenUsage{
						InputTokens:  msg.InputTokens,
						OutputTokens: msg.OutputTokens,
						AgentName:    msg.AgentName,
					}
				}
				_ = s.captures.SaveCapture(ctx, runID, exec)
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
		unexpectedExit := false
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
			// Container exited with a non-zero code without reporting a result —
			// this is an unexpected crash or abort, not a normal workflow failure.
			run.Fail(fmt.Sprintf("container exited with code %d", exitCode))
			unexpectedExit = true
		}
		_ = s.store.UpdateRun(ctx, run)
		if unexpectedExit {
			s.haltProjectLoop(projectDir, fmt.Sprintf("container exited unexpectedly with code %d for run %s", exitCode, runID))
		}
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
		completions = append(completions, s.activeOrRecentTaskAndAttemptIDs(ctx, projectDir)...)
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
		completions = s.activeOrRecentTaskAndAttemptIDs(ctx, projectDir)

	default:
		// No dynamic completions for other subcommands.
	}

	return &pb.CompleteResponse{Completions: fuzzyFilterPrefix(completions, cur)}, nil
}

// fuzzyFilterPrefix returns items from list that match prefix either by
// exact string prefix or by prefix-matching any colon-delimited component.
// This allows partial attempt IDs (e.g. "1fka") to match composite IDs
// like "task-id:1fka" without requiring the user to know the task ID.
// If prefix is empty, all items are returned.
func fuzzyFilterPrefix(list []string, prefix string) []string {
	if prefix == "" {
		return list
	}
	var out []string
	for _, s := range list {
		if MatchesFuzzy(s, prefix) {
			out = append(out, s)
		}
	}
	return out
}

// MatchesFuzzy returns true if prefix matches the beginning of s, or matches
// the beginning of any colon-delimited component of s.
// Exported so it can be exercised directly in tests.
func MatchesFuzzy(s, prefix string) bool {
	if strings.HasPrefix(s, prefix) {
		return true
	}
	for _, part := range strings.Split(s, ":") {
		if strings.HasPrefix(part, prefix) {
			return true
		}
	}
	return false
}

// filterPrefix returns items from list that start with prefix.
// Kept for use in subcommand completion where component matching is not needed.
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
	wfs, err := host.FindAllWorkflows(projectDir)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(wfs))
	for name := range wfs {
		names = append(names, name)
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

// activeOrRecentRunIDs returns IDs of currently active (running/pending) runs
// and runs that completed within the last 10 minutes.
func (s *ClocheServer) activeOrRecentRunIDs(ctx context.Context, projectDir string) []string {
	since := time.Now().Add(-10 * time.Minute)
	filter := domain.RunListFilter{ProjectDir: projectDir, Since: since, Limit: 20}
	runs, err := s.store.ListRunsFiltered(ctx, filter)
	if err != nil {
		runs, err = s.store.ListRunsFiltered(ctx, domain.RunListFilter{Since: since, Limit: 20})
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

// activeOrRecentTaskAndAttemptIDs returns task and attempt IDs for tasks that
// are currently active (running/pending) or were recently completed within the
// last 10 minutes. This provides context-appropriate suggestions for commands
// like "status" and "poll" that operate on in-flight or just-finished work.
func (s *ClocheServer) activeOrRecentTaskAndAttemptIDs(ctx context.Context, projectDir string) []string {
	cutoff := time.Now().Add(-10 * time.Minute)
	var ids []string

	if s.taskStore != nil {
		tasks, err := s.taskStore.ListTasks(ctx, projectDir)
		if err == nil {
			for _, t := range tasks {
				hasActiveOrRecent := false
				for _, a := range t.Attempts {
					if a.Result == domain.AttemptResultRunning || (!a.EndedAt.IsZero() && a.EndedAt.After(cutoff)) {
						hasActiveOrRecent = true
						break
					}
				}
				if hasActiveOrRecent {
					ids = append(ids, t.ID)
					for _, a := range t.Attempts {
						if a.Result == domain.AttemptResultRunning || (!a.EndedAt.IsZero() && a.EndedAt.After(cutoff)) {
							ids = append(ids, t.ID+":"+a.ID)
						}
					}
				}
			}
		}
	}

	// Fallback: active or recently completed run IDs.
	if len(ids) == 0 {
		ids = s.activeOrRecentRunIDs(ctx, projectDir)
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
		// Fall back to task_id:attempt_id (with parts[2] as workflow name or step name)
		run, e = s.findRunByTaskAndAttempt(ctx, parts[0], parts[1])
		if e != nil {
			return "", "", fmt.Errorf("no run found for task %q attempt %q: %w", parts[0], parts[1], e)
		}
		// If parts[2] matches the run's workflow name, treat as workflow ID (no step).
		// This handles the canonical task:attempt:workflow format (e.g. TASK-123:a41k:develop).
		if parts[2] == run.WorkflowName {
			return run.ID, "", nil
		}
		// Otherwise treat parts[2] as a step name.
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
				se := &pb.StepExecutionStatus{
					StepName:    exec.StepName,
					Result:      exec.Result,
					StartedAt:   exec.StartedAt.String(),
					CompletedAt: exec.CompletedAt.String(),
				}
				if exec.Usage != nil {
					se.InputTokens = exec.Usage.InputTokens
					se.OutputTokens = exec.Usage.OutputTokens
					se.AgentName = exec.Usage.AgentName
				}
				resp.StepExecutions = append(resp.StepExecutions, se)
			}
		}
	} else {
		for _, exec := range run.StepExecutions {
			se := &pb.StepExecutionStatus{
				StepName:    exec.StepName,
				Result:      exec.Result,
				StartedAt:   exec.StartedAt.String(),
				CompletedAt: exec.CompletedAt.String(),
			}
			if exec.Usage != nil {
				se.InputTokens = exec.Usage.InputTokens
				se.OutputTokens = exec.Usage.OutputTokens
				se.AgentName = exec.Usage.AgentName
			}
			resp.StepExecutions = append(resp.StepExecutions, se)
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
		if err := sendContentChunked(stream, "full_log", "", "", "", msg); err != nil {
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
			v2Path := filepath.Join(v2Dir, v2Prefix+exec.StepName+".log")
			if data, err := os.ReadFile(v2Path); err == nil && len(data) > 0 {
				output = string(data)
			}
			if output == "" {
				outputPath := filepath.Join(run.ProjectDir, ".cloche", req.RunId, "output", exec.StepName+".log")
				if data, err := os.ReadFile(outputPath); err == nil && len(data) > 0 {
					output = string(data)
				}
			}
			if err := sendContentChunked(stream, "step_completed", exec.StepName, exec.Result, exec.CompletedAt.String(), applyLimit(output, limit)); err != nil {
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

// maxLogChunkSize is the maximum size of a single LogEntry message sent over
// gRPC. The gRPC default max message size is 4MB; we use 512KB to stay well
// under that limit and allow for protobuf encoding overhead.
const maxLogChunkSize = 512 * 1024

// splitIntoChunks splits content into chunks at line boundaries, each at most
// maxSize bytes. Returns a single-element slice for small content.
func splitIntoChunks(content string, maxSize int) []string {
	if len(content) <= maxSize {
		return []string{content}
	}
	lines := strings.Split(content, "\n")
	// Remove trailing empty string from a terminal newline.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	var chunks []string
	var cur strings.Builder
	for _, line := range lines {
		lineStr := line + "\n"
		if cur.Len() > 0 && cur.Len()+len(lineStr) > maxSize {
			chunks = append(chunks, cur.String())
			cur.Reset()
		}
		cur.WriteString(lineStr)
	}
	if cur.Len() > 0 {
		chunks = append(chunks, cur.String())
	}
	if len(chunks) == 0 {
		return []string{content}
	}
	return chunks
}

// sendContentChunked sends content as one or more LogEntry messages to avoid
// exceeding the gRPC per-message size limit. The first message uses the
// provided entry type with all metadata fields; any additional chunks are sent
// as "log_chunk" entries so the client can append them without re-printing headers.
func sendContentChunked(stream rpcgrpc.ServerStreamingServer[pb.LogEntry], entryType, stepName, result, timestamp, content string) error {
	chunks := splitIntoChunks(content, maxLogChunkSize)
	for i, chunk := range chunks {
		var entry *pb.LogEntry
		if i == 0 {
			entry = &pb.LogEntry{
				Type:      entryType,
				StepName:  stepName,
				Result:    result,
				Timestamp: timestamp,
				Message:   chunk,
			}
		} else {
			entry = &pb.LogEntry{
				Type:     "log_chunk",
				StepName: stepName,
				Message:  chunk,
			}
		}
		if err := stream.Send(entry); err != nil {
			return err
		}
	}
	return nil
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
			if err := sendContentChunked(stream, "full_log", "", "", "", msg); err != nil {
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
							_ = sendContentChunked(stream, "full_log", "", "", "", applyLimit(string(data), limit))
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
				if err := sendContentChunked(stream, lf.FileType+"_log", lf.StepName, "", "", applyLimit(string(data), limit)); err != nil {
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
			// Try v2 path (workflow-prefixed) then legacy path
			candidates = []string{
				filepath.Join(v2LogDir, wfPrefix+req.StepName+".log"),
				filepath.Join(outputDir, req.StepName+".log"),
			}
		}

		for _, logPath := range candidates {
			data, err := os.ReadFile(logPath)
			if err != nil || len(data) == 0 {
				continue
			}
			return sendContentChunked(stream, "step_log", req.StepName, "", "", applyLimit(string(data), limit))
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
		cancelFn, isHostRun := s.hostCancels[run.ID]
		s.mu.Unlock()

		if ok {
			if stopErr := s.container.Stop(ctx, containerID); stopErr != nil {
				log.Printf("server: stop task %s: stopping container for run %s: %v", taskID, run.ID, stopErr)
			}
		}

		run.Complete(domain.RunStateCancelled)
		_ = s.store.UpdateRun(ctx, run)

		if isHostRun {
			cancelFn()
		}

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

	loop := s.createPhaseLoop(loopCfg, projectDir, dedupTimeout)

	s.loops[projectDir] = loop
	loop.Start()

	return &pb.EnableLoopResponse{}, nil
}

// createPhaseLoop creates a two-phase orchestration loop using host workflows
// from any .cloche file in the project. When a list-tasks workflow exists, tasks
// are discovered through it; otherwise main runs continuously with an untracked
// sentinel task.
func (s *ClocheServer) createPhaseLoop(loopCfg host.LoopConfig, projectDir string, dedupTimeout time.Duration) *host.Loop {
	hostWFs, _ := host.FindHostWorkflows(projectDir)
	alog := s.activityLoggerLocked(projectDir)

	// Phase 1: list-tasks function.
	// If no list-tasks workflow is defined, return a single untracked sentinel
	// task (empty ID) so the main workflow runs continuously.
	var listTasksFn host.ListTasksFunc
	if _, hasListTasks := hostWFs["list-tasks"]; hasListTasks {
		listTasksFn = func(ctx context.Context, projDir string) ([]host.Task, error) {
			runner := &host.Runner{
				Store: s.store,
			}
			tasks, _, err := host.RunListTasksWorkflow(ctx, runner, projDir)
			return tasks, err
		}
		log.Printf("orchestration loop: two-phase mode enabled for %s (list-tasks + main, dedup=%s)", projectDir, dedupTimeout)
	} else {
		listTasksFn = func(ctx context.Context, projDir string) ([]host.Task, error) {
			return []host.Task{{ID: ""}}, nil
		}
		log.Printf("orchestration loop: continuous mode enabled for %s (main runs unconditionally, dedup=%s)", projectDir, dedupTimeout)
	}

	// Phase 2: main function
	mainFn := func(ctx context.Context, projDir string, taskID string, taskTitle string, attemptID string) (*host.RunResult, error) {
		runner := &host.Runner{
			Store:        s.store,
			Captures:     s.captures,
			LogBroadcast: s.logBroadcast,
			ActivityLog:  alog,
			TaskID:       taskID,
			TaskTitle:    taskTitle,
			AttemptID:    attemptID,
		}
		return runner.RunNamed(ctx, projDir, "main")
	}

	loop := host.NewPhaseLoop(loopCfg, s.store, listTasksFn, mainFn)
	if s.attemptStore != nil {
		loop.SetAttemptStore(s.attemptStore)
	}
	if s.taskStore != nil {
		loop.SetTaskStore(s.taskStore)
	}
	loop.SetActivityLogger(alog)
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

// GetUsage returns aggregated token usage statistics for a project or globally.
func (s *ClocheServer) GetUsage(ctx context.Context, req *pb.GetUsageRequest) (*pb.GetUsageResponse, error) {
	q := ports.UsageQuery{
		ProjectDir: req.ProjectDir,
		AgentName:  req.AgentName,
	}
	if req.WindowSeconds > 0 {
		q.Since = time.Now().Add(-time.Duration(req.WindowSeconds) * time.Second)
		q.Until = time.Now()
	}

	summaries, err := s.store.QueryUsage(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("querying usage: %w", err)
	}

	resp := &pb.GetUsageResponse{}
	for _, s := range summaries {
		resp.Summaries = append(resp.Summaries, &pb.UsageSummary{
			AgentName:    s.AgentName,
			InputTokens:  s.InputTokens,
			OutputTokens: s.OutputTokens,
			TotalTokens:  s.TotalTokens,
			BurnRate:     s.BurnRate,
		})
	}
	return resp, nil
}

// Console handles a bidirectional streaming RPC that starts an interactive
// container session and forwards I/O between the gRPC stream and the container.
func (s *ClocheServer) Console(stream pb.ClocheService_ConsoleServer) error {
	// First message must be ConsoleStart.
	in, err := stream.Recv()
	if err != nil {
		return fmt.Errorf("receiving ConsoleStart: %w", err)
	}
	start := in.GetStart()
	if start == nil {
		return fmt.Errorf("first message must be ConsoleStart")
	}
	if s.container == nil {
		return fmt.Errorf("no container runtime configured")
	}

	projectDir := start.ProjectDir
	if projectDir == "" {
		return fmt.Errorf("project_dir is required")
	}

	agentCmd := resolveConsoleAgentCommand(start.AgentCommand, projectDir)

	ctx := context.Background()
	image := s.defaultImage
	if projCfg, err := config.Load(projectDir); err == nil && projCfg.Daemon.Image != "" {
		image = projCfg.Daemon.Image
	}

	// Rebuild image if the Dockerfile has changed.
	if ensurer, ok := s.container.(ports.ImageEnsurer); ok {
		if err := ensurer.EnsureImage(ctx, projectDir, image); err != nil {
			return fmt.Errorf("ensuring image: %w", err)
		}
	}

	// Use a short random ID for the container name: console-<id>.
	shortID := domain.GenerateAttemptID()
	containerName := "console-" + shortID

	containerID, err := s.container.Start(ctx, ports.ContainerConfig{
		Image:       image,
		ProjectDir:  projectDir,
		RunID:       containerName,
		Interactive: true,
		Cmd:         []string{agentCmd},
	})
	if err != nil {
		return fmt.Errorf("starting container: %w", err)
	}
	// Container is intentionally kept after the session ends (no Remove call).

	conn, err := s.container.Attach(ctx, containerID)
	if err != nil {
		return fmt.Errorf("attaching to container: %w", err)
	}

	var closeOnce sync.Once
	closeConn := func() { closeOnce.Do(func() { conn.Close() }) }
	defer closeConn()

	// Apply initial terminal size if provided.
	if start.Rows > 0 && start.Cols > 0 {
		if resizer, ok := s.container.(ports.TerminalResizer); ok {
			_ = resizer.ResizeTerminal(ctx, containerID, int(start.Rows), int(start.Cols))
		}
	}

	// Send ConsoleStarted with the container ID.
	if err := stream.Send(&pb.ConsoleOutput{
		Payload: &pb.ConsoleOutput_Started{
			Started: &pb.ConsoleStarted{ContainerId: containerID},
		},
	}); err != nil {
		return fmt.Errorf("sending ConsoleStarted: %w", err)
	}

	// Output pump: container stdout → gRPC stream.
	outDone := make(chan struct{})
	go func() {
		defer close(outDone)
		buf := make([]byte, 4096)
		for {
			n, readErr := conn.Read(buf)
			if n > 0 {
				data := make([]byte, n)
				copy(data, buf[:n])
				if sendErr := stream.Send(&pb.ConsoleOutput{
					Payload: &pb.ConsoleOutput_Stdout{Stdout: data},
				}); sendErr != nil {
					return
				}
			}
			if readErr != nil {
				return
			}
		}
	}()

	// Input pump: gRPC stream → container stdin; resize events → ResizeTerminal.
	go func() {
		for {
			msg, recvErr := stream.Recv()
			if recvErr != nil {
				return
			}
			switch p := msg.Payload.(type) {
			case *pb.ConsoleInput_Stdin:
				if _, wErr := conn.Write(p.Stdin); wErr != nil {
					return
				}
			case *pb.ConsoleInput_Resize:
				if p.Resize != nil {
					if resizer, ok := s.container.(ports.TerminalResizer); ok {
						_ = resizer.ResizeTerminal(ctx, containerID, int(p.Resize.Rows), int(p.Resize.Cols))
					}
				}
			}
		}
	}()

	// Wait for container exit in a goroutine so we can also detect stream closure.
	waitDone := make(chan int, 1)
	go func() {
		code, waitErr := s.container.Wait(context.Background(), containerID)
		if waitErr != nil {
			code = -1
			log.Printf("console %s: waiting for container: %v", containerID, waitErr)
		}
		waitDone <- code
	}()

	var exitCode int
	select {
	case exitCode = <-waitDone:
		// Container exited — close conn so the output pump drains and exits.
		closeConn()
		<-outDone
	case <-stream.Context().Done():
		// Client disconnected — leave the container running per design.
		return nil
	case <-outDone:
		// Output pump exited (EOF or stream send error) — still wait for container exit.
		select {
		case exitCode = <-waitDone:
		case <-stream.Context().Done():
			return nil
		}
	}

	_ = stream.Send(&pb.ConsoleOutput{
		Payload: &pb.ConsoleOutput_Exited{
			Exited: &pb.ConsoleExited{ExitCode: int32(exitCode)},
		},
	})
	return nil
}

// resolveConsoleAgentCommand resolves the agent command for a console session.
// Resolution order: explicit flag → workflow config (container.agent_command)
// → CLOCHE_AGENT_COMMAND env → default "claude".
func resolveConsoleAgentCommand(flagCmd, projectDir string) string {
	if flagCmd != "" {
		return flagCmd
	}
	// Scan container workflow files for container.agent_command config.
	clochedir := filepath.Join(projectDir, ".cloche")
	if entries, err := os.ReadDir(clochedir); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".cloche") || e.Name() == "host.cloche" {
				continue
			}
			data, readErr := os.ReadFile(filepath.Join(clochedir, e.Name()))
			if readErr != nil {
				continue
			}
			wf, parseErr := dsl.ParseForContainer(string(data))
			if parseErr != nil {
				continue
			}
			if cmd := wf.Config["container.agent_command"]; cmd != "" {
				return cmd
			}
		}
	}
	if cmd, ok := os.LookupEnv("CLOCHE_AGENT_COMMAND"); ok && cmd != "" {
		return cmd
	}
	return "claude"
}

// GetContextKey retrieves a value from the per-attempt KV namespace.
func (s *ClocheServer) GetContextKey(ctx context.Context, req *pb.GetContextKeyRequest) (*pb.GetContextKeyResponse, error) {
	value, found, err := s.store.GetContextKey(ctx, req.TaskId, req.AttemptId, req.Key)
	if err != nil {
		return nil, err
	}
	return &pb.GetContextKeyResponse{Value: value, Found: found}, nil
}

// SetContextKey sets a value in the per-attempt KV namespace.
// Returns INVALID_ARGUMENT if len(value) > 1024.
func (s *ClocheServer) SetContextKey(ctx context.Context, req *pb.SetContextKeyRequest) (*pb.SetContextKeyResponse, error) {
	if len(req.Value) > 1024 {
		return nil, status.Errorf(codes.InvalidArgument, "value exceeds 1 KB limit (%d bytes)", len(req.Value))
	}
	if err := s.store.SetContextKey(ctx, req.TaskId, req.AttemptId, req.Key, req.Value); err != nil {
		return nil, err
	}
	return &pb.SetContextKeyResponse{}, nil
}

// ListContextKeys returns all keys in the per-attempt KV namespace.
func (s *ClocheServer) ListContextKeys(ctx context.Context, req *pb.ListContextKeysRequest) (*pb.ListContextKeysResponse, error) {
	keys, err := s.store.ListContextKeys(ctx, req.TaskId, req.AttemptId)
	if err != nil {
		return nil, err
	}
	return &pb.ListContextKeysResponse{Keys: keys}, nil
}

// stuckContainerThreshold is how long a container must have been dead before
// the stuck workflow scanner declares the associated run stuck.
const stuckContainerThreshold = 90 * time.Second

// StartStuckWorkflowScanner starts a background goroutine that periodically
// detects workflows stuck in "running" state due to undetected container exits
// (e.g. when AttachOutput or Wait fail silently). Call once after the server
// is fully initialised. The goroutine runs until ctx is cancelled.
func (s *ClocheServer) StartStuckWorkflowScanner(ctx context.Context) {
	go s.runStuckWorkflowScanner(ctx)
}

func (s *ClocheServer) runStuckWorkflowScanner(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.scanAndResolveStuckWorkflows(ctx)
		}
	}
}

// scanAndResolveStuckWorkflows inspects every actively-tracked container. If a
// container has been dead longer than stuckContainerThreshold but its run is
// still "running" in the store, the run is marked failed and the project's
// orchestration loop is halted to prevent failure loops.
func (s *ClocheServer) scanAndResolveStuckWorkflows(ctx context.Context) {
	// Snapshot the run→container map under lock so we don't hold the lock
	// during slow Inspect/store calls.
	s.mu.Lock()
	tracked := make(map[string]string, len(s.runIDs))
	for runID, cid := range s.runIDs {
		tracked[runID] = cid
	}
	s.mu.Unlock()

	for runID, containerID := range tracked {
		status, err := s.container.Inspect(ctx, containerID)
		if err != nil {
			// Container no longer exists — check if the run is still stuck.
			run, rerr := s.store.GetRun(ctx, runID)
			if rerr != nil || run == nil || run.State != domain.RunStateRunning {
				continue
			}
			log.Printf("stuck workflow scanner: container %s not found for run %s; marking failed", containerID, runID)
			run.Fail("container not found; may have been removed unexpectedly")
			_ = s.store.UpdateRun(ctx, run)
			if s.logBroadcast != nil {
				s.logBroadcast.Finish(runID)
			}
			s.mu.Lock()
			delete(s.runIDs, runID)
			s.mu.Unlock()
			s.haltProjectLoop(run.ProjectDir, fmt.Sprintf("container not found for run %s", runID))
			continue
		}

		if status.Running {
			continue // Container is alive; nothing to do.
		}

		// Container is dead but trackRun hasn't cleaned up yet. Only intervene
		// if it has been dead long enough that normal cleanup should have finished.
		if status.FinishedAt.IsZero() || time.Since(status.FinishedAt) < stuckContainerThreshold {
			continue
		}

		run, rerr := s.store.GetRun(ctx, runID)
		if rerr != nil || run == nil || run.State != domain.RunStateRunning {
			continue
		}

		log.Printf("stuck workflow scanner: run %s stuck — container %s dead since %v (exit %d); marking failed",
			runID, containerID, status.FinishedAt.Format(time.RFC3339), status.ExitCode)
		run.Fail(fmt.Sprintf("container exited with code %d (%s ago) but workflow remained running",
			status.ExitCode, time.Since(status.FinishedAt).Round(time.Second)))
		_ = s.store.UpdateRun(ctx, run)
		if s.logBroadcast != nil {
			s.logBroadcast.Finish(runID)
		}
		s.mu.Lock()
		delete(s.runIDs, runID)
		s.mu.Unlock()
		s.haltProjectLoop(run.ProjectDir, fmt.Sprintf(
			"stuck workflow detected for run %s: container exited with code %d", runID, status.ExitCode))
	}
}
