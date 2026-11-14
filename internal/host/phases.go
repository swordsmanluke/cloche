package host

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"

	"github.com/swordsmanluke/cloche/internal/builtin"
	"github.com/swordsmanluke/cloche/internal/domain"
	"github.com/swordsmanluke/cloche/internal/module"
)

// Slot names of the task-management contract (docs/plans/2026-10-13-modules-design.md
// §Standard contract). The daemon resolves each through the project root scope.
const (
	SlotListTasks   = "list-tasks"
	SlotClaimTask   = "claim-task"
	SlotCloseTask   = "close-task"
	SlotReleaseTask = "release-task"
	SlotCreateTask  = "create-task"
	SlotUpdateTask  = "update-task"
)

// phaseHasEnv reports whether an env var is visible to a phase run: set by
// the resolved bind or in the daemon's environment.
func phaseHasEnv(bindEnv map[string]string) func(string) bool {
	return func(n string) bool {
		if _, ok := bindEnv[n]; ok {
			return true
		}
		_, ok := os.LookupEnv(n)
		return ok
	}
}

// ClaimStatusUnavailable is the claim_status value a claim-task workflow
// writes, alongside ending in fail, when another worker won the task.
const ClaimStatusUnavailable = "unavailable"

var errNotHostWorkflow = errors.New("slot resolves to a non-host workflow")

// ResolvePhase resolves a daemon phase slot through the project root bind
// scope (project.cloche), then project workflows, then built-ins. It wraps
// module.ErrSlotNotFound when nothing provides the slot, and
// errNotHostWorkflow when the match is not a host workflow.
func ResolvePhase(projectDir, slot string) (*domain.Workflow, error) {
	res, err := resolvePhase(projectDir, slot)
	if err != nil {
		return nil, err
	}
	return res.Workflow, nil
}

// resolvePhase is ResolvePhase keeping the winning bind's env settings, which
// the phase's run must see.
func resolvePhase(projectDir, slot string) (*module.Resolved, error) {
	reg, err := module.Load(projectDir)
	if err != nil {
		log.Printf("host: loading modules for %s: %v; phases resolve to project workflows only", projectDir, err)
		reg = nil
	}
	all, _ := FindAllWorkflows(projectDir)
	rs := &module.Resolver{Registry: reg, Project: all, Builtin: builtin.Lookup}
	res, err := rs.ResolveSlot(nil, slot)
	if err != nil {
		return nil, err
	}
	if res.Workflow.Location != domain.LocationHost {
		return nil, fmt.Errorf("%w: %q (%s)", errNotHostWorkflow, slot, res.Source)
	}
	return res, nil
}

// PhaseResolves reports whether slot resolves to a host workflow.
func PhaseResolves(projectDir, slot string) bool {
	_, err := ResolvePhase(projectDir, slot)
	return err == nil
}

// ClaimResult is the outcome of the claim-task phase.
type ClaimResult struct {
	// Unavailable is set when the claim was lost (fail with
	// claim_status = unavailable): skip the task, don't treat it as an error.
	Unavailable bool
	Title       string // task_title output, if any
	BodyPath    string // task_body_path output, if any
}

// claim outputs copied from the claim run's KV scope into the attempt scope
// so the following main run can read them.
var claimOutputKeys = []string{"task_title", "task_body_path", "claim_status"}

// RunClaimTask runs the claim-task phase for taskID within attemptID, using
// runner (whose Store must be set) to execute the resolved workflow. It
// returns (nil, nil) when no claim-task resolves, in which case main claims
// the task itself (migration fallback). A claim lost to another worker is
// reported as ClaimResult.Unavailable, not as an error.
func RunClaimTask(ctx context.Context, runner *Runner, projectDir, taskID, attemptID string) (*ClaimResult, error) {
	res, err := resolvePhase(projectDir, SlotClaimTask)
	if err != nil {
		if errors.Is(err, module.ErrSlotNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("resolving claim-task: %w", err)
	}
	return runClaimWorkflow(ctx, runner, projectDir, res.Workflow, res.Env, taskID, attemptID)
}

func runClaimWorkflow(ctx context.Context, runner *Runner, projectDir string, wf *domain.Workflow, bindEnv map[string]string, taskID, attemptID string) (*ClaimResult, error) {
	store := runner.Store
	if store == nil {
		return nil, errors.New("claim-task: no run store")
	}
	if err := store.SetContextKey(ctx, taskID, attemptID, "", "task_id", taskID); err != nil {
		return nil, fmt.Errorf("claim-task: seeding task_id: %w", err)
	}
	hasKV := func(k string) bool {
		_, ok, _ := store.GetContextKey(ctx, taskID, attemptID, "", k)
		return ok
	}
	hasEnv := phaseHasEnv(bindEnv)
	if err := module.CheckInputs(SlotClaimTask, wf.Contract, hasKV, hasEnv); err != nil {
		return nil, err
	}

	ctx = module.WithEnv(ctx, bindEnv)
	r := *runner
	r.TaskID, r.AttemptID, r.KeepContext = taskID, attemptID, true
	result, err := r.RunWorkflowDef(ctx, projectDir, wf, domain.GenerateRunID(SlotClaimTask, attemptID))
	if err != nil {
		return nil, fmt.Errorf("claim-task: %w", err)
	}

	get := func(k string) (string, bool) {
		v, ok, _ := store.GetContextKey(ctx, taskID, attemptID, result.RunID, k)
		return v, ok
	}
	if result.State != domain.RunStateSucceeded {
		if v, _ := get("claim_status"); v == ClaimStatusUnavailable {
			return &ClaimResult{Unavailable: true}, nil
		}
		return nil, fmt.Errorf("claim-task workflow finished with state %s", result.State)
	}

	if err := module.CheckOutputs(SlotClaimTask, wf.Contract, func(k string) bool { _, ok := get(k); return ok }); err != nil {
		return nil, err
	}
	// Promote outputs to the attempt scope for main.
	for _, k := range claimOutputKeys {
		if v, ok := get(k); ok {
			if err := store.SetContextKey(ctx, taskID, attemptID, "", k, v); err != nil {
				return nil, fmt.Errorf("claim-task: saving %s: %w", k, err)
			}
		}
	}
	res := &ClaimResult{}
	res.Title, _ = get("task_title")
	res.BodyPath, _ = get("task_body_path")
	return res, nil
}

// RunTrackerPhase runs an on-demand tracker phase (create-task or
// update-task) resolved through the project binds. inputs are seeded as KV
// keys in a throwaway namespace so the phase's scripts read them with
// `cloche get`; the contract's inputs are checked first, and its outputs are
// returned after a successful run. Empty input values are not seeded.
func RunTrackerPhase(ctx context.Context, runner *Runner, projectDir, slot string, inputs map[string]string) (map[string]string, error) {
	store := runner.Store
	if store == nil {
		return nil, fmt.Errorf("%s: no run store", slot)
	}
	res, err := resolvePhase(projectDir, slot)
	if err != nil {
		return nil, fmt.Errorf("resolving %s: %w", slot, err)
	}
	wf := res.Workflow

	kvTask, kvAttempt := slot, domain.GenerateAttemptID()
	defer func() { _ = store.DeleteContextKeys(context.Background(), kvTask, kvAttempt) }()
	for k, v := range inputs {
		if v == "" {
			continue
		}
		if err := store.SetContextKey(ctx, kvTask, kvAttempt, "", k, v); err != nil {
			return nil, fmt.Errorf("%s: seeding %s: %w", slot, k, err)
		}
	}
	hasKV := func(k string) bool {
		_, ok, _ := store.GetContextKey(ctx, kvTask, kvAttempt, "", k)
		return ok
	}
	hasEnv := phaseHasEnv(res.Env)
	if err := module.CheckInputs(slot, wf.Contract, hasKV, hasEnv); err != nil {
		return nil, err
	}

	ctx = module.WithEnv(ctx, res.Env)
	r := *runner
	r.SkipRunRecord = true
	r.KVTaskID, r.KVAttemptID, r.KeepContext = kvTask, kvAttempt, true
	result, err := r.RunWorkflowDef(ctx, projectDir, wf, domain.GenerateRunID(slot, kvAttempt))
	if result != nil && result.tmpOutputDir != "" {
		defer os.RemoveAll(result.tmpOutputDir)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", slot, err)
	}
	if result.State != domain.RunStateSucceeded {
		return nil, fmt.Errorf("%s workflow finished with state %s", slot, result.State)
	}
	get := func(k string) (string, bool) {
		v, ok, _ := store.GetContextKey(ctx, kvTask, kvAttempt, result.RunID, k)
		return v, ok
	}
	if err := module.CheckOutputs(slot, wf.Contract, func(k string) bool { _, ok := get(k); return ok }); err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, k := range []string{"new_task_id", "found_task_id", "task_found", "task_labels", "task_status"} {
		if v, ok := get(k); ok {
			out[k] = v
		}
	}
	return out, nil
}
