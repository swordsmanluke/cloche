package grpc

// AddActiveRun registers a fake active run for testing.
func (s *ClocheServer) AddActiveRun(runID, containerID string) {
	s.mu.Lock()
	s.runIDs[runID] = containerID
	s.mu.Unlock()
}
