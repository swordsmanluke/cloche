package domain

// GenerateRunID returns a run ID for the given workflow. The ID is just the
// workflow name — run IDs are scoped to an attempt, so the full identity of a
// run is (task, attempt, workflow). If stepName is non-empty, it is appended
// with a dash to produce a step-scoped sub-ID.
func GenerateRunID(workflowName, stepName string) string {
	if stepName == "" {
		return workflowName
	}
	return workflowName + "-" + stepName
}

// FormatRunID returns the run ID unchanged. Run IDs are now just workflow
// names; no prefix conversion is needed.
func FormatRunID(id string) string {
	return id
}

// ParseRunID returns the workflow name from a run ID. Since run IDs are now
// just workflow names (with no embedded attempt prefix), this always returns
// ("", id, "") where the workflow name equals the run ID.
//
// Deprecated: run IDs no longer encode attempt information. Use the AttemptID
// field on the Run struct directly.
func ParseRunID(id string) (attemptID, workflowName, stepName string) {
	return "", id, ""
}
