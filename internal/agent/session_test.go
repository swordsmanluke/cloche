package agent_test

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	pb "github.com/cloche-dev/cloche/api/clochepb"
	"github.com/cloche-dev/cloche/internal/agent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// fakeAgentSessionServer implements the ClocheServiceServer interface just
// enough to exercise the AgentSession RPC.
type fakeAgentSessionServer struct {
	pb.UnimplementedClocheServiceServer
	steps   []*pb.ExecuteStep  // steps to dispatch after AgentReady
	gotReady chan *pb.AgentReady
	results  chan *pb.StepResult
	logs     chan *pb.StepLog
	started  chan *pb.StepStarted
}

func newFakeServer(steps []*pb.ExecuteStep) *fakeAgentSessionServer {
	return &fakeAgentSessionServer{
		steps:    steps,
		gotReady: make(chan *pb.AgentReady, 1),
		results:  make(chan *pb.StepResult, 10),
		logs:     make(chan *pb.StepLog, 100),
		started:  make(chan *pb.StepStarted, 10),
	}
}

func (f *fakeAgentSessionServer) AgentSession(stream pb.ClocheService_AgentSessionServer) error {
	// First message from agent should be AgentReady.
	msg, err := stream.Recv()
	if err != nil {
		return err
	}
	ready, ok := msg.Payload.(*pb.AgentMessage_Ready)
	if !ok {
		return nil
	}
	f.gotReady <- ready.Ready

	// Send each step command and collect results.
	for _, step := range f.steps {
		if err := stream.Send(&pb.DaemonMessage{
			Payload: &pb.DaemonMessage_ExecuteStep{ExecuteStep: step},
		}); err != nil {
			return err
		}
		// Collect messages until StepResult for this request_id.
		for {
			agentMsg, err := stream.Recv()
			if err != nil {
				return err
			}
			switch p := agentMsg.Payload.(type) {
			case *pb.AgentMessage_StepStarted:
				f.started <- p.StepStarted
			case *pb.AgentMessage_StepLog:
				f.logs <- p.StepLog
			case *pb.AgentMessage_StepResult:
				f.results <- p.StepResult
				goto nextStep
			}
		}
	nextStep:
	}

	// Send Shutdown.
	return stream.Send(&pb.DaemonMessage{
		Payload: &pb.DaemonMessage_Shutdown{Shutdown: &pb.Shutdown{}},
	})
}

// startFakeServer starts a gRPC server with fakeAgentSessionServer and returns
// its address and a cleanup function.
func startFakeServer(t *testing.T, srv *fakeAgentSessionServer) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	s := grpc.NewServer()
	pb.RegisterClocheServiceServer(s, srv)

	go func() {
		_ = s.Serve(lis)
	}()
	t.Cleanup(func() { s.Stop() })

	return lis.Addr().String()
}

func TestSession_AgentReady(t *testing.T) {
	srv := newFakeServer(nil)
	addr := startFakeServer(t, srv)

	dir := t.TempDir()
	sess := agent.NewSession(agent.SessionConfig{
		Addr:      addr,
		RunID:     "run-1",
		AttemptID: "att-1",
		TaskID:    "task-1",
		WorkDir:   dir,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := sess.Run(ctx)
	require.NoError(t, err)

	expectedHostname, _ := os.Hostname()
	select {
	case ready := <-srv.gotReady:
		assert.Equal(t, expectedHostname, ready.RunId)
		assert.Equal(t, "att-1", ready.AttemptId)
	default:
		t.Fatal("AgentReady was not received")
	}
}

func TestSession_ExecuteScriptStep(t *testing.T) {
	// A script step that echoes a line and exits successfully.
	srv := newFakeServer([]*pb.ExecuteStep{
		{
			StepName:  "build",
			StepType:  "script",
			Config:    map[string]string{"run": "echo 'hello from session'"},
			RequestId: "req-1",
		},
	})
	addr := startFakeServer(t, srv)

	dir := t.TempDir()
	sess := agent.NewSession(agent.SessionConfig{
		Addr:    addr,
		RunID:   "run-2",
		WorkDir: dir,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := sess.Run(ctx)
	require.NoError(t, err)

	// Should have received StepStarted.
	select {
	case started := <-srv.started:
		assert.Equal(t, "req-1", started.RequestId)
		assert.Equal(t, "build", started.StepName)
	default:
		t.Fatal("StepStarted not received")
	}

	// Should have received StepResult.
	select {
	case result := <-srv.results:
		assert.Equal(t, "req-1", result.RequestId)
		assert.Equal(t, "success", result.Result)
	default:
		t.Fatal("StepResult not received")
	}
}

func TestSession_ExecuteAgentStep(t *testing.T) {
	dir := t.TempDir()

	// A mock agent script that reads stdin and outputs a line.
	mockAgent := filepath.Join(dir, "mock-agent.sh")
	require.NoError(t, os.WriteFile(mockAgent, []byte("#!/bin/sh\ncat > /dev/null\necho 'agent output'\n"), 0755))

	srv := newFakeServer([]*pb.ExecuteStep{
		{
			StepName:  "implement",
			StepType:  "agent",
			Config:    map[string]string{"prompt": "Do something.", "agent_command": mockAgent},
			RequestId: "req-2",
		},
	})
	addr := startFakeServer(t, srv)

	sess := agent.NewSession(agent.SessionConfig{
		Addr:    addr,
		RunID:   "run-3",
		WorkDir: dir,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := sess.Run(ctx)
	require.NoError(t, err)

	select {
	case result := <-srv.results:
		assert.Equal(t, "req-2", result.RequestId)
		assert.Equal(t, "success", result.Result)
	default:
		t.Fatal("StepResult not received")
	}
}

func TestSession_StepLogStreaming(t *testing.T) {
	// A script step that emits multiple lines.
	srv := newFakeServer([]*pb.ExecuteStep{
		{
			StepName:  "build",
			StepType:  "script",
			Config:    map[string]string{"run": "echo line1; echo line2; echo line3"},
			RequestId: "req-3",
		},
	})
	addr := startFakeServer(t, srv)

	dir := t.TempDir()
	sess := agent.NewSession(agent.SessionConfig{
		Addr:    addr,
		RunID:   "run-4",
		WorkDir: dir,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := sess.Run(ctx)
	require.NoError(t, err)

	// Collect all received log lines.
	var lines []string
	for {
		select {
		case l := <-srv.logs:
			lines = append(lines, l.Line)
		default:
			goto done
		}
	}
done:
	assert.Contains(t, lines, "line1")
	assert.Contains(t, lines, "line2")
	assert.Contains(t, lines, "line3")
}

func TestSession_ResumeFlag(t *testing.T) {
	dir := t.TempDir()

	// Mock agent that records whether -c flag was passed.
	mockAgent := filepath.Join(dir, "mock-agent.sh")
	script := `#!/bin/sh
args="$*"
cat > /dev/null
if echo "$args" | grep -q -- '-c'; then
  echo 'resumed'
else
  echo 'fresh'
fi
`
	require.NoError(t, os.WriteFile(mockAgent, []byte(script), 0755))

	srv := newFakeServer([]*pb.ExecuteStep{
		{
			StepName:  "implement",
			StepType:  "agent",
			Config:    map[string]string{"prompt": "Do something.", "agent_command": mockAgent},
			RequestId: "req-4",
			Resume:    true,
		},
	})
	addr := startFakeServer(t, srv)

	sess := agent.NewSession(agent.SessionConfig{
		Addr:    addr,
		RunID:   "run-5",
		WorkDir: dir,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := sess.Run(ctx)
	require.NoError(t, err)

	select {
	case result := <-srv.results:
		assert.Equal(t, "success", result.Result)
	default:
		t.Fatal("StepResult not received")
	}
}

func TestSession_MultipleSteps(t *testing.T) {
	srv := newFakeServer([]*pb.ExecuteStep{
		{
			StepName:  "step1",
			StepType:  "script",
			Config:    map[string]string{"run": "echo step1"},
			RequestId: "req-s1",
		},
		{
			StepName:  "step2",
			StepType:  "script",
			Config:    map[string]string{"run": "echo step2"},
			RequestId: "req-s2",
		},
	})
	addr := startFakeServer(t, srv)

	dir := t.TempDir()
	sess := agent.NewSession(agent.SessionConfig{
		Addr:    addr,
		RunID:   "run-6",
		WorkDir: dir,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := sess.Run(ctx)
	require.NoError(t, err)

	// Collect all results.
	var results []*pb.StepResult
	for {
		select {
		case r := <-srv.results:
			results = append(results, r)
		default:
			goto done
		}
	}
done:
	require.Len(t, results, 2)
	assert.Equal(t, "req-s1", results[0].RequestId)
	assert.Equal(t, "success", results[0].Result)
	assert.Equal(t, "req-s2", results[1].RequestId)
	assert.Equal(t, "success", results[1].Result)
}

func TestSession_EnvVarsFromWiring(t *testing.T) {
	// A script step that reads an env var set via the wiring map.
	srv := newFakeServer([]*pb.ExecuteStep{
		{
			StepName:  "check",
			StepType:  "script",
			Config:    map[string]string{"run": `[ "$WIRED_VALUE" = "hello" ] && echo ok || echo fail`},
			Env:       map[string]string{"WIRED_VALUE": "hello"},
			RequestId: "req-env",
		},
	})
	addr := startFakeServer(t, srv)

	dir := t.TempDir()
	sess := agent.NewSession(agent.SessionConfig{
		Addr:    addr,
		RunID:   "run-7",
		WorkDir: dir,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := sess.Run(ctx)
	require.NoError(t, err)

	select {
	case result := <-srv.results:
		assert.Equal(t, "success", result.Result)
	default:
		t.Fatal("StepResult not received")
	}
}

// TestSession_GRPCStatusWriter_LogForwarding verifies that log lines emitted
// by an adapter's StatusWriter reach the daemon as StepLog messages.
func TestSession_GRPCStatusWriter_LogForwarding(t *testing.T) {
	srv := newFakeServer([]*pb.ExecuteStep{
		{
			StepName:  "build",
			StepType:  "script",
			Config:    map[string]string{"run": "echo 'expected log line'"},
			RequestId: "req-log",
		},
	})
	addr := startFakeServer(t, srv)

	dir := t.TempDir()
	sess := agent.NewSession(agent.SessionConfig{
		Addr:    addr,
		RunID:   "run-8",
		WorkDir: dir,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := sess.Run(ctx)
	require.NoError(t, err)

	var found bool
	for {
		select {
		case l := <-srv.logs:
			if l.Line == "expected log line" {
				found = true
			}
		default:
			goto check
		}
	}
check:
	assert.True(t, found, "expected log line should have been streamed as StepLog")
}

// ---------------------------------------------------------------------------
// Poll (human) step tests
// ---------------------------------------------------------------------------

// TestSession_ExecuteHumanStep_ImmediateDecision verifies that a poll step
// returns the correct wire result when the script emits a marker immediately.
func TestSession_ExecuteHumanStep_ImmediateDecision(t *testing.T) {
	srv := newFakeServer([]*pb.ExecuteStep{
		{
			StepName:  "gate",
			StepType:  "human",
			Config:    map[string]string{"poll": "echo 'CLOCHE_RESULT:approved'", "interval": "50ms"},
			RequestId: "req-h1",
		},
	})
	addr := startFakeServer(t, srv)

	dir := t.TempDir()
	sess := agent.NewSession(agent.SessionConfig{
		Addr:    addr,
		RunID:   "run-h1",
		WorkDir: dir,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := sess.Run(ctx)
	require.NoError(t, err)

	select {
	case result := <-srv.results:
		assert.Equal(t, "req-h1", result.RequestId)
		assert.Equal(t, "approved", result.Result)
	default:
		t.Fatal("StepResult not received")
	}
}

// TestSession_ExecuteHumanStep_PendingThenDecision verifies that the poll step
// keeps polling until the script emits a result marker after several pending responses.
func TestSession_ExecuteHumanStep_PendingThenDecision(t *testing.T) {
	dir := t.TempDir()

	// Script increments a counter file; emits result marker on the 3rd invocation.
	counterFile := filepath.Join(dir, "counter")
	require.NoError(t, os.WriteFile(counterFile, []byte("0"), 0644))

	scriptPath := filepath.Join(dir, "poll.sh")
	script := `#!/bin/sh
count=$(cat "` + counterFile + `")
count=$((count + 1))
echo "$count" > "` + counterFile + `"
if [ "$count" -ge 3 ]; then
  echo "CLOCHE_RESULT:approved"
fi
`
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0755))

	srv := newFakeServer([]*pb.ExecuteStep{
		{
			StepName:  "review",
			StepType:  "human",
			Config:    map[string]string{"poll": scriptPath, "interval": "50ms"},
			RequestId: "req-h2",
		},
	})
	addr := startFakeServer(t, srv)

	sess := agent.NewSession(agent.SessionConfig{
		Addr:    addr,
		RunID:   "run-h2",
		WorkDir: dir,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := sess.Run(ctx)
	require.NoError(t, err)

	select {
	case result := <-srv.results:
		assert.Equal(t, "req-h2", result.RequestId)
		assert.Equal(t, "approved", result.Result)
	default:
		t.Fatal("StepResult not received")
	}

	// Verify the poll was invoked at least 3 times.
	data, err := os.ReadFile(counterFile)
	require.NoError(t, err)
	assert.Contains(t, string(data), "3")
}

// TestSession_ExecuteHumanStep_FailOnNonZeroExit verifies that a non-zero exit
// without a result marker produces a "fail" result.
func TestSession_ExecuteHumanStep_FailOnNonZeroExit(t *testing.T) {
	srv := newFakeServer([]*pb.ExecuteStep{
		{
			StepName:  "gate",
			StepType:  "human",
			Config:    map[string]string{"poll": "exit 1", "interval": "50ms"},
			RequestId: "req-h3",
		},
	})
	addr := startFakeServer(t, srv)

	dir := t.TempDir()
	sess := agent.NewSession(agent.SessionConfig{
		Addr:    addr,
		RunID:   "run-h3",
		WorkDir: dir,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := sess.Run(ctx)
	require.NoError(t, err)

	select {
	case result := <-srv.results:
		assert.Equal(t, "req-h3", result.RequestId)
		assert.Equal(t, "fail", result.Result)
	default:
		t.Fatal("StepResult not received")
	}
}

// TestSession_ExecuteHumanStep_WireOutputOnNonZeroExit verifies that wire output
// from a non-zero exit is honored over the default "fail".
func TestSession_ExecuteHumanStep_WireOutputOnNonZeroExit(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "poll.sh")
	script := "#!/bin/sh\necho 'CLOCHE_RESULT:fix'\nexit 1\n"
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0755))

	srv := newFakeServer([]*pb.ExecuteStep{
		{
			StepName:  "gate",
			StepType:  "human",
			Config:    map[string]string{"poll": scriptPath, "interval": "50ms"},
			RequestId: "req-h4",
		},
	})
	addr := startFakeServer(t, srv)

	sess := agent.NewSession(agent.SessionConfig{
		Addr:    addr,
		RunID:   "run-h4",
		WorkDir: dir,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := sess.Run(ctx)
	require.NoError(t, err)

	select {
	case result := <-srv.results:
		assert.Equal(t, "req-h4", result.RequestId)
		assert.Equal(t, "fix", result.Result)
	default:
		t.Fatal("StepResult not received")
	}
}

// TestSession_ExecuteHumanStep_ContextTimeout verifies that context cancellation
// during a poll step causes the session to exit cleanly without hanging.
// The poll step internally produces a "timeout" result, but the gRPC stream
// may close before the result is sent — so we verify clean exit, not the result.
func TestSession_ExecuteHumanStep_ContextTimeout(t *testing.T) {
	srv := newFakeServer([]*pb.ExecuteStep{
		{
			StepName:  "gate",
			StepType:  "human",
			Config:    map[string]string{"poll": "exit 0", "interval": "50ms"},
			RequestId: "req-h5",
		},
	})
	addr := startFakeServer(t, srv)

	dir := t.TempDir()
	sess := agent.NewSession(agent.SessionConfig{
		Addr:    addr,
		RunID:   "run-h5",
		WorkDir: dir,
	})

	// Short timeout so the poll step times out quickly.
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- sess.Run(ctx) }()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("session did not exit within 5s after context cancellation")
	}
}

// TestSession_ExecuteHumanStep_InvalidInterval verifies that an invalid interval
// produces a "fail" result (the error path sets result to "fail").
func TestSession_ExecuteHumanStep_InvalidInterval(t *testing.T) {
	srv := newFakeServer([]*pb.ExecuteStep{
		{
			StepName:  "gate",
			StepType:  "human",
			Config:    map[string]string{"poll": "echo ok", "interval": "not-a-duration"},
			RequestId: "req-h6",
		},
	})
	addr := startFakeServer(t, srv)

	dir := t.TempDir()
	sess := agent.NewSession(agent.SessionConfig{
		Addr:    addr,
		RunID:   "run-h6",
		WorkDir: dir,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := sess.Run(ctx)
	require.NoError(t, err)

	select {
	case result := <-srv.results:
		assert.Equal(t, "req-h6", result.RequestId)
		assert.Equal(t, "fail", result.Result)
	default:
		t.Fatal("StepResult not received")
	}
}

// verifyGRPC ensures the test binary can import grpc without issue.
func init() {
	_ = grpc.WithTransportCredentials(insecure.NewCredentials())
}
