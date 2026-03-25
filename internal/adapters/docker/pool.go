package docker

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"sync"
	"sync/atomic"

	pb "github.com/cloche-dev/cloche/api/clochepb"
	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/ports"
)

// requestCounter is used to generate unique request IDs for step dispatch.
var requestCounter atomic.Int64

// generateRequestID returns a unique string ID for an ExecuteStep request.
func generateRequestID() string {
	return fmt.Sprintf("req-%d-%d", requestCounter.Add(1), rand.Int63n(1<<32))
}

// ContainerSession holds the container ID for a running agent container and
// the gRPC send function for dispatching steps to the in-container agent.
type ContainerSession struct {
	ContainerID string

	mu      sync.Mutex
	send    func(*pb.DaemonMessage) error
	pending map[string]chan *pb.StepResult
}

// ExecuteStep sends an ExecuteStep command to the in-container agent and blocks
// until the StepResult is received or the context is cancelled.
func (cs *ContainerSession) ExecuteStep(ctx context.Context, step *domain.Step) (domain.StepResult, error) {
	reqID := generateRequestID()
	ch := make(chan *pb.StepResult, 1)

	cs.mu.Lock()
	if cs.pending == nil {
		cs.pending = make(map[string]chan *pb.StepResult)
	}
	cs.pending[reqID] = ch
	cs.mu.Unlock()

	defer func() {
		cs.mu.Lock()
		delete(cs.pending, reqID)
		cs.mu.Unlock()
	}()

	cs.mu.Lock()
	sendFn := cs.send
	cs.mu.Unlock()

	if sendFn == nil {
		return domain.StepResult{}, fmt.Errorf("container session %s: send function not registered (agent not ready)", cs.ContainerID)
	}

	if err := sendFn(&pb.DaemonMessage{
		Payload: &pb.DaemonMessage_ExecuteStep{
			ExecuteStep: &pb.ExecuteStep{
				StepName:  step.Name,
				StepType:  string(step.Type),
				Config:    step.Config,
				RequestId: reqID,
			},
		},
	}); err != nil {
		return domain.StepResult{}, fmt.Errorf("sending ExecuteStep for step %q: %w", step.Name, err)
	}

	select {
	case <-ctx.Done():
		return domain.StepResult{}, ctx.Err()
	case result := <-ch:
		var usage *domain.TokenUsage
		if result.TokenUsage != nil {
			usage = &domain.TokenUsage{
				InputTokens:  result.TokenUsage.InputTokens,
				OutputTokens: result.TokenUsage.OutputTokens,
			}
		}
		return domain.StepResult{Result: result.Result, Usage: usage}, nil
	}
}

// NotifyReadyWithStream registers the gRPC send function on the session after
// the in-container agent sends AgentReady. This enables ExecuteStep to dispatch
// commands to the agent.
func (cs *ContainerSession) NotifyReadyWithStream(send func(*pb.DaemonMessage) error) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.send = send
}

// DeliverResult routes an incoming StepResult from the agent to the pending
// request channel, unblocking the corresponding ExecuteStep call.
func (cs *ContainerSession) DeliverResult(result *pb.StepResult) {
	cs.mu.Lock()
	ch, ok := cs.pending[result.RequestId]
	cs.mu.Unlock()

	if ok {
		ch <- result
	}
}

// Shutdown sends a Shutdown message to the in-container agent.
func (cs *ContainerSession) Shutdown() error {
	cs.mu.Lock()
	sendFn := cs.send
	cs.mu.Unlock()

	if sendFn == nil {
		return nil
	}
	return sendFn(&pb.DaemonMessage{
		Payload: &pb.DaemonMessage_Shutdown{
			Shutdown: &pb.Shutdown{},
		},
	})
}

// poolEntry tracks all containers created for a single attempt and a channel
// that is closed when the agent inside the first container sends AgentReady.
type poolEntry struct {
	sessions []*ContainerSession
	// readyOnce ensures the ready channel is closed at most once.
	readyOnce sync.Once
	readyCh   chan struct{}
}

func newPoolEntry() *poolEntry {
	return &poolEntry{
		readyCh: make(chan struct{}),
	}
}

// ContainerPool manages container lifecycle per attempt.
//
// Key: attemptID → one or more ContainerSessions.
//
// SessionFor returns the existing session for an attempt or starts a new
// container and blocks until the agent inside signals AgentReady (via
// NotifyReady). CleanupAttempt stops and removes all containers belonging to
// an attempt unless the containers should be kept (failure, --keep-container,
// or abort).
type ContainerPool struct {
	mu      sync.Mutex
	runtime ports.ContainerRuntime
	// attempts maps attemptID -> poolEntry
	attempts map[string]*poolEntry
	// containerAttempt maps containerID -> attemptID for NotifyReady lookups
	containerAttempt map[string]string
}

// NewContainerPool creates a ContainerPool backed by the given runtime.
func NewContainerPool(runtime ports.ContainerRuntime) *ContainerPool {
	return &ContainerPool{
		runtime:          runtime,
		attempts:         make(map[string]*poolEntry),
		containerAttempt: make(map[string]string),
	}
}

// SessionFor returns the existing container session for the attempt, or starts
// a new container using cfg and blocks until the in-container agent sends
// AgentReady (signalled via NotifyReady). Subsequent calls with the same
// attemptID return the existing session without starting another container.
func (p *ContainerPool) SessionFor(ctx context.Context, attemptID string, cfg ports.ContainerConfig) (*ContainerSession, error) {
	if attemptID == "" {
		return nil, fmt.Errorf("attemptID must not be empty")
	}

	p.mu.Lock()
	entry, exists := p.attempts[attemptID]
	if exists && len(entry.sessions) > 0 {
		sess := entry.sessions[0]
		p.mu.Unlock()
		return sess, nil
	}

	// Create (or reuse) the poolEntry before releasing the lock so concurrent
	// callers wait on the same ready channel.
	if !exists {
		entry = newPoolEntry()
		p.attempts[attemptID] = entry
	}
	p.mu.Unlock()

	// Start the container outside the lock to avoid blocking other attempts.
	containerID, err := p.runtime.Start(ctx, cfg)
	if err != nil {
		// Remove the entry so the caller may retry.
		p.mu.Lock()
		delete(p.attempts, attemptID)
		p.mu.Unlock()
		return nil, fmt.Errorf("starting container for attempt %s: %w", attemptID, err)
	}

	sess := &ContainerSession{ContainerID: containerID}

	p.mu.Lock()
	entry.sessions = append(entry.sessions, sess)
	p.containerAttempt[containerID] = attemptID
	p.mu.Unlock()

	// Wait for the agent inside the container to call back with AgentReady.
	select {
	case <-entry.readyCh:
		return sess, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("waiting for AgentReady for attempt %s: %w", attemptID, ctx.Err())
	}
}

// NotifyReady signals that the agent running in containerID has sent AgentReady.
// This unblocks any SessionFor call waiting on that attempt.
func (p *ContainerPool) NotifyReady(containerID string) {
	p.mu.Lock()
	attemptID := p.containerAttempt[containerID]
	var entry *poolEntry
	if attemptID != "" {
		entry = p.attempts[attemptID]
	}
	p.mu.Unlock()

	if entry == nil {
		return
	}
	entry.readyOnce.Do(func() {
		close(entry.readyCh)
	})
}

// CleanupAttempt stops and removes all containers associated with attemptID.
// Containers are kept (not removed) when any of the following is true:
//   - keepContainer is true (--keep-container CLI flag)
//   - runFailed is true (the attempt result was failed/cancelled)
//   - aborted is true (the attempt was aborted mid-run)
func (p *ContainerPool) CleanupAttempt(ctx context.Context, attemptID string, keepContainer, runFailed, aborted bool) error {
	p.mu.Lock()
	entry, exists := p.attempts[attemptID]
	if !exists {
		p.mu.Unlock()
		return nil
	}
	// Take ownership of the sessions and remove the entry.
	sessions := entry.sessions
	for _, sess := range sessions {
		delete(p.containerAttempt, sess.ContainerID)
	}
	delete(p.attempts, attemptID)
	p.mu.Unlock()

	keep := keepContainer || runFailed || aborted

	var errs []error
	for _, sess := range sessions {
		if keep {
			log.Printf("pool: keeping container %s for attempt %s", sess.ContainerID, attemptID)
			continue
		}
		// Stop before remove so the process has a chance to exit cleanly.
		if err := p.runtime.Stop(ctx, sess.ContainerID); err != nil {
			log.Printf("pool: stopping container %s: %v", sess.ContainerID, err)
		}
		if err := p.runtime.Remove(ctx, sess.ContainerID); err != nil {
			log.Printf("pool: removing container %s: %v", sess.ContainerID, err)
			errs = append(errs, fmt.Errorf("removing container %s: %w", sess.ContainerID, err))
		}
	}

	return errors.Join(errs...)
}
