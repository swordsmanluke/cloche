package attention

import (
	"sort"

	"github.com/swordsmanluke/cloche/internal/domain"
)

// FailureStreak describes a task's run of consecutive failed attempts.
type FailureStreak struct {
	// Count is the number of consecutive failed attempts, newest first.
	Count int
	// LatestRunID is the top-level run of the newest failed attempt.
	LatestRunID string
	// FailedStep is the step that failed in the newest attempt: the deepest
	// failing step (a child workflow's step if one failed, else the host
	// run's own step). Empty when no failing step was recorded.
	FailedStep string
	// Error is the newest failed run's error message, if any.
	Error string
}

// TaskFailureStreak counts taskID's consecutive failed attempts in runs, using
// the same attempt grouping as the repeat-failure "Needs you" item.
func TaskFailureStreak(runs []*domain.Run, taskID string) FailureStreak {
	topLevel, parentMap := topLevelRuns(runs)
	var group []*domain.Run
	for _, r := range topLevel {
		if r.TaskID == taskID {
			group = append(group, r)
		}
	}
	sort.SliceStable(group, func(i, j int) bool { return group[i].StartedAt.After(group[j].StartedAt) })

	var fs FailureStreak
	for _, r := range group {
		attemptRuns := append([]*domain.Run{r}, parentMap[r.ID]...)
		if domain.AttemptAggregateStatus(attemptRuns) != domain.RunStateFailed {
			break
		}
		if fs.Count == 0 {
			fs.LatestRunID = r.ID
			fs.FailedStep = failedStep(r, parentMap[r.ID])
			fs.Error = r.ErrorMessage
		}
		fs.Count++
	}
	return fs
}

// failedStep returns the last failing step of the newest failed child run,
// falling back to the host run's own last failing step.
func failedStep(host *domain.Run, children []*domain.Run) string {
	sorted := append([]*domain.Run(nil), children...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].StartedAt.After(sorted[j].StartedAt) })
	for _, c := range sorted {
		if c.State == domain.RunStateFailed {
			if s := lastFailingStep(c); s != "" {
				return s
			}
		}
	}
	return lastFailingStep(host)
}

func lastFailingStep(r *domain.Run) string {
	for i := len(r.StepExecutions) - 1; i >= 0; i-- {
		e := r.StepExecutions[i]
		if e.Skipped {
			continue
		}
		if e.Result == "fail" || e.Result == "failed" || e.Result == "error" {
			return e.StepName
		}
	}
	return ""
}
