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
// It handles only the methods exercised by L1 scenarios.
type fakeLoopServer struct {
	pb.UnimplementedClocheServiceServer
	mu          sync.Mutex
	loopRunning bool
}

func (s *fakeLoopServer) DisableLoop(_ context.Context, req *pb.DisableLoopRequest) (*pb.DisableLoopResponse, error) {
	s.mu.Lock()
	s.loopRunning = false
	s.mu.Unlock()
	return &pb.DisableLoopResponse{}, nil
}

func (s *fakeLoopServer) GetProjectInfo(_ context.Context, req *pb.GetProjectInfoRequest) (*pb.GetProjectInfoResponse, error) {
	s.mu.Lock()
	running := s.loopRunning
	s.mu.Unlock()
	return &pb.GetProjectInfoResponse{
		Name:        "test-project",
		LoopRunning: running,
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

	// in-process daemon for L1 scenarios that need gRPC
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
	return nil
}

func (s *loopResumeGateCtx) thereAreNoResumableRuns() error {
	s.resumableRuns = 0
	return nil
}

func (s *loopResumeGateCtx) aRunWasInFlightAtShutdown() error {
	s.inFlightRun = true
	return godog.ErrPending
}

func (s *loopResumeGateCtx) aTaskIsDispatchedToTheLoop() error {
	return godog.ErrPending
}

// ─── When steps ──────────────────────────────────────────────────────────────

// theOperatorRuns dispatches "cloche loop <subcommand>" to the appropriate
// in-process implementation, capturing output in s.commandOutput.
func (s *loopResumeGateCtx) theOperatorRuns(cmd string) error {
	parts := strings.Fields(cmd)
	if len(parts) < 2 || parts[0] != "cloche" {
		return fmt.Errorf("unsupported command in L1 test: %q", cmd)
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
		// cloche loop quiesce
		count, qErr := s.runMockQuiesce(ctx)
		if qErr != nil {
			s.commandErr = qErr
			return nil
		}
		fmt.Fprintf(&buf, "%d resumable runs parked\n", count)

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
			count, qErr := s.runMockQuiesce(ctx)
			if qErr != nil {
				s.commandErr = qErr
				return nil
			}
			fmt.Fprintf(&buf, "%d resumable runs parked\n", count)
		}

	case len(parts) >= 3 && parts[1] == "loop" && parts[2] == "status":
		// cloche loop status — mirror cmdStatusProject output relevant to L1
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
		fmt.Fprintf(&buf, "Resumable runs: %d\n", s.resumableRuns)

	default:
		s.commandErr = fmt.Errorf("unrecognised loop command in L1 test: %q", cmd)
		return nil
	}

	s.commandOutput = buf.String()
	s.commandErr = nil
	return nil
}

// runMockQuiesce is the L1 stub: returns s.resumableRuns as the parked count
// without modifying any daemon state. TODO: L2 replaces with real RPC.
func (s *loopResumeGateCtx) runMockQuiesce(_ context.Context) (int, error) {
	return s.resumableRuns, nil
}

func (s *loopResumeGateCtx) theDaemonIsRestarted() error {
	s.restarted = true
	return godog.ErrPending
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
	return godog.ErrPending
}

func (s *loopResumeGateCtx) theInFlightRunIsAutomaticallyResumed() error {
	return godog.ErrPending
}

func (s *loopResumeGateCtx) noRunsAreAutomaticallyResumed() error {
	return godog.ErrPending
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
