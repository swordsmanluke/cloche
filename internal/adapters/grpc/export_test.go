package grpc

import (
	"context"

	pb "github.com/cloche-dev/cloche/api/clochepb"
	"github.com/cloche-dev/cloche/internal/domain"
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
