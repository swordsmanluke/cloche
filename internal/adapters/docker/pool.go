package docker

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"strings"
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
// until the StepResult is received or the context is cancelled. When resume is
// true, the ExecuteStep message carries the resume flag so the agent continues
// an existing LLM conversation rather than starting a fresh one.
func (cs *ContainerSession) ExecuteStep(ctx context.Context, step *domain.Step, resume bool) (domain.StepResult, error) {
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
				Resume:    resume,
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

// failAllPending sends a synthetic "fail" StepResult to every pending channel,
// unblocking any ExecuteStep callers that are waiting for a result. This is
// called when the agent session disconnects unexpectedly.
func (cs *ContainerSession) failAllPending() {
	cs.mu.Lock()
	pending := cs.pending
	cs.pending = make(map[string]chan *pb.StepResult)
	cs.mu.Unlock()

	for reqID, ch := range pending {
		select {
		case ch <- &pb.StepResult{RequestId: reqID, Result: "fail"}:
		default:
		}
	}
}

// deliverResult routes a StepResult to the pending channel for its request ID.
func (cs *ContainerSession) deliverResult(result *pb.StepResult) {
	cs.mu.Lock()
	ch, ok := cs.pending[result.RequestId]
	if ok {
		delete(cs.pending, result.RequestId)
	}
	cs.mu.Unlock()

	if ok {
		select {
		case ch <- result:
		default:
		}
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
// NotifyReadyWithStream). CleanupAttempt stops and removes all containers
// belonging to an attempt unless the containers should be kept (failure,
// --keep-container, or abort).
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

// resolveAttempt looks up the attemptID for a containerID. Falls back to prefix
// matching when an exact lookup misses — Docker hostnames are the short 12-char
// container ID while the pool stores the full 64-char ID. Must be called with
// p.mu held.
func (p *ContainerPool) resolveAttempt(containerID string) string {
	if aID, ok := p.containerAttempt[containerID]; ok {
		return aID
	}
	if len(containerID) >= 12 {
		for fullID, aID := range p.containerAttempt {
			if strings.HasPrefix(fullID, containerID) {
				return aID
			}
		}
	}
	return ""
}

// SessionFor returns the existing container session for the attempt, or starts
// a new container using cfg and blocks until the in-container agent sends
// AgentReady (signalled via NotifyReadyWithStream). Subsequent calls with the
// same attemptID return the existing session without starting another container.
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

// NotifyReadyWithStream signals that the agent running in containerID has sent
// AgentReady. It registers the send function on the session so steps can be
// dispatched to the agent, then unblocks any SessionFor call waiting on the
// attempt. The send function must be safe to call concurrently.
func (p *ContainerPool) NotifyReadyWithStream(containerID string, send func(*pb.DaemonMessage) error) {
	p.mu.Lock()
	attemptID := p.resolveAttempt(containerID)
	var entry *poolEntry
	if attemptID != "" {
		entry = p.attempts[attemptID]
	}
	// Register the send function on all sessions for this attempt.
	if entry != nil {
		for _, sess := range entry.sessions {
			sess.mu.Lock()
			sess.send = send
			if sess.pending == nil {
				sess.pending = make(map[string]chan *pb.StepResult)
			}
			sess.mu.Unlock()
		}
	}
	p.mu.Unlock()

	if entry == nil {
		return
	}
	entry.readyOnce.Do(func() {
		close(entry.readyCh)
	})
}

// NotifyReady signals that the agent running in containerID has sent AgentReady,
// without registering a send function. This unblocks any SessionFor call waiting
// on that attempt. Prefer NotifyReadyWithStream when a gRPC send function is
// available.
func (p *ContainerPool) NotifyReady(containerID string) {
	p.mu.Lock()
	attemptID := p.resolveAttempt(containerID)
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

// DeliverResult routes a StepResult to the session that issued the matching
// ExecuteStep request. The containerID is used to find the attempt and session.
func (p *ContainerPool) DeliverResult(containerID string, result *pb.StepResult) {
	p.mu.Lock()
	attemptID := p.resolveAttempt(containerID)
	var entry *poolEntry
	if attemptID != "" {
		entry = p.attempts[attemptID]
	}
	p.mu.Unlock()

	if entry == nil || len(entry.sessions) == 0 {
		return
	}
	entry.sessions[0].deliverResult(result)
}

// FailPendingRequests sends a synthetic "fail" result to all pending ExecuteStep
// requests for the given container, unblocking any callers waiting on those
// channels. This is called when an agent session disconnects unexpectedly.
func (p *ContainerPool) FailPendingRequests(containerID string) {
	p.mu.Lock()
	attemptID := p.resolveAttempt(containerID)
	var sess *ContainerSession
	if attemptID != "" {
		if entry, ok := p.attempts[attemptID]; ok {
			for _, s := range entry.sessions {
				if s.ContainerID == containerID {
					sess = s
					break
				}
			}
		}
	}
	p.mu.Unlock()

	if sess != nil {
		sess.failAllPending()
	}
}

// containerResumer is an optional runtime capability for committing containers
// to images and removing images. Implemented by docker.Runtime for resume support.
type containerResumer interface {
	CommitContainer(ctx context.Context, containerID, attemptID string) (string, error)
	RemoveImage(ctx context.Context, imageTag string) error
}

// CommitForResume commits all containers associated with attemptID to Docker
// images, preserving their filesystem state for cross-attempt resume. Returns a
// map of containerID → imageTag. Returns nil, nil when no containers are
// registered for the attempt. Returns an error if the runtime does not support
// commit or if any commit fails.
func (p *ContainerPool) CommitForResume(ctx context.Context, attemptID string) (map[string]string, error) {
	p.mu.Lock()
	entry, exists := p.attempts[attemptID]
	p.mu.Unlock()

	if !exists {
		return nil, nil
	}

	resumer, ok := p.runtime.(containerResumer)
	if !ok {
		return nil, fmt.Errorf("container runtime does not support image commit for resume")
	}

	images := make(map[string]string, len(entry.sessions))
	for _, sess := range entry.sessions {
		tag, err := resumer.CommitContainer(ctx, sess.ContainerID, attemptID)
		if err != nil {
			return nil, fmt.Errorf("committing container %s: %w", sess.ContainerID, err)
		}
		images[sess.ContainerID] = tag
		log.Printf("pool: committed container %s to image %s for resume", sess.ContainerID, tag)
	}
	return images, nil
}

// StartFromImage starts a new container session for the given key from the
// specified committed image. Unlike SessionFor, the project directory is not
// copied into the new container — the committed image already contains the
// workspace state from the previous run. Blocks until the in-container agent
// sends AgentReady.
func (p *ContainerPool) StartFromImage(ctx context.Context, key, image string, cfg ports.ContainerConfig) (*ContainerSession, error) {
	cfg.Image = image
	cfg.ProjectDir = "" // committed image already has workspace state
	return p.SessionFor(ctx, key, cfg)
}

// RemoveImages removes Docker images created for resume. This is best-effort:
// errors are logged but do not cause failure.
func (p *ContainerPool) RemoveImages(ctx context.Context, images map[string]string) {
	resumer, ok := p.runtime.(containerResumer)
	if !ok {
		return
	}
	for _, tag := range images {
		if err := resumer.RemoveImage(ctx, tag); err != nil {
			log.Printf("pool: failed to remove resume image %s: %v", tag, err)
		} else {
			log.Printf("pool: removed resume image %s", tag)
		}
	}
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
