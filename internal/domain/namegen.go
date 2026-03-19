package domain

// GenerateRunID returns a unique run ID. When attemptID is provided, the ID
// is "<attemptID>-<workflowName>" to ensure uniqueness across concurrent runs.
// When attemptID is empty (e.g. ephemeral list-tasks runs), falls back to just
// the workflow name.
func GenerateRunID(workflowName, attemptID string) string {
	if attemptID != "" {
		return attemptID + "-" + workflowName
	}
	return workflowName
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
