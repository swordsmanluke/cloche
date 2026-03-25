package grpc

import (
	"context"

	pb "github.com/cloche-dev/cloche/api/clochepb"
	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/host"
	rpcgrpc "google.golang.org/grpc"
)

// SplitIntoChunks exposes splitIntoChunks for testing.
func SplitIntoChunks(content string, maxSize int) []string {
	return splitIntoChunks(content, maxSize)
}

// SendContentChunked exposes sendContentChunked for testing.
func SendContentChunked(stream rpcgrpc.ServerStreamingServer[pb.LogEntry], entryType, stepName, result, timestamp, content string) error {
	return sendContentChunked(stream, entryType, stepName, result, timestamp, content)
}

// MaxLogChunkSize exposes maxLogChunkSize for testing.
const MaxLogChunkSize = maxLogChunkSize

// AddActiveRun registers a fake active container run for testing.
func (s *ClocheServer) AddActiveRun(runID, containerID string) {
	s.mu.Lock()
	s.runIDs[runID] = containerID
	s.mu.Unlock()
}

// RegisterContainerRun registers a container-to-run mapping for testing.
// This simulates what launchAndTrack does after starting a container.
func (s *ClocheServer) RegisterContainerRun(containerID, runID string) {
	s.mu.Lock()
	s.containerRun[containerID] = runID
	s.mu.Unlock()
}

// AddActiveHostRun registers a fake active host run cancel function for testing.
func (s *ClocheServer) AddActiveHostRun(runID string, cancelFn context.CancelFunc) {
	s.mu.Lock()
	s.hostCancels[runID] = cancelFn
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

// RegisterLoop registers a host.Loop for the given project directory so that
// haltProjectLoop can find it during tests.
func (s *ClocheServer) RegisterLoop(projectDir string, loop *host.Loop) {
	s.mu.Lock()
	s.loops[projectDir] = loop
	s.mu.Unlock()
}

// ScanAndResolveStuckWorkflows exposes scanAndResolveStuckWorkflows for testing.
func (s *ClocheServer) ScanAndResolveStuckWorkflows(ctx context.Context) {
	s.scanAndResolveStuckWorkflows(ctx)
}

// TrackRun exposes trackRun for testing.
func (s *ClocheServer) TrackRun(runID, containerID, projectDir, workflowName string, keepContainer bool) {
	s.trackRun(runID, containerID, projectDir, workflowName, keepContainer)
}
