package grpc

import (
	"context"
	"fmt"
	"log"

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
	ProjectDir string
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
		projectDir:       cfg.ProjectDir,
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
func (d *DaemonExecutor) executeWorkflowStep(ctx context.Context, step *domain.Step) (domain.StepResult, error) {
	targetName := step.Config["workflow_name"]
	if targetName == "" {
		return domain.StepResult{}, fmt.Errorf("workflow step %q missing workflow_name config", step.Name)
	}

	targetWF, ok := d.allWFs[targetName]
	if !ok {
		return domain.StepResult{}, fmt.Errorf("workflow step %q: workflow %q not found in project", step.Name, targetName)
	}

	log.Printf("daemon executor: running sub-workflow %q for step %q", targetName, step.Name)

	eng := engine.New(d)
	run, err := eng.Run(ctx, targetWF)
	if err != nil {
		log.Printf("daemon executor: sub-workflow %q failed: %v", targetName, err)
		return domain.StepResult{Result: "fail"}, nil
	}

	if run.State == domain.RunStateSucceeded {
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
