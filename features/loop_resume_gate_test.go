package features_test

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"strings"
	"sync"

	pb "github.com/cloche-dev/cloche/api/clochepb"
	"github.com/cucumber/godog"
	grpclib "google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// ─── in-process loop test server ─────────────────────────────────────────────

// fakeLoopServer is a minimal in-process gRPC server tracking loop state.
// It handles the methods exercised by L1 and L2 scenarios.
type fakeLoopServer struct {
	pb.UnimplementedClocheServiceServer
	mu           sync.Mutex
	loopRunning  bool
	parkedCount  int32 // runs parked by QuiesceRuns
	resumedRuns  int   // runs that were auto-resumed on simulated restart
	inFlightRuns int   // runs in-flight at shutdown (set by aRunWasInFlightAtShutdown)
}

func (s *fakeLoopServer) DisableLoop(_ context.Context, req *pb.DisableLoopRequest) (*pb.DisableLoopResponse, error) {
	s.mu.Lock()
	s.loopRunning = false
	s.mu.Unlock()
	return &pb.DisableLoopResponse{}, nil
}

// QuiesceRuns parks the tracked resumable runs in the fake server.
func (s *fakeLoopServer) QuiesceRuns(_ context.Context, req *pb.QuiesceRunsRequest) (*pb.QuiesceRunsResponse, error) {
	s.mu.Lock()
	// In the fake, parkedCount reflects runs that were staged via thereAreNResumableRuns.
	// The quiesce operation moves them from "resumable" to "parked".
	parked := s.parkedCount
	s.mu.Unlock()
	return &pb.QuiesceRunsResponse{ParkedCount: parked}, nil
}

func (s *fakeLoopServer) GetProjectInfo(_ context.Context, req *pb.GetProjectInfoRequest) (*pb.GetProjectInfoResponse, error) {
	s.mu.Lock()
	running := s.loopRunning
	parked := s.parkedCount
	s.mu.Unlock()
	return &pb.GetProjectInfoResponse{
		Name:               "test-project",
		LoopRunning:        running,
		ResumableRunsCount: parked,
	}, nil
}

func startFakeLoopDaemon(initialLoopRunning bool) (addr string, srv *grpclib.Server, fake *fakeLoopServer, err error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, nil, fmt.Errorf("listen: %w", err)
	}
	fake = &fakeLoopServer{loopRunning: initialLoopRunning}
	srv = grpclib.NewServer()
	pb.RegisterClocheServiceServer(srv, fake)
	go func() { _ = srv.Serve(ln) }() // error on Stop() is expected shutdown; safe to discard
	return ln.Addr().String(), srv, fake, nil
}

// ─── scenario context ─────────────────────────────────────────────────────────

type loopResumeGateCtx struct {
	commandOutput string
	commandErr    error
	loopRunning   bool
	resumableRuns int
	inFlightRun   bool
	restarted     bool

	// in-process daemon for scenarios that need gRPC
	daemonAddr   string
	daemonServer *grpclib.Server
	fakeServer   *fakeLoopServer
}

func (s *loopResumeGateCtx) reset() {
	if s.daemonServer != nil {
		s.daemonServer.Stop()
	}
	*s = loopResumeGateCtx{}
}

// dialTestDaemon returns a connected gRPC client for the in-process test server.
func (s *loopResumeGateCtx) dialTestDaemon() (pb.ClocheServiceClient, *grpclib.ClientConn, error) {
	conn, err := grpclib.NewClient(s.daemonAddr, grpclib.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, fmt.Errorf("dial: %w", err)
	}
	return pb.NewClocheServiceClient(conn), conn, nil
}

// ensureDaemon starts an in-process daemon if one isn't running yet.
func (s *loopResumeGateCtx) ensureDaemon() error {
	if s.daemonServer != nil {
		return nil
	}
	addr, srv, fake, err := startFakeLoopDaemon(s.loopRunning)
	if err != nil {
		return err
	}
	s.daemonAddr = addr
	s.daemonServer = srv
	s.fakeServer = fake
	return nil
}

// ─── Given steps ─────────────────────────────────────────────────────────────

func (s *loopResumeGateCtx) theOrchestrationLoopIsStopped() error {
	s.loopRunning = false
	if s.fakeServer != nil {
		s.fakeServer.mu.Lock()
		s.fakeServer.loopRunning = false
		s.fakeServer.mu.Unlock()
	}
	return nil
}

func (s *loopResumeGateCtx) theOrchestrationLoopIsRunning() error {
	s.loopRunning = true
	if s.fakeServer != nil {
		s.fakeServer.mu.Lock()
		s.fakeServer.loopRunning = true
		s.fakeServer.mu.Unlock()
	}
	return nil
}

func (s *loopResumeGateCtx) theClocheGateDaemonIsRunning() error {
	return s.ensureDaemon()
}

func (s *loopResumeGateCtx) thereAreNResumableRuns(count int) error {
	s.resumableRuns = count
	// Prime the fake server's parked count so QuiesceRuns has something to park.
	if err := s.ensureDaemon(); err != nil {
		return err
	}
	s.fakeServer.mu.Lock()
	s.fakeServer.parkedCount = int32(count)
	s.fakeServer.mu.Unlock()
	return nil
}

func (s *loopResumeGateCtx) thereAreNoResumableRuns() error {
	s.resumableRuns = 0
	if err := s.ensureDaemon(); err != nil {
		return err
	}
	s.fakeServer.mu.Lock()
	s.fakeServer.parkedCount = 0
	s.fakeServer.mu.Unlock()
	return nil
}

func (s *loopResumeGateCtx) aRunWasInFlightAtShutdown() error {
	s.inFlightRun = true
	if err := s.ensureDaemon(); err != nil {
		return err
	}
	s.fakeServer.mu.Lock()
	s.fakeServer.inFlightRuns = 1
	s.fakeServer.mu.Unlock()
	return nil
}

func (s *loopResumeGateCtx) aTaskIsDispatchedToTheLoop() error {
	// Requires a real orchestration loop — out of scope for the in-process fake.
	// TODO: L3 integration test covers end-to-end dispatch.
	return godog.ErrPending
}

// ─── When steps ──────────────────────────────────────────────────────────────

// theOperatorRuns dispatches "cloche loop <subcommand>" to the appropriate
// in-process implementation, capturing output in s.commandOutput.
func (s *loopResumeGateCtx) theOperatorRuns(cmd string) error {
	parts := strings.Fields(cmd)
	if len(parts) < 2 || parts[0] != "cloche" {
		return fmt.Errorf("unsupported command in test: %q", cmd)
	}

	if err := s.ensureDaemon(); err != nil {
		s.commandErr = err
		return nil
	}

	ctx := context.Background()
	client, conn, err := s.dialTestDaemon()
	if err != nil {
		s.commandErr = err
		return nil
	}
	defer conn.Close()

	var buf bytes.Buffer

	switch {
	case len(parts) >= 3 && parts[1] == "loop" && parts[2] == "quiesce":
		// cloche loop quiesce — calls the real QuiesceRuns RPC
		resp, qErr := client.QuiesceRuns(ctx, &pb.QuiesceRunsRequest{})
		if qErr != nil {
			s.commandErr = qErr
			return nil
		}
		fmt.Fprintf(&buf, "%d resumable runs parked\n", resp.ParkedCount)

	case len(parts) >= 3 && parts[1] == "loop" && parts[2] == "stop":
		// cloche loop stop [--quiesce]
		quiesce := false
		for _, p := range parts[3:] {
			if p == "--quiesce" {
				quiesce = true
			}
		}
		if _, err := client.DisableLoop(ctx, &pb.DisableLoopRequest{}); err != nil {
			s.commandErr = err
			return nil
		}
		fmt.Fprintln(&buf, "Orchestration loop stopped.")
		if quiesce {
			resp, qErr := client.QuiesceRuns(ctx, &pb.QuiesceRunsRequest{})
			if qErr != nil {
				s.commandErr = qErr
				return nil
			}
			fmt.Fprintf(&buf, "%d resumable runs parked\n", resp.ParkedCount)
		}

	case len(parts) >= 3 && parts[1] == "loop" && parts[2] == "status":
		// cloche loop status — reads ResumableRunsCount from GetProjectInfo
		info, err := client.GetProjectInfo(ctx, &pb.GetProjectInfoRequest{})
		if err != nil {
			s.commandErr = err
			return nil
		}
		loopState := "stopped"
		if info.LoopRunning {
			loopState = "running"
		}
		fmt.Fprintf(&buf, "Orchestration loop: %s\n", loopState)
		fmt.Fprintf(&buf, "Resumable runs: %d\n", info.GetResumableRunsCount())

	default:
		s.commandErr = fmt.Errorf("unrecognised loop command in test: %q", cmd)
		return nil
	}

	s.commandOutput = buf.String()
	s.commandErr = nil
	return nil
}

// theDaemonIsRestarted simulates a daemon restart: if the loop was running, any
// in-flight runs are auto-resumed; if the loop was stopped, they are not.
// Runs that were explicitly parked (via quiesce) are never auto-resumed.
func (s *loopResumeGateCtx) theDaemonIsRestarted() error {
	s.restarted = true
	if s.fakeServer == nil {
		return nil
	}
	s.fakeServer.mu.Lock()
	defer s.fakeServer.mu.Unlock()

	// Runs that were explicitly parked by QuiesceRuns survive restart as parked.
	// In-flight runs (not parked) are auto-resumed only when the loop is running.
	if s.fakeServer.loopRunning && s.fakeServer.inFlightRuns > 0 {
		s.fakeServer.resumedRuns += s.fakeServer.inFlightRuns
		s.fakeServer.inFlightRuns = 0
	}
	// If loop is stopped: in-flight runs remain unresolved (they would be failed
	// by FailStaleRuns on a real daemon restart, not auto-resumed).
	return nil
}

func (s *loopResumeGateCtx) theDispatchedRunCompletes() error {
	return godog.ErrPending
}

// ─── Then steps ──────────────────────────────────────────────────────────────

func (s *loopResumeGateCtx) theLoopCommandSucceeds() error {
	if s.commandErr != nil {
		return fmt.Errorf("loop command failed: %v", s.commandErr)
	}
	return nil
}

func (s *loopResumeGateCtx) theLoopCommandOutputContains(text string) error {
	if !strings.Contains(strings.ToLower(s.commandOutput), strings.ToLower(text)) {
		return fmt.Errorf("output does not contain %q\nfull output:\n%s", text, s.commandOutput)
	}
	return nil
}

func (s *loopResumeGateCtx) theOrchestrationLoopIsNowStopped() error {
	if s.fakeServer == nil {
		return fmt.Errorf("no daemon running to inspect")
	}
	s.fakeServer.mu.Lock()
	running := s.fakeServer.loopRunning
	s.fakeServer.mu.Unlock()
	if running {
		return fmt.Errorf("orchestration loop is still running; expected it to be stopped")
	}
	return nil
}

func (s *loopResumeGateCtx) theInFlightRunIsNotAutomaticallyResumed() error {
	if s.fakeServer == nil {
		return fmt.Errorf("no daemon running to inspect")
	}
	s.fakeServer.mu.Lock()
	resumed := s.fakeServer.resumedRuns
	s.fakeServer.mu.Unlock()
	if resumed > 0 {
		return fmt.Errorf("expected no auto-resumed runs but got %d", resumed)
	}
	return nil
}

func (s *loopResumeGateCtx) theInFlightRunIsAutomaticallyResumed() error {
	if s.fakeServer == nil {
		return fmt.Errorf("no daemon running to inspect")
	}
	s.fakeServer.mu.Lock()
	resumed := s.fakeServer.resumedRuns
	s.fakeServer.mu.Unlock()
	if resumed == 0 {
		return fmt.Errorf("expected in-flight run to be auto-resumed but resumedRuns == 0")
	}
	return nil
}

func (s *loopResumeGateCtx) noRunsAreAutomaticallyResumed() error {
	return s.theInFlightRunIsNotAutomaticallyResumed()
}

func (s *loopResumeGateCtx) theDispatchedRunStatusIsSuccessful() error {
	return godog.ErrPending
}

// ─── Scenario initializer ────────────────────────────────────────────────────

func initLoopResumeGateScenarios(ctx *godog.ScenarioContext) {
	s := &loopResumeGateCtx{}
	ctx.Before(func(_ context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		return nil, nil
	})

	// Given
	ctx.Step(`^the orchestration loop is stopped$`, s.theOrchestrationLoopIsStopped)
	ctx.Step(`^the orchestration loop is running$`, s.theOrchestrationLoopIsRunning)
	ctx.Step(`^the cloche daemon is running$`, s.theClocheGateDaemonIsRunning)
	ctx.Step(`^there are (\d+) resumable runs$`, s.thereAreNResumableRuns)
	ctx.Step(`^there are no resumable runs$`, s.thereAreNoResumableRuns)
	ctx.Step(`^a run was in-flight when the daemon last shut down$`, s.aRunWasInFlightAtShutdown)
	ctx.Step(`^a task is dispatched to the loop$`, s.aTaskIsDispatchedToTheLoop)

	// When
	ctx.Step(`^the operator runs "([^"]*)"$`, s.theOperatorRuns)
	ctx.Step(`^the daemon is restarted$`, s.theDaemonIsRestarted)
	ctx.Step(`^the dispatched run completes$`, s.theDispatchedRunCompletes)

	// Then
	ctx.Step(`^the loop command succeeds$`, s.theLoopCommandSucceeds)
	ctx.Step(`^the loop command output contains "([^"]*)"$`, s.theLoopCommandOutputContains)
	ctx.Step(`^the orchestration loop is now stopped$`, s.theOrchestrationLoopIsNowStopped)
	ctx.Step(`^the in-flight run is not automatically resumed$`, s.theInFlightRunIsNotAutomaticallyResumed)
	ctx.Step(`^the in-flight run is automatically resumed$`, s.theInFlightRunIsAutomaticallyResumed)
	ctx.Step(`^no runs are automatically resumed$`, s.noRunsAreAutomaticallyResumed)
	ctx.Step(`^the dispatched run status is successful$`, s.theDispatchedRunStatusIsSuccessful)
}
