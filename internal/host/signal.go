package host

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	"github.com/swordsmanluke/cloche/internal/domain"
	"github.com/swordsmanluke/cloche/internal/signal"
)

// signalKV renders sig as the KV keys a handle-notification workflow reads
// with `cloche get`: signal_source, signal_key, signal_severity,
// signal_priority, signal_title, signal_evidence, signal_domain, and
// signal_json (the whole object). signal_count starts at 1 and grows as
// identical signals join the running handler.
func signalKV(sig signal.Signal, count int) map[string]string {
	obj, _ := json.Marshal(sig)
	return map[string]string{
		"signal_source":   sig.Source,
		"signal_key":      sig.Key,
		"signal_severity": sig.Severity,
		"signal_priority": signal.Priority(sig.Severity),
		"signal_title":    sig.Title,
		"signal_evidence": sig.Evidence,
		"signal_domain":   sig.Domain,
		"signal_json":     string(obj),
		"signal_count":    strconv.Itoa(count),
	}
}

// RunSignalHandler runs the project's handle-notification host workflow (the
// project's own, else the built-in default) with sig as input. Occurrences
// that join run while it is executing update signal_count in its KV, so a
// step that reads it later sees the burst size.
func RunSignalHandler(ctx context.Context, runner *Runner, projectDir string, sig signal.Signal, run *signal.InFlight) error {
	store := runner.Store
	if store == nil {
		return fmt.Errorf("%s: no run store", signal.WorkflowName)
	}
	wf, err := findHostWorkflow(projectDir, signal.WorkflowName)
	if err != nil {
		return err
	}

	kvTask, kvAttempt := signal.WorkflowName, domain.GenerateAttemptID()
	defer func() { _ = store.DeleteContextKeys(context.Background(), kvTask, kvAttempt) }()
	for k, v := range signalKV(sig, 1) {
		if v == "" {
			continue
		}
		if err := store.SetContextKey(ctx, kvTask, kvAttempt, "", k, v); err != nil {
			return fmt.Errorf("%s: seeding %s: %w", signal.WorkflowName, k, err)
		}
	}
	if run != nil {
		run.OnAdd(func(count int, _ signal.Signal) {
			_ = store.SetContextKey(context.Background(), kvTask, kvAttempt, "", "signal_count", strconv.Itoa(count))
		})
	}

	// The run is recorded: a failed handler is a lost signal, and the record
	// is the only place it shows.
	r := *runner
	r.KVTaskID, r.KVAttemptID = kvTask, kvAttempt
	result, err := r.RunWorkflowDef(ctx, projectDir, wf, domain.GenerateRunID(signal.WorkflowName, kvAttempt))
	if result != nil && result.tmpOutputDir != "" {
		defer os.RemoveAll(result.tmpOutputDir)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", signal.WorkflowName, err)
	}
	if result.State != domain.RunStateSucceeded {
		return fmt.Errorf("%s workflow finished with state %s", signal.WorkflowName, result.State)
	}
	return nil
}
