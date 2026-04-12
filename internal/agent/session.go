package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	pb "github.com/cloche-dev/cloche/api/clochepb"
	"github.com/cloche-dev/cloche/internal/adapters/agents/generic"
	"github.com/cloche-dev/cloche/internal/adapters/agents/prompt"
	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/logstream"
	"github.com/cloche-dev/cloche/internal/protocol"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// SessionConfig holds configuration for the bidirectional session handler.
type SessionConfig struct {
	Addr      string // daemon gRPC address (CLOCHE_ADDR)
	RunID     string
	AttemptID string
	TaskID    string
	WorkDir   string
}

// Session handles the bidirectional AgentSession gRPC stream.
// It connects to the daemon, sends AgentReady, then loops receiving
// ExecuteStep commands, dispatching them to the appropriate adapter
// and streaming results back.
type Session struct {
	cfg SessionConfig
}

// NewSession creates a new Session with the given config.
func NewSession(cfg SessionConfig) *Session {
	return &Session{cfg: cfg}
}

// Run connects to the daemon, opens the AgentSession stream, sends
// AgentReady, and handles commands until a Shutdown is received or
// the context is cancelled.
func (s *Session) Run(ctx context.Context) error {
	conn, err := grpc.NewClient(s.cfg.Addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("dialing daemon at %s: %w", s.cfg.Addr, err)
	}
	defer conn.Close()

	client := pb.NewClocheServiceClient(conn)
	stream, err := client.AgentSession(ctx)
	if err != nil {
		return fmt.Errorf("opening AgentSession: %w", err)
	}

	// Send AgentReady to signal the daemon we are up. RunId identifies this
	// agent to the pool. Always use the container's hostname, which Docker sets
	// to the short container ID so the pool can match via prefix lookup.
	runID, _ := os.Hostname()
	if err := stream.Send(&pb.AgentMessage{
		Payload: &pb.AgentMessage_Ready{
			Ready: &pb.AgentReady{
				RunId:     runID,
				AttemptId: s.cfg.AttemptID,
			},
		},
	}); err != nil {
		return fmt.Errorf("sending AgentReady: %w", err)
	}

	// Unified log for this session.
	ulog, err := logstream.New(s.cfg.WorkDir)
	if err != nil {
		return fmt.Errorf("creating unified log: %w", err)
	}
	defer ulog.Close()

	// Thread-safe send over the stream.
	var sendMu sync.Mutex
	send := func(msg *pb.AgentMessage) error {
		sendMu.Lock()
		defer sendMu.Unlock()
		return stream.Send(msg)
	}

	// grpcStatusWriter translates StatusWriter MsgLog entries into StepLog gRPC messages.
	gsw := newGRPCStatusWriter(send)
	sw := protocol.NewStatusWriter(gsw)

	// Set up adapters, wiring them to the gRPC status writer for live log streaming.
	genericAdapter := generic.New()
	genericAdapter.RunID = s.cfg.RunID
	genericAdapter.StatusWriter = sw

	promptAdapter := prompt.New()
	promptAdapter.RunID = s.cfg.RunID
	promptAdapter.TaskID = s.cfg.TaskID
	promptAdapter.StatusWriter = sw

	// Apply agent command override from environment.
	if cmd, ok := os.LookupEnv("CLOCHE_AGENT_COMMAND"); ok {
		promptAdapter.Commands = prompt.ParseCommands(cmd)
	}

	// KV client reuses the existing connection.
	kvClient := pb.NewClocheServiceClient(conn)

	// Per-step context for cancellation support.
	var stepMu sync.Mutex
	var stepCancel context.CancelFunc

	for {
		msg, err := stream.Recv()
		if err != nil {
			if ctx.Err() != nil {
				return nil // context cancelled, clean exit
			}
			return fmt.Errorf("receiving from AgentSession: %w", err)
		}

		switch payload := msg.Payload.(type) {
		case *pb.DaemonMessage_ExecuteStep:
			cmd := payload.ExecuteStep

			stepCtx, cancel := context.WithCancel(ctx)
			stepMu.Lock()
			if stepCancel != nil {
				// Safety: cancel any previously active step (shouldn't happen per protocol).
				stepCancel()
			}
			stepCancel = cancel
			stepMu.Unlock()

			// Execute in a goroutine so we can receive StepCancelled concurrently.
			go func(c *pb.ExecuteStep, sCtx context.Context, sCancel context.CancelFunc) {
				defer sCancel()
				s.executeStep(sCtx, c, genericAdapter, promptAdapter, kvClient, ulog, send)
			}(cmd, stepCtx, cancel)

		case *pb.DaemonMessage_StepCancelled:
			stepMu.Lock()
			if stepCancel != nil {
				log.Printf("agent: cancelling step (request_id=%s)", payload.StepCancelled.RequestId)
				stepCancel()
			}
			stepMu.Unlock()

		case *pb.DaemonMessage_Shutdown:
			log.Printf("agent: received Shutdown from daemon, exiting")
			stepMu.Lock()
			if stepCancel != nil {
				stepCancel()
			}
			stepMu.Unlock()
			return nil

		default:
			// Ignore unrecognised messages (HostWorkflowResult etc.).
		}
	}
}

// executeStep runs a single step as directed by an ExecuteStep command,
// streaming StepStarted, StepLog, and StepResult messages back to the daemon.
func (s *Session) executeStep(
	ctx context.Context,
	cmd *pb.ExecuteStep,
	genericAdapter *generic.Adapter,
	promptAdapter *prompt.Adapter,
	kvClient pb.ClocheServiceClient,
	ulog *logstream.Writer,
	send func(*pb.AgentMessage) error,
) {
	// Apply output-mapped env vars from wiring. Steps run sequentially per the
	// protocol so temporary env mutation is safe.
	for k, v := range cmd.Env {
		os.Setenv(k, v)
	}
	defer func() {
		for k := range cmd.Env {
			os.Unsetenv(k)
		}
	}()

	// Signal step start.
	_ = send(&pb.AgentMessage{
		Payload: &pb.AgentMessage_StepStarted{
			StepStarted: &pb.StepStarted{
				RequestId: cmd.RequestId,
				StepName:  cmd.StepName,
			},
		},
	})

	step := &domain.Step{
		Name:   cmd.StepName,
		Type:   domain.StepType(cmd.StepType),
		Config: cmd.Config,
	}

	// Apply per-step agent overrides from config.
	if agentCmd := cmd.Config["agent_command"]; agentCmd != "" {
		promptAdapter.Commands = prompt.ParseCommands(agentCmd)
	}
	if agentArgs := cmd.Config["agent_args"]; agentArgs != "" {
		promptAdapter.ExplicitArgs = strings.Fields(agentArgs)
	}

	// Handle conversation resume flag.
	if cmd.Resume {
		promptAdapter.ResumeConversation = true
		defer func() { promptAdapter.ResumeConversation = false }()
	}

	var sr domain.StepResult
	var execErr error

	switch step.Type {
	case domain.StepTypeScript:
		sr, execErr = genericAdapter.Execute(ctx, step, s.cfg.WorkDir)
		sessionLogStepOutput(s.cfg.WorkDir, step.Name, ulog, logstream.TypeScript)
	case domain.StepTypeAgent:
		if _, ok := step.Config["run"]; ok {
			sr, execErr = genericAdapter.Execute(ctx, step, s.cfg.WorkDir)
			sessionLogStepOutput(s.cfg.WorkDir, step.Name, ulog, logstream.TypeScript)
		} else {
			sr, execErr = promptAdapter.Execute(ctx, step, s.cfg.WorkDir)
			sessionCopyToLLMLog(s.cfg.WorkDir, step.Name)
			sessionLogStepOutput(s.cfg.WorkDir, step.Name, ulog, logstream.TypeLLM)
		}
	case domain.StepTypeHuman:
		sr, execErr = s.executeHumanStep(ctx, step, s.cfg.WorkDir)
		sessionLogStepOutput(s.cfg.WorkDir, step.Name, ulog, logstream.TypeScript)
	default:
		execErr = fmt.Errorf("unknown step type: %s", step.Type)
	}

	// Record step result in KV store.
	if execErr == nil && sr.Result != "" && kvClient != nil && s.cfg.TaskID != "" {
		key := fmt.Sprintf("session:%s:result", step.Name)
		rCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		_, _ = kvClient.SetContextKey(rCtx, &pb.SetContextKeyRequest{
			TaskId:    s.cfg.TaskID,
			AttemptId: s.cfg.AttemptID,
			RunId:     s.cfg.RunID,
			Key:       key,
			Value:     sr.Result,
		})
	}

	// Determine final result and send StepResult.
	result := sr.Result
	if execErr != nil {
		if result == "" {
			result = "fail"
		}
		log.Printf("agent: step %q error: %v", cmd.StepName, execErr)
	}

	var tokenUsage *pb.TokenUsage
	if sr.Usage != nil {
		tokenUsage = &pb.TokenUsage{
			InputTokens:  sr.Usage.InputTokens,
			OutputTokens: sr.Usage.OutputTokens,
		}
	}

	_ = send(&pb.AgentMessage{
		Payload: &pb.AgentMessage_StepResult{
			StepResult: &pb.StepResult{
				RequestId:  cmd.RequestId,
				Result:     result,
				TokenUsage: tokenUsage,
			},
		},
	})
}

// sessionLogStepOutput reads the per-step log file and writes it to the unified log.
func sessionLogStepOutput(workDir, stepName string, ulog *logstream.Writer, typ logstream.EntryType) {
	logPath := filepath.Join(workDir, ".cloche", "output", stepName+".log")
	data, err := os.ReadFile(logPath)
	if err != nil || len(data) == 0 {
		return
	}
	ulog.Log(typ, string(data))
}

// sessionCopyToLLMLog copies the step log file to the llm-<step>.log path.
func sessionCopyToLLMLog(workDir, stepName string) {
	outputDir := filepath.Join(workDir, ".cloche", "output")
	srcPath := filepath.Join(outputDir, stepName+".log")
	dstPath := filepath.Join(outputDir, "llm-"+stepName+".log")
	data, err := os.ReadFile(srcPath)
	if err != nil {
		return
	}
	_ = os.WriteFile(dstPath, data, 0644)
}

// executeHumanStep runs a poll step inside the container using a standalone
// polling loop. Modeled on the host executor's executeHumanStepStandalone.
func (s *Session) executeHumanStep(ctx context.Context, step *domain.Step, workDir string) (domain.StepResult, error) {
	intervalStr := step.Config["interval"]
	interval, err := time.ParseDuration(intervalStr)
	if err != nil {
		return domain.StepResult{}, fmt.Errorf("human step %q: invalid interval %q: %w", step.Name, intervalStr, err)
	}

	// Maximum time allowed for a single invocation before the step is failed.
	maxInvocationTime := 4 * interval

	const defaultCheckInterval = 30 * time.Second
	checkInterval := defaultCheckInterval
	if interval < checkInterval {
		checkInterval = interval / 2
		if checkInterval < time.Second {
			checkInterval = time.Second
		}
	}

	type pollResult struct {
		result string
		err    error
	}

	// Buffered so the goroutine never blocks if we exit early.
	pollCh := make(chan pollResult, 1)
	invocationRunning := false
	var invocationStart time.Time

	// Set last poll to interval ago so the first poll fires immediately.
	lastPoll := time.Now().Add(-interval)

	for {
		now := time.Now()

		if invocationRunning {
			select {
			case pr := <-pollCh:
				invocationRunning = false
				lastPoll = now
				if pr.err != nil {
					return domain.StepResult{}, pr.err
				}
				if pr.result != "" {
					return domain.StepResult{Result: pr.result}, nil
				}
				// Empty result = pending; keep polling.
			default:
				// Still running — check for 4× overage.
				elapsed := now.Sub(invocationStart)
				if elapsed > maxInvocationTime {
					log.Printf("human step %q: invocation running for %v (>4× interval %v), failing step",
						step.Name, elapsed.Round(time.Second), interval)
					return domain.StepResult{Result: "fail"}, nil
				}
			}
		} else if now.Sub(lastPoll) >= interval {
			// Time to start a new poll invocation.
			invocationRunning = true
			invocationStart = now
			log.Printf("human step %q: polling (last=%s interval=%s)", step.Name, lastPoll.Format(time.RFC3339), interval)
			go func() {
				r, pollErr := runPollCommand(ctx, step.Config["poll"], step.Name, s.cfg.RunID, workDir)
				pollCh <- pollResult{result: r, err: pollErr}
			}()
		}

		select {
		case <-ctx.Done():
			return domain.StepResult{Result: "timeout"}, nil
		case <-time.After(checkInterval):
		}
	}
}

// runPollCommand runs a single invocation of a poll step's polling script.
// Returns the result name (empty if pending) and any error.
func runPollCommand(ctx context.Context, pollCmd, stepName, runID, workDir string) (string, error) {
	cmd := exec.CommandContext(ctx, "sh", "-c", pollCmd)
	cmd.Dir = workDir

	var baseEnv []string
	for _, ev := range os.Environ() {
		if !strings.HasPrefix(ev, "CLOCHE_RUN_ID=") {
			baseEnv = append(baseEnv, ev)
		}
	}
	cmd.Env = append(baseEnv,
		"CLOCHE_PROJECT_DIR="+workDir,
	)
	if runID != "" {
		cmd.Env = append(cmd.Env, "CLOCHE_RUN_ID="+runID)
	}

	output, err := cmd.CombinedOutput()
	markerResult, cleanOutput, found := protocol.ExtractResult(output)

	// Write cleaned output to log file (overwritten on each poll invocation).
	outputDir := filepath.Join(workDir, ".cloche", "output")
	if mkErr := os.MkdirAll(outputDir, 0755); mkErr == nil {
		_ = os.WriteFile(filepath.Join(outputDir, stepName+".log"), cleanOutput, 0644)
	}

	if err != nil {
		if _, ok := err.(*exec.ExitError); ok {
			if found {
				return markerResult, nil
			}
			return "fail", nil
		}
		return "", err
	}

	if found {
		return markerResult, nil
	}
	// Exit 0, no marker: pending.
	return "", nil
}

// grpcStatusWriter is an io.Writer that buffers lines from a protocol.StatusWriter
// and forwards MsgLog entries to the daemon as StepLog gRPC messages.
type grpcStatusWriter struct {
	send func(*pb.AgentMessage) error
	buf  []byte
}

func newGRPCStatusWriter(send func(*pb.AgentMessage) error) *grpcStatusWriter {
	return &grpcStatusWriter{send: send}
}

// Write buffers incoming bytes and processes complete newline-terminated JSON lines.
func (w *grpcStatusWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		idx := bytes.IndexByte(w.buf, '\n')
		if idx < 0 {
			break
		}
		line := w.buf[:idx]
		w.buf = w.buf[idx+1:]
		w.processLine(line)
	}
	return len(p), nil
}

// processLine parses a single JSON-encoded StatusMessage and, if it is a MsgLog
// entry, sends a corresponding StepLog message over the gRPC stream.
func (w *grpcStatusWriter) processLine(line []byte) {
	if len(bytes.TrimSpace(line)) == 0 {
		return
	}
	var msg protocol.StatusMessage
	if err := json.Unmarshal(line, &msg); err != nil {
		return
	}
	if msg.Type != protocol.MsgLog {
		return
	}
	_ = w.send(&pb.AgentMessage{
		Payload: &pb.AgentMessage_StepLog{
			StepLog: &pb.StepLog{
				StepName:  msg.StepName,
				Line:      msg.Message,
				Timestamp: msg.Timestamp.UnixNano(),
			},
		},
	})
}
