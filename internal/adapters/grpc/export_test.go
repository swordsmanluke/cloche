package grpc

import (
	"context"

	"github.com/cloche-dev/cloche/internal/domain"
)

// AddActiveRun registers a fake active run for testing.
func (s *ClocheServer) AddActiveRun(runID, containerID string) {
	s.mu.Lock()
	s.runIDs[runID] = containerID
	s.mu.Unlock()
}

// ResolveRunIDFromID exposes resolveRunIDFromID for testing.
func (s *ClocheServer) ResolveRunIDFromID(ctx context.Context, id string) (string, string, error) {
	return s.resolveRunIDFromID(ctx, id)
}

// ResolveResumeTarget exposes resolveResumeTarget for testing.
func (s *ClocheServer) ResolveResumeTarget(ctx context.Context, id string) (string, error) {
	return s.resolveResumeTarget(ctx, id)
}

// PickFailedRun exposes pickFailedRun for testing.
func PickFailedRun(runs []*domain.Run) (string, error) {
	return pickFailedRun(runs)
}
