package grpc

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/swordsmanluke/cloche/internal/domain"
	"github.com/swordsmanluke/cloche/internal/dra"
	"github.com/swordsmanluke/cloche/internal/host"
	"github.com/swordsmanluke/cloche/internal/protocol"
)

// recordedPollRunner runs each poll invocation as a recorded host run,
// "dra-wait-<name>", built in Go from dra.WaitWorkflow (see internal/dra/
// waitrun.go), the way wake-ups are built from dra.WakeSpec.
type recordedPollRunner struct {
	s   *ClocheServer
	seq atomic.Int64
}

// pollStepExecutor is the host step executor of a dra-wait run. It runs the
// resolved poll directly, never through a shell, and keeps what it saw: the
// run's own record only carries the step's result (ready, pending or failed).
type pollStepExecutor struct {
	spec    dra.PollSpec
	outcome dra.PollOutcome
}

func (e *pollStepExecutor) Execute(ctx context.Context, step *domain.Step) (domain.StepResult, error) {
	out, err := e.spec.Command(ctx).CombinedOutput()
	marker, clean, found := protocol.ExtractResult(out)
	e.outcome = dra.PollOutcome{Output: string(clean)}
	switch {
	case found:
		e.outcome.Result = marker
		return domain.StepResult{Result: dra.PollStepReady}, nil
	case ctx.Err() != nil:
		// Stopped from outside (daemon shutdown, run stop): not an answer.
		return domain.StepResult{Result: dra.PollStepPending}, nil
	case err != nil:
		e.outcome.Failed = true
		if _, ok := err.(*exec.ExitError); !ok {
			e.outcome.Output = fmt.Sprintf("could not run the poll: %v", err)
		}
		return domain.StepResult{Result: dra.PollStepFailed}, nil
	}
	return domain.StepResult{Result: dra.PollStepPending}, nil
}

// Poll runs one invocation as a recorded run and returns what the poll said.
// A run that could not be recorded or started counts as "not yet": the wait
// stays pending and is polled again at its next interval.
func (r *recordedPollRunner) Poll(ctx context.Context, spec dra.PollSpec) dra.PollOutcome {
	exec := &pollStepExecutor{spec: spec}
	runID := domain.GenerateRunID(dra.WaitWorkflowName(spec.DRA),
		strconv.FormatInt(time.Now().Unix(), 36)+"-"+strconv.FormatInt(r.seq.Add(1), 36))
	runner := &host.Runner{
		Store:        r.s.store,
		Captures:     r.s.captures,
		LogBroadcast: r.s.logBroadcast,
		ActivityLog:  r.s.activityLoggerFor(spec.Dir),
		Executor:     exec,
	}
	if _, err := runner.RunWorkflowDef(ctx, spec.Dir, dra.WaitWorkflow(spec.DRA), runID); err != nil {
		log.Printf("dra wait poll %s: %v", runID, err)
	}
	return exec.outcome
}
