package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGenerateAttemptID_LengthAndAlphabet(t *testing.T) {
	id := GenerateAttemptID()
	assert.Len(t, id, attemptIDLength)
	for _, c := range id {
		assert.Contains(t, attemptIDAlphabet, string(c))
	}
}

func TestGenerateAttemptID_NoCollisionsAtHistoricalVolume(t *testing.T) {
	// Real databases hold thousands of attempts; 4-character IDs collided about
	// a dozen times at this volume and silently took over each other's rows.
	const n = 7000
	seen := make(map[string]bool, n)
	collisions := 0
	for i := 0; i < n; i++ {
		id := GenerateAttemptID()
		if seen[id] {
			collisions++
		}
		seen[id] = true
	}
	// The expected count is ~0.01, so more than one means the ID space shrank.
	assert.LessOrEqual(t, collisions, 1)
}
