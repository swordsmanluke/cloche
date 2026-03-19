package grpc

import "context"

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
