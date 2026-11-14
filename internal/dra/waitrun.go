package dra

import "github.com/swordsmanluke/cloche/internal/domain"

// A poll invocation is an ordinary recorded host run, "dra-wait-<name>", built
// in Go like a wake-up: one step, "poll", that runs the wait's poll.
//
//	poll --ready---> done     (the poll printed a CLOCHE_RESULT marker)
//	poll --pending-> done     (exit 0, no marker: not yet)
//	poll --failed--> abort    (non-zero exit, no marker, or it would not start)
//
// The step's result is always one of those three, whatever name the poll's
// marker carries; the marker's own name travels in the outcome the executor
// records, not in the wiring.

// WaitWorkflowPrefix prefixes the host workflow (and run) name of a poll:
// "dra-wait-<name>".
const WaitWorkflowPrefix = "dra-wait-"

// Results of the poll step.
const (
	PollStepReady   = "ready"
	PollStepPending = "pending"
	PollStepFailed  = "failed"
	PollStepName    = "poll"
)

// WaitWorkflowName is the host workflow / run name of a DRA's polls.
func WaitWorkflowName(name string) string { return WaitWorkflowPrefix + name }

// WaitWorkflow returns the host workflow of one poll invocation.
func WaitWorkflow(name string) *domain.Workflow {
	return &domain.Workflow{
		Name:     WaitWorkflowName(name),
		Location: domain.LocationHost,
		Builtin:  true,
		Config:   map[string]string{"_location_block": "host"},
		Steps: map[string]*domain.Step{
			PollStepName: {
				Name:    PollStepName,
				Type:    domain.StepTypeScript,
				Config:  map[string]string{"run": "(poll of a DRA wait, run by the daemon without a shell)", "timeout": "10m"},
				Results: []string{PollStepReady, PollStepPending, PollStepFailed},
			},
		},
		Wiring: []domain.Wire{
			{From: PollStepName, Result: PollStepReady, To: domain.StepDone},
			{From: PollStepName, Result: PollStepPending, To: domain.StepDone},
			{From: PollStepName, Result: PollStepFailed, To: domain.StepAbort},
		},
		EntryStep: PollStepName,
	}
}
