package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGenerateRunID_NoAttempt(t *testing.T) {
	id := GenerateRunID("develop", "")
	assert.Equal(t, "develop", id, "run ID without attempt should be just the workflow name")
}

func TestGenerateRunID_WithAttempt(t *testing.T) {
	id := GenerateRunID("develop", "a1b2")
	assert.Equal(t, "a1b2-develop", id, "run ID with attempt should be attemptID-workflow")
}

func TestGenerateRunID_Deterministic(t *testing.T) {
	a := GenerateRunID("main", "x1y2")
	b := GenerateRunID("main", "x1y2")
	assert.Equal(t, a, b, "GenerateRunID should be deterministic")
}

func TestFormatRunID_NoConversion(t *testing.T) {
	assert.Equal(t, "develop", FormatRunID("develop"))
	assert.Equal(t, "a1b2-develop", FormatRunID("a1b2-develop"))
}

func TestParseRunID_ReturnsWorkflowName(t *testing.T) {
	attempt, workflow, step := ParseRunID("develop")
	assert.Equal(t, "", attempt, "attempt should be empty — not encoded in run ID")
	assert.Equal(t, "develop", workflow)
	assert.Equal(t, "", step)
}
