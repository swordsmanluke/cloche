package intent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAllocateUniqueID_RetriesOnCollision(t *testing.T) {
	generated := []string{"req-0001", "req-0002", "req-0003"}
	calls := 0
	generate := func() string {
		id := generated[calls]
		calls++
		return id
	}

	seen := map[string]bool{"req-0001": true} // first candidate already taken
	exists := func(id string) bool { return seen[id] }

	id, err := allocateUniqueID(generate, exists, 5)
	require.NoError(t, err)
	assert.Equal(t, "req-0002", id)
	assert.Equal(t, 2, calls, "should stop retrying once a free id is found")
}

func TestAllocateUniqueID_ExhaustsAttempts(t *testing.T) {
	generate := func() string { return "req-dead" }
	exists := func(string) bool { return true } // every candidate collides

	id, err := allocateUniqueID(generate, exists, 3)
	require.Error(t, err)
	assert.Empty(t, id)
	assert.Contains(t, err.Error(), "3 attempts")
}
