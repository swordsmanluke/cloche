package domain

// HealthStatus represents the overall health of a project based on recent runs.
type HealthStatus string

const (
	HealthGreen  HealthStatus = "green"
	HealthYellow HealthStatus = "yellow"
	HealthRed    HealthStatus = "red"
	HealthGrey   HealthStatus = "grey"
	HealthBlue   HealthStatus = "blue"
)

// HealthResult holds the computed health status along with pass/fail counts.
type HealthResult struct {
	Status HealthStatus
	Passed int
	Failed int
	Total  int
}

// CalculateHealth determines project health from the most recent runs.
// It considers the last windowSize runs (or fewer if not enough exist).
//
// Status rules:
//   - Grey: no runs provided
//   - Blue: all runs in the window are still in-progress (pending/running)
//   - Green: all completed runs passed (succeeded)
//   - Red: all completed runs failed (failed/cancelled)
//   - Yellow: mix of passed and failed completed runs
func CalculateHealth(runs []Run, windowSize int) HealthResult {
	if len(runs) == 0 {
		return HealthResult{Status: HealthGrey}
	}

	n := windowSize
	if n > len(runs) {
		n = len(runs)
	}

	// Take the most recent n runs (assumes runs are ordered most-recent first).
	window := runs[:n]

	var passed, failed int
	for _, r := range window {
		switch r.State {
		case RunStateSucceeded:
			passed++
		case RunStateFailed, RunStateCancelled:
			failed++
		// pending, running — neither passed nor failed
		}
	}

	total := len(window)
	completed := passed + failed

	var status HealthStatus
	switch {
	case completed == 0:
		status = HealthBlue
	case failed == 0:
		status = HealthGreen
	case passed == 0:
		status = HealthRed
	default:
		status = HealthYellow
	}

	return HealthResult{
		Status: status,
		Passed: passed,
		Failed: failed,
		Total:  total,
	}
}
