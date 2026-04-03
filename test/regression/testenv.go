//go:build regression

// Package regression provides end-to-end tests that exercise the full
// daemon stack (gRPC server, HTTP handler, SQLite store, broadcaster)
// over real network connections. Tests bind to random ports so they
// never conflict with a running daemon.
//
// Run with:  go test -tags regression ./test/regression/ -v -count=1
package regression

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pb "github.com/cloche-dev/cloche/api/clochepb"
	adaptgrpc "github.com/cloche-dev/cloche/internal/adapters/grpc"
	"github.com/cloche-dev/cloche/internal/adapters/sqlite"
	"github.com/cloche-dev/cloche/internal/adapters/web"
	"github.com/cloche-dev/cloche/internal/logstream"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

// TestEnv is a fully wired daemon-equivalent for regression tests.
// All components are real (SQLite, gRPC, HTTP) but bound to random ports.
type TestEnv struct {
	GRPCClient  pb.ClocheServiceClient
	HTTPURL     string // base URL for the HTTP dashboard (e.g. "http://127.0.0.1:54321")
	Store       *sqlite.Store
	Broadcaster *logstream.Broadcaster
	ProjectDir  string // temp dir with .cloche/ workflow files

	grpcServer *grpc.Server
	grpcConn   *grpc.ClientConn
	httpServer *httptest.Server
}

// NewTestEnv creates and starts a fully wired test environment.
// Cleanup is registered via t.Cleanup.
func NewTestEnv(t *testing.T) *TestEnv {
	t.Helper()

	// SQLite store in a temp file (not :memory:) for WAL compat.
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := sqlite.NewStore(dbPath)
	require.NoError(t, err)

	// Project directory with a .cloche/ dir for workflow files.
	projectDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(projectDir, ".cloche"), 0755))

	broadcaster := logstream.NewBroadcaster()

	// Wire ClocheServer exactly like cmd/cloched/main.go does.
	srv := adaptgrpc.NewClocheServerWithCaptures(store, store, nil, "test-image:latest")
	srv.SetLogStore(store)
	srv.SetTaskStore(store)
	srv.SetActivityStore(store)
	srv.SetLogBroadcaster(broadcaster)

	// gRPC server on random port.
	grpcServer := grpc.NewServer()
	pb.RegisterClocheServiceServer(grpcServer, srv)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	go grpcServer.Serve(lis)

	// gRPC client connected to the random port.
	conn, err := grpc.NewClient(
		lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)

	client := pb.NewClocheServiceClient(conn)

	// HTTP handler with the same stores and broadcaster.
	webHandler, err := web.NewHandler(store, store,
		web.WithLogStore(store),
		web.WithLogBroadcaster(broadcaster),
		web.WithTaskProvider(srv),
	)
	require.NoError(t, err)

	httpServer := httptest.NewServer(webHandler)

	env := &TestEnv{
		GRPCClient:  client,
		HTTPURL:     httpServer.URL,
		Store:       store,
		Broadcaster: broadcaster,
		ProjectDir:  projectDir,
		grpcServer:  grpcServer,
		grpcConn:    conn,
		httpServer:  httpServer,
	}

	t.Cleanup(func() {
		grpcServer.GracefulStop()
		conn.Close()
		httpServer.Close()
		store.Close()
	})

	return env
}

// WriteHostWorkflow writes a host workflow .cloche file to the project dir.
func (e *TestEnv) WriteHostWorkflow(t *testing.T, filename, content string) {
	t.Helper()
	path := filepath.Join(e.ProjectDir, ".cloche", filename)
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))
}

// RunWorkflow triggers a workflow run and returns the run ID.
func (e *TestEnv) RunWorkflow(t *testing.T, workflowName string) string {
	t.Helper()
	resp, err := e.GRPCClient.RunWorkflow(context.Background(), &pb.RunWorkflowRequest{
		ProjectDir:   e.ProjectDir,
		WorkflowName: workflowName,
	})
	require.NoError(t, err)
	return resp.RunId
}

// WaitForState polls GetStatus until the run reaches the expected state or the timeout expires.
func (e *TestEnv) WaitForState(t *testing.T, runID, expectedState string, timeout time.Duration) *pb.GetStatusResponse {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := e.GRPCClient.GetStatus(context.Background(), &pb.GetStatusRequest{RunId: runID})
		if err == nil && resp.State == expectedState {
			return resp
		}
		time.Sleep(100 * time.Millisecond)
	}
	// One final attempt for the error message.
	resp, err := e.GRPCClient.GetStatus(context.Background(), &pb.GetStatusRequest{RunId: runID})
	require.NoError(t, err)
	require.Equal(t, expectedState, resp.State, "run %s did not reach state %q within %v (current: %q)", runID, expectedState, timeout, resp.State)
	return resp
}

// StreamLogsFollow opens a gRPC StreamLogs call with follow mode enabled.
// Returns a channel that receives LogEntry messages until the stream ends.
func (e *TestEnv) StreamLogsFollow(t *testing.T, runID string) <-chan *pb.LogEntry {
	t.Helper()
	ctx := metadata.NewOutgoingContext(context.Background(),
		metadata.Pairs("x-cloche-follow", "true"))

	stream, err := e.GRPCClient.StreamLogs(ctx, &pb.StreamLogsRequest{RunId: runID})
	require.NoError(t, err)

	ch := make(chan *pb.LogEntry, 256)
	go func() {
		defer close(ch)
		for {
			entry, err := stream.Recv()
			if err != nil {
				return
			}
			ch <- entry
		}
	}()
	return ch
}

// StreamLogs calls gRPC StreamLogs (non-follow) and collects all entries.
func (e *TestEnv) StreamLogs(t *testing.T, runID string) []*pb.LogEntry {
	t.Helper()
	stream, err := e.GRPCClient.StreamLogs(context.Background(), &pb.StreamLogsRequest{RunId: runID})
	require.NoError(t, err)

	var entries []*pb.LogEntry
	for {
		entry, err := stream.Recv()
		if err != nil {
			break
		}
		entries = append(entries, entry)
	}
	return entries
}

// SSEEvent represents a parsed Server-Sent Event.
type SSEEvent struct {
	Event string // "message" if no explicit event field
	Data  string
}

// CollectSSEEvents connects to the SSE endpoint for a run and collects
// events until the "done" event or timeout.
func (e *TestEnv) CollectSSEEvents(t *testing.T, runID string, timeout time.Duration) []SSEEvent {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	url := fmt.Sprintf("%s/api/runs/%s/stream", e.HTTPURL, runID)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var events []SSEEvent
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)

	var currentEvent string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event: ") {
			currentEvent = strings.TrimPrefix(line, "event: ")
		} else if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			eventType := currentEvent
			if eventType == "" {
				eventType = "message"
			}
			events = append(events, SSEEvent{Event: eventType, Data: data})
			if currentEvent == "done" {
				return events
			}
			currentEvent = ""
		} else if line == "" {
			// blank line resets event
			currentEvent = ""
		}
	}
	return events
}

// SSEDataLines extracts the parsed LogLine content from SSE data events (excludes "done").
func SSEDataLines(events []SSEEvent) []logstream.LogLine {
	var lines []logstream.LogLine
	for _, ev := range events {
		if ev.Event == "done" {
			continue
		}
		if ev.Event == "meta" {
			continue
		}
		var line logstream.LogLine
		if err := json.Unmarshal([]byte(ev.Data), &line); err == nil {
			lines = append(lines, line)
		}
	}
	return lines
}
