package grpc

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

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

	// taskID is the task ID for KV store operations.
	taskID string

	// resumeMode, when true, sets the resume flag on all ExecuteStep messages
	// so the in-container agent continues an existing LLM conversation.
	resumeMode bool

	// onContainerStart is called after a container is started with (containerID).
	// The server uses this to register the container → run mapping so the
	// AgentSession handler can route StepLog messages to the right run.
	onContainerStart func(containerID string)
}

// DaemonExecutorConfig holds configuration for constructing a DaemonExecutor.
type DaemonExecutorConfig struct {
	HostExec   *host.Executor
	Pool       *docker.ContainerPool
	Store      ports.RunStore
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

	// For container sub-workflows, register a deferred cleanup so the
	// container is always stopped even if eng.Run returns an error
	// (e.g. context cancellation, daemon restart).
	succeeded := false
	if targetWF.Location == domain.LocationContainer && d.pool != nil {
		poolKey := d.attemptID + ":" + targetWF.ContainerID()
		defer func() {
			// Use background context: the original ctx may already be cancelled.
			cleanupCtx := context.Background()
			_ = d.pool.CleanupAttempt(cleanupCtx, poolKey, false, succeeded)
		}()
	}

	eng := engine.New(d)
	run, err := eng.Run(ctx, targetWF)
	if err != nil {
		log.Printf("daemon executor: sub-workflow %q failed: %v", targetName, err)
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
			if err := docker.ExtractResults(ctx, session.ContainerID, d.projectDir, childRunID, baseSHA, targetName, resultLabel); err != nil {
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
		}

		// Set child_run_id so downstream host steps (merge-to-base.sh) can
		// find the branch.
		if d.store != nil && d.taskID != "" {
			_ = d.store.SetContextKey(ctx, d.taskID, d.attemptID, "child_run_id", childRunID)
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

	cfg := ports.ContainerConfig{
		Image:        d.image,
		WorkflowName: wf.Name,
		ProjectDir:   d.projectDir,
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
