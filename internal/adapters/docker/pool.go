package docker

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"

	"github.com/cloche-dev/cloche/internal/ports"
)

// ContainerSession holds the container ID for a running agent container.
type ContainerSession struct {
	ContainerID string
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
