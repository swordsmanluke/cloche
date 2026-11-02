package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateAttemptID_LengthAndAlphabet(t *testing.T) {
	id := GenerateAttemptID()
	assert.Len(t, id, attemptIDLength)
	for _, c := range id {
		assert.Contains(t, attemptIDAlphabet, string(c))
	}
}

func TestAttemptIDFromSequence_UniqueAcrossWholeSpace(t *testing.T) {
	seen := make(map[string]struct{}, AttemptIDSpace)
	for seq := uint64(0); seq < AttemptIDSpace; seq++ {
		id := AttemptIDFromSequence(seq)
		require.Len(t, id, attemptIDLength)
		_, dup := seen[id]
		require.False(t, dup, "sequence %d reissued ID %s", seq, id)
		seen[id] = struct{}{}
	}
}

func TestAttemptIDFromSequence_NeighboursDontLookAlike(t *testing.T) {
	for seq := uint64(1); seq < 1000; seq++ {
		a, b := AttemptIDFromSequence(seq), AttemptIDFromSequence(seq+1)
		assert.NotEqual(t, a[:2], b[:2], "sequences %d and %d: %s, %s", seq, seq+1, a, b)
	}
}

func TestAttemptIDFromSequence_MappingIsPinned(t *testing.T) {
	// Changing the mapping would reissue IDs already handed out.
	got := []string{AttemptIDFromSequence(1), AttemptIDFromSequence(2), AttemptIDFromSequence(3)}
	assert.Equal(t, []string{"wi9n", "ir80", "408d"}, got)
}
