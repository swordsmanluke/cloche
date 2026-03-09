package host

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/dsl"
	"github.com/cloche-dev/cloche/internal/engine"
	"github.com/cloche-dev/cloche/internal/ports"
)

// Runner executes a host workflow by parsing host.cloche and walking the step graph.
type Runner struct {
	Dispatcher RunDispatcher
	Store      ports.RunStore
}

// RunResult contains the outcome of a host workflow execution.
type RunResult struct {
	RunID string
	State domain.RunState
}

// Run parses .cloche/host.cloche from projectDir and executes the "main" workflow.
func (r *Runner) Run(ctx context.Context, projectDir string) (*RunResult, error) {
	hostPath := filepath.Join(projectDir, ".cloche", "host.cloche")
	data, err := os.ReadFile(hostPath)
	if err != nil {
		return nil, fmt.Errorf("reading host.cloche: %w", err)
	}

	wf, err := dsl.ParseForHost(string(data))
	if err != nil {
		return nil, fmt.Errorf("parsing host.cloche: %w", err)
	}

	if wf.Name != "main" {
		return nil, fmt.Errorf("host.cloche workflow is %q, expected \"main\"", wf.Name)
	}

	// Generate a unique run ID for this host workflow execution
	orchRunID := domain.GenerateRunID("main")

	// Create output directory for step outputs
	outputDir := filepath.Join(projectDir, ".cloche", orchRunID, "output")
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("creating output dir: %w", err)
	}

	log.Printf("host workflow: starting %q for project %s (run %s)", wf.Name, projectDir, orchRunID)

	executor := &Executor{
		ProjectDir: projectDir,
		Dispatcher: r.Dispatcher,
		Store:      r.Store,
		OutputDir:  outputDir,
		Wires:      wf.Wiring,
	}

	eng := engine.New(executor)
	eng.SetStatusHandler(&hostStatusHandler{projectDir: projectDir, orchRunID: orchRunID})

	run, runErr := eng.Run(ctx, wf)

	result := &RunResult{
		RunID: orchRunID,
		State: domain.RunStateFailed,
	}
	if run != nil {
		result.State = run.State
	}

	if runErr != nil {
		log.Printf("host workflow: %q failed for %s: %v", wf.Name, projectDir, runErr)
		return result, runErr
	}

	log.Printf("host workflow: %q completed for %s with state %s", wf.Name, projectDir, run.State)
	return result, nil
}

// hostStatusHandler logs host workflow step events.
type hostStatusHandler struct {
	projectDir string
	orchRunID  string
}

func (h *hostStatusHandler) OnStepStart(_ *domain.Run, step *domain.Step) {
	log.Printf("host workflow [%s]: step %q started", h.orchRunID, step.Name)
}

func (h *hostStatusHandler) OnStepComplete(_ *domain.Run, step *domain.Step, result string) {
	log.Printf("host workflow [%s]: step %q completed with result %q", h.orchRunID, step.Name, result)
}

func (h *hostStatusHandler) OnRunComplete(run *domain.Run) {
	log.Printf("host workflow [%s]: run completed with state %s", h.orchRunID, run.State)
}
