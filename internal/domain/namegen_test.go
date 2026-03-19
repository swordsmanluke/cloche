package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGenerateRunID_WorkflowNameOnly(t *testing.T) {
	id := GenerateRunID("develop", "")
	assert.Equal(t, "develop", id, "run ID without step should be just the workflow name")
}

func TestGenerateRunID_WithStep(t *testing.T) {
	id := GenerateRunID("develop", "implement")
	assert.Equal(t, "develop-implement", id, "run ID with step should be workflow-step")
}

func TestGenerateRunID_Deterministic(t *testing.T) {
	// Same workflow name always produces same run ID (no random prefix).
	a := GenerateRunID("main", "")
	b := GenerateRunID("main", "")
	assert.Equal(t, a, b, "GenerateRunID should be deterministic")
}

func TestFormatRunID_NoConversion(t *testing.T) {
	assert.Equal(t, "develop", FormatRunID("develop"))
	assert.Equal(t, "develop-implement", FormatRunID("develop-implement"))
}

func TestParseRunID_ReturnsWorkflowName(t *testing.T) {
	attempt, workflow, step := ParseRunID("develop")
	assert.Equal(t, "", attempt, "attempt should be empty — not encoded in run ID")
	assert.Equal(t, "develop", workflow)
	assert.Equal(t, "", step)
}
