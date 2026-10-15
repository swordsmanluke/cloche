package host

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPollCoordinator_SlotsAreScopedPerProject verifies that one project's
// loop cannot drain or grant another project's slot releases/resumes. A shared
// pool made loops steal each other's handoffs, leaving phantom in-flight
// slots that never completed.
func TestPollCoordinator_SlotsAreScopedPerProject(t *testing.T) {
	coord := NewPollCoordinator()

	coord.ReleaseSlot("/a")
	assert.Equal(t, 0, coord.drainReleasedSlots("/b"))
	assert.Equal(t, 1, coord.drainReleasedSlots("/a"))

	done := make(chan struct{})
	go func() { coord.ReacquireSlot("/a", "run-a", "poll"); close(done) }()
	require.Eventually(t, func() bool { return coord.PendingResumeCount("/a") == 1 }, time.Second, 5*time.Millisecond)

	assert.False(t, coord.grantNextResume("/b"), "another project's loop must not grant this resume")
	assert.True(t, coord.grantNextResume("/a"))
	<-done
}
