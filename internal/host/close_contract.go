package host

import (
	"errors"

	"github.com/cloche-dev/cloche/internal/domain"
)

// ErrNoCloseContract is returned by callers dispatching the close/cancel task
// contract (see ResolveCloseTaskWorkflow) when a project defines neither a
// "close-task" nor a "cancel-task" standalone host workflow. Consumers use
// errors.Is to distinguish "this action isn't available here" from a
// workflow failure.
var ErrNoCloseContract = errors.New("project defines no close/cancel task contract")

// CloseTaskWorkflowNames lists the standalone host workflow names checked, in
// order, for a project's "close in tracker" contract — the counterpart to
// release-task, but for marking a task done/cancelled from outside the main
// pipeline (e.g. giving up on a repeat-failure task from the dashboard). A
// project opts in by defining one of these as a top-level host workflow; if
// neither is defined, the close action stays unavailable.
var CloseTaskWorkflowNames = []string{"close-task", "cancel-task"}

// ResolveCloseTaskWorkflow returns the name of projectDir's close/cancel task
// contract workflow and true, if one of CloseTaskWorkflowNames is defined as
// a top-level host workflow. Returns "", false if none is defined (including
// when the .cloche files fail to parse) — callers should treat this as "the
// action isn't available here", not an error.
func ResolveCloseTaskWorkflow(projectDir string) (string, bool) {
	all, err := FindAllWorkflows(projectDir)
	if err != nil {
		return "", false
	}
	for _, name := range CloseTaskWorkflowNames {
		if wf, ok := all[name]; ok && wf.Location == domain.LocationHost {
			return name, true
		}
	}
	return "", false
}
