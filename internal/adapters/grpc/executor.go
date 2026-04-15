package grpc

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cloche-dev/cloche/internal/adapters/docker"
	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/engine"
	"github.com/cloche-dev/cloche/internal/host"
	"github.com/cloche-dev/cloche/internal/ports"
)

// DaemonExecutor implements engine.StepExecutor and routes steps based on the
// workflow's location. Host steps are delegated to the embedded host.Executor.
// Container steps are dispatched to the in-container agent via the ContainerPool.
// Workflow_name steps look up the target workflow and run it recursively using
// the appropriate executor.
type DaemonExecutor struct {
	// hostExec handles script and agent steps in host workflows.
	hostExec *host.Executor

	// pool manages container sessions for container workflow steps.
	pool *docker.ContainerPool

	// projectDir is the project root directory, used to look up workflows and
	// build container configs.
	projectDir string

	// attemptID identifies the current attempt, used as the pool session key.
	attemptID string

	// image is the container image to use when starting new containers.
	image string

	// allWFs is the full set of workflows (host and container) for the project,
	// keyed by name. Used to resolve workflow_name step targets.
	allWFs map[string]*domain.Workflow

	// store is used to set child_run_id in the KV store after extracting
	// container results to a git branch.
	store ports.RunStore

	// logStore is used to index extracted container step log files so the web
	// UI can serve them by step name. Optional: indexing is skipped when nil.
	logStore ports.LogStore

	// taskID is the task ID for KV store operations.
	taskID string

	// resumeMode, when true, sets the resume flag on all ExecuteStep messages
	// so the in-container agent continues an existing LLM conversation.
	resumeMode bool

	// onContainerStart is called after a container is started with (containerID).
	// The server uses this to register the container → run mapping so the
	// AgentSession handler can route StepLog messages to the right run.
	onContainerStart func(containerID string)

	// poolKeys tracks container pool keys used by this executor so Close()
	// can clean them all up after the host workflow finishes.
	poolKeys map[string]bool

	// closed tracks whether Close() has already been called.
	closed bool
}

// DaemonExecutorConfig holds configuration for constructing a DaemonExecutor.
type DaemonExecutorConfig struct {
	HostExec   *host.Executor
	Pool       *docker.ContainerPool
	Store      ports.RunStore
	LogStore   ports.LogStore
	ProjectDir string
	TaskID     string
	AttemptID  string
	Image      string
	AllWFs     map[string]*domain.Workflow
	// ResumeMode, when true, sets resume=true on all ExecuteStep messages so
	// that the in-container agent continues its previous LLM conversation.
	ResumeMode bool
	// OnContainerStart is called after a container starts with (containerID).
	OnContainerStart func(containerID string)
}

// NewDaemonExecutor creates a DaemonExecutor from the given config.
func NewDaemonExecutor(cfg DaemonExecutorConfig) *DaemonExecutor {
	return &DaemonExecutor{
		hostExec:         cfg.HostExec,
		pool:             cfg.Pool,
		store:            cfg.Store,
		logStore:         cfg.LogStore,
		projectDir:       cfg.ProjectDir,
		taskID:           cfg.TaskID,
		attemptID:        cfg.AttemptID,
		image:            cfg.Image,
		allWFs:           cfg.AllWFs,
		resumeMode:       cfg.ResumeMode,
		onContainerStart: cfg.OnContainerStart,
	}
}

// Ensure DaemonExecutor satisfies engine.StepExecutor.
var _ engine.StepExecutor = (*DaemonExecutor)(nil)

// Close cleans up all container pool entries used by this executor. Successful
// containers are stopped and removed; failed containers are stopped but kept
// for debugging. Must be called after the host workflow finishes.
func (d *DaemonExecutor) Close(succeeded bool) {
	if d.closed || d.pool == nil {
		return
	}
	d.closed = true
	ctx := context.Background()
	for key := range d.poolKeys {
		if err := d.pool.CleanupAttempt(ctx, key, false, succeeded); err != nil {
			log.Printf("daemon executor: cleanup pool key %s: %v", key, err)
		}
	}
}

// SetHostExecutor replaces the host executor with a fully-configured one.
// Implements engine.HostExecutorConfigurer.
func (d *DaemonExecutor) SetHostExecutor(exec engine.StepExecutor) {
	if he, ok := exec.(*host.Executor); ok {
		d.hostExec = he
	}
}

// Execute routes the step to the appropriate executor based on workflow location.
func (d *DaemonExecutor) Execute(ctx context.Context, step *domain.Step) (domain.StepResult, error) {
	// workflow_name steps are handled at the daemon level regardless of which
	// workflow they appear in.
	if step.Type == domain.StepTypeWorkflow {
		return d.executeWorkflowStep(ctx, step)
	}

	wf, ok := engine.WorkflowFromContext(ctx)
	if !ok {
		return domain.StepResult{}, fmt.Errorf("daemon executor: no workflow in context for step %q", step.Name)
	}

	if wf.Location == domain.LocationHost {
		return d.hostExec.Execute(ctx, step)
	}

	return d.executeContainerStep(ctx, step, wf)
}

// executeWorkflowStep looks up the target workflow by name, then runs it as a
// sub-workflow using this same executor, mapping the final state to a step result.
// For container sub-workflows, it extracts the container's workspace to a git
// branch and sets child_run_id in the KV store so downstream merge steps can
// find it.
func (d *DaemonExecutor) executeWorkflowStep(ctx context.Context, step *domain.Step) (domain.StepResult, error) {
	targetName := step.Config["workflow_name"]
	if targetName == "" {
		return domain.StepResult{}, fmt.Errorf("workflow step %q missing workflow_name config", step.Name)
	}

	targetWF, ok := d.allWFs[targetName]
	if !ok {
		return domain.StepResult{}, fmt.Errorf("workflow step %q: workflow %q not found in project", step.Name, targetName)
	}

	// Capture the base SHA before the sub-workflow runs so we can create a
	// branch from it after the container modifies files.
	baseSHA := gitHEAD(d.projectDir)

	// Generate a run ID for the child workflow (used as the branch name).
	childRunID := domain.GenerateRunID(targetName, d.attemptID)

	log.Printf("daemon executor: running sub-workflow %q for step %q (childRunID=%s)", targetName, step.Name, childRunID)

	// For container sub-workflows, register a deferred cleanup on failure so
	// the container is stopped if eng.Run returns an error (e.g. context
	// cancellation, daemon restart). On success the container stays in the pool
	// so subsequent sub-workflows sharing the same container.id can reuse it.
	// Final cleanup of successful containers happens in Close().
	succeeded := false
	if targetWF.Location == domain.LocationContainer && d.pool != nil {
		poolKey := d.attemptID + ":" + targetWF.ContainerID()
		if d.poolKeys == nil {
			d.poolKeys = make(map[string]bool)
		}
		d.poolKeys[poolKey] = true
		defer func() {
			if !succeeded {
				// Use background context: the original ctx may already be cancelled.
				cleanupCtx := context.Background()
				_ = d.pool.CleanupAttempt(cleanupCtx, poolKey, false, false)
			}
		}()
	}

	eng := engine.New(d)
	run, err := eng.Run(ctx, targetWF)
	if err != nil {
		log.Printf("daemon executor: sub-workflow %q failed: %v", targetName, err)
		// Even on error (e.g. context timeout), try to extract container logs so
		// they are accessible for post-mortem investigation. The original ctx may
		// already be cancelled, so use a background context. Only attempt
		// extraction if a session actually exists (i.e. the container started
		// before the failure).
		if targetWF.Location == domain.LocationContainer && d.pool != nil {
			poolKey := d.attemptID + ":" + targetWF.ContainerID()
			if session := d.pool.GetSession(poolKey); session != nil {
				bgCtx := context.Background()
				d.extractContainerLogs(bgCtx, session.ContainerID, step.Name)
				if d.logStore != nil && d.hostExec != nil && d.hostExec.HostRunID != "" {
					subDir := filepath.Join(d.projectDir, ".cloche", "logs", d.taskID, d.attemptID, step.Name)
					d.indexSubworkflowLogs(bgCtx, d.hostExec.HostRunID, subDir)
				}
			}
		}
		return domain.StepResult{Result: "fail"}, nil
	}

	succeeded = run.State == domain.RunStateSucceeded
	resultLabel := "failed"
	if succeeded {
		resultLabel = "succeeded"
	}

	// For container sub-workflows, extract the workspace to a git branch
	// and copy logs to the host log directory.
	if targetWF.Location == domain.LocationContainer {
		poolKey := d.attemptID + ":" + targetWF.ContainerID()

		// Get the session so we can access the container for extraction.
		session, sessErr := d.pool.SessionFor(ctx, poolKey, ports.ContainerConfig{})
		if sessErr != nil {
			log.Printf("daemon executor: could not get session for extraction: %v", sessErr)
		}

		if baseSHA != "" && session != nil {
			log.Printf("daemon executor: extracting results to branch cloche/%s (baseSHA=%s)", childRunID, baseSHA)
			if _, err := docker.ExtractResults(ctx, docker.ExtractOptions{
				ContainerID:  session.ContainerID,
				ProjectDir:   d.projectDir,
				RunID:        childRunID,
				BaseSHA:      baseSHA,
				WorkflowName: targetName,
				Result:       resultLabel,
			}); err != nil {
				log.Printf("daemon executor: failed to extract results: %v", err)
			} else {
				log.Printf("daemon executor: branch cloche/%s created successfully", childRunID)
			}
		}

		// Extract container output logs to the host log directory so that
		// the host status handler can read them (it looks for <step>.log)
		// and they survive container cleanup.
		if session != nil {
			d.extractContainerLogs(ctx, session.ContainerID, step.Name)
			if d.logStore != nil && d.hostExec != nil && d.hostExec.HostRunID != "" {
				subDir := filepath.Join(d.projectDir, ".cloche", "logs", d.taskID, d.attemptID, step.Name)
				d.indexSubworkflowLogs(ctx, d.hostExec.HostRunID, subDir)
			}
		}

		// Set child_run_id so downstream host steps (merge-to-base.sh) can
		// find the branch.
		if d.store != nil && d.taskID != "" {
			var kvRunID string
			if d.hostExec != nil {
				kvRunID = d.hostExec.HostRunID
			}
			_ = d.store.SetContextKey(ctx, d.taskID, d.attemptID, kvRunID, "child_run_id", childRunID)
		}

		// Note: CleanupAttempt is called by the deferred function above.
	}

	if succeeded {
		return domain.StepResult{Result: "success"}, nil
	}
	return domain.StepResult{Result: "fail"}, nil
}


// executeContainerStep obtains a container session for the attempt (starting a
// new container if needed) and dispatches the step to the in-container agent.
func (d *DaemonExecutor) executeContainerStep(ctx context.Context, step *domain.Step, wf *domain.Workflow) (domain.StepResult, error) {
	if d.pool == nil {
		return domain.StepResult{}, fmt.Errorf("daemon executor: no container pool configured")
	}
	if d.attemptID == "" {
		return domain.StepResult{}, fmt.Errorf("daemon executor: attemptID not set for container step %q", step.Name)
	}

	// Use the workflow's container ID as part of the pool key so that workflows
	// sharing the same container ID reuse the same session within an attempt.
	poolKey := d.attemptID + ":" + wf.ContainerID()

	var hostRunID string
	if d.hostExec != nil {
		hostRunID = d.hostExec.HostRunID
	}

	cfg := ports.ContainerConfig{
		Image:        d.image,
		WorkflowName: wf.Name,
		ProjectDir:   d.projectDir,
		RunID:        hostRunID,
		TaskID:       d.taskID,
		AttemptID:    d.attemptID,
		NetworkAllow: []string{"*"},
		// Start agent in session mode (no workflow file argument) so it
		// connects to the daemon via gRPC and waits for ExecuteStep commands.
		Cmd: []string{"cloche-agent"},
	}

	session, err := d.pool.SessionFor(ctx, poolKey, cfg)
	if err != nil {
		return domain.StepResult{}, fmt.Errorf("daemon executor: getting container session for step %q: %w", step.Name, err)
	}

	if d.onContainerStart != nil {
		d.onContainerStart(session.ContainerID)
	}

	return session.ExecuteStep(ctx, step, d.resumeMode)
}

// extractContainerLogs copies output log files from the container to the host
// log directory. The container's full.log is written as <stepName>.log so the
// host status handler (which reads <outputDir>/<step>.log on step completion)
// can pick it up and append it to the host workflow's full.log. Individual
// container step logs are placed in a <stepName>/ subdirectory.
func (d *DaemonExecutor) extractContainerLogs(ctx context.Context, containerID, stepName string) {
	if d.pool == nil || d.taskID == "" || d.attemptID == "" {
		return
	}

	hostLogDir := filepath.Join(d.projectDir, ".cloche", "logs", d.taskID, d.attemptID)

	// Extract container output to a step-specific subdirectory so individual
	// container step logs (implement.log, test.log, etc.) are preserved
	// without colliding with the host workflow's own log files.
	subDir := filepath.Join(hostLogDir, stepName)
	if err := os.MkdirAll(subDir, 0755); err != nil {
		log.Printf("daemon executor: failed to create log subdir %s: %v", subDir, err)
		return
	}

	if err := d.pool.CopyFrom(ctx, containerID, "/workspace/.cloche/output/.", subDir); err != nil {
		log.Printf("daemon executor: failed to extract container logs: %v", err)
		return
	}

	// Copy the container's full.log as <stepName>.log in the host log dir.
	// The host status handler reads this file on step completion and appends
	// its content to the host workflow's full.log.
	containerFullLog := filepath.Join(subDir, "full.log")
	data, err := os.ReadFile(containerFullLog)
	if err != nil {
		log.Printf("daemon executor: no full.log in container output: %v", err)
		return
	}
	stepLog := filepath.Join(hostLogDir, stepName+".log")
	if err := os.WriteFile(stepLog, data, 0644); err != nil {
		log.Printf("daemon executor: failed to write %s: %v", stepLog, err)
	}
}

// indexSubworkflowLogs scans the sub-workflow log directory (e.g. develop/) and
// registers each .log file in the log_files table under hostRunID so the web UI
// can serve individual container step logs (implement, compile, test, etc.).
func (d *DaemonExecutor) indexSubworkflowLogs(ctx context.Context, hostRunID, subDir string) {
	entries, err := os.ReadDir(subDir)
	if err != nil {
		log.Printf("daemon executor: failed to read subdir %s for log indexing: %v", subDir, err)
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
			RunID:     hostRunID,
			StepName:  stepName,
			FileType:  fileType,
			FilePath:  filepath.Join(subDir, name),
			FileSize:  fileSize,
			CreatedAt: now,
		}
		if err := d.logStore.SaveLogFile(ctx, logEntry); err != nil {
			log.Printf("daemon executor: failed to index log file %s for run %s: %v", name, hostRunID, err)
		}
	}
}
