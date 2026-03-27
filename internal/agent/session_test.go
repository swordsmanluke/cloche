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

// verifyGRPC ensures the test binary can import grpc without issue.
func init() {
	_ = grpc.WithTransportCredentials(insecure.NewCredentials())
}
