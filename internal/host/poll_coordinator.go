package host

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/cloche-dev/cloche/internal/ports"
)

// PollCoordinator manages human step poll sessions. The orchestration loop
// drives all poll timing via DrivePolls; individual executors register
// sessions and block on result channels.
//
// Design: the executor goroutine blocks on a result channel. On each loop
// tick, DrivePolls checks which sessions are due for a poll invocation and
// runs the script in a background goroutine. When the script returns a
// decision (non-empty, non-pending result), the result is sent on the
// channel, unblocking the executor.
type PollCoordinator struct {
	mu       sync.Mutex
	sessions map[string]*pollSession
}

type pollSession struct {
	runID      string
	stepName   string
	resultCh   chan string                                // buffered(1); executor blocks on this
	invokeFn   func(ctx context.Context) (string, error) // runs one poll invocation
	interval   time.Duration
	lastPollAt time.Time // time the last poll invocation was started
	inFlight   bool      // true while an invocation goroutine is running
	invStart   time.Time // when the current in-flight invocation started
	invCancel  context.CancelFunc
}

// NewPollCoordinator creates a new PollCoordinator.
func NewPollCoordinator() *PollCoordinator {
	return &PollCoordinator{sessions: make(map[string]*pollSession)}
}

func sessionKey(runID, stepName string) string {
	return runID + ":" + stepName
}

// Register creates a session for (runID, stepName) and returns a channel
// that receives the final result when the poll script returns a decision.
// The first poll fires on the next DrivePolls call after registration.
func (c *PollCoordinator) Register(
	runID, stepName string,
	invokeFn func(ctx context.Context) (string, error),
	interval time.Duration,
) <-chan string {
	c.mu.Lock()
	defer c.mu.Unlock()

	ch := make(chan string, 1)
	c.sessions[sessionKey(runID, stepName)] = &pollSession{
		runID:      runID,
		stepName:   stepName,
		resultCh:   ch,
		invokeFn:   invokeFn,
		interval:   interval,
		lastPollAt: time.Now().Add(-interval), // eligible for immediate first poll
	}
	return ch
}

// Unregister removes the session and cancels any in-flight invocation.
// Called when the executor's context is cancelled (e.g. step timeout).
func (c *PollCoordinator) Unregister(runID, stepName string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := sessionKey(runID, stepName)
	if s, ok := c.sessions[key]; ok {
		if s.invCancel != nil {
			s.invCancel()
		}
		delete(c.sessions, key)
	}
}

// DrivePolls checks all sessions and:
//   - triggers polls that are due (now >= lastPollAt + interval)
//   - fails sessions whose in-flight invocation exceeds 4× the interval
//
// Should be called on each orchestration loop tick. store is optional; when
// provided, last_poll_at is updated in the DB after each pending result.
func (c *PollCoordinator) DrivePolls(ctx context.Context, store ports.HumanPollStore) {
	c.mu.Lock()
	var toTrigger []*pollSession
	var toFail []*pollSession
	now := time.Now()
	for key, s := range c.sessions {
		if s.inFlight {
			if elapsed := now.Sub(s.invStart); elapsed > 4*s.interval {
				log.Printf("poll coordinator: step %q run %q: invocation running for %v (>4× interval %v), failing",
					s.stepName, s.runID, elapsed.Round(time.Second), s.interval)
				if s.invCancel != nil {
					s.invCancel()
				}
				s.inFlight = false
				s.invCancel = nil
				toFail = append(toFail, s)
				delete(c.sessions, key)
			}
		} else if now.Sub(s.lastPollAt) >= s.interval {
			toTrigger = append(toTrigger, s)
		}
	}
	c.mu.Unlock()

	// Deliver fail results outside the lock.
	for _, s := range toFail {
		s.resultCh <- "fail"
		if store != nil {
			_ = store.DeleteHumanPoll(ctx, s.runID, s.stepName)
		}
	}

	for _, s := range toTrigger {
		c.triggerPoll(s, store)
	}
}

// triggerPoll starts a background invocation for the given session.
func (c *PollCoordinator) triggerPoll(s *pollSession, store ports.HumanPollStore) {
	c.mu.Lock()
	key := sessionKey(s.runID, s.stepName)
	if _, ok := c.sessions[key]; !ok {
		c.mu.Unlock()
		return // session was unregistered
	}
	if s.inFlight {
		c.mu.Unlock()
		return // already running
	}

	invCtx, invCancel := context.WithCancel(context.Background())
	s.inFlight = true
	s.invStart = time.Now()
	s.invCancel = invCancel
	runID := s.runID
	stepName := s.stepName
	invokeFn := s.invokeFn
	log.Printf("poll coordinator: polling step %q run %q (interval=%v)", stepName, runID, s.interval)
	c.mu.Unlock()

	go func() {
		result, err := invokeFn(invCtx)
		// Note: invCancel() is called at the end of each path to release resources.
		// It must NOT be called before checking invCtx.Err(), because calling it
		// would set invCtx.Err() != nil and mask whether an external cancellation
		// (from Unregister or 4× overage check) occurred.

		c.mu.Lock()
		s2, ok := c.sessions[sessionKey(runID, stepName)]
		if !ok {
			// Session unregistered while in-flight (e.g. executor context cancelled).
			c.mu.Unlock()
			invCancel()
			return
		}

		// If the invocation context was cancelled by an external caller
		// (Unregister or 4× overage check), do not deliver any result —
		// the caller already handled it.
		if invCtx.Err() != nil {
			s2.inFlight = false
			s2.invCancel = nil
			c.mu.Unlock()
			invCancel()
			return
		}

		s2.inFlight = false
		s2.invCancel = nil

		var deliver string
		if err != nil {
			log.Printf("poll coordinator: poll error for step %q run %q: %v", stepName, runID, err)
			deliver = "fail"
		} else {
			deliver = result // "" = pending, non-empty = decision
		}

		if deliver != "" {
			// Decision (or error): deliver result and clean up.
			s2.resultCh <- deliver
			delete(c.sessions, sessionKey(runID, stepName))
			c.mu.Unlock()
			invCancel()
			if store != nil {
				_ = store.DeleteHumanPoll(context.Background(), runID, stepName)
			}
		} else {
			// Pending: record the poll timestamp and continue.
			s2.lastPollAt = time.Now()
			c.mu.Unlock()
			invCancel()
			if store != nil {
				if rec, getErr := store.GetHumanPoll(context.Background(), runID, stepName); getErr == nil && rec != nil {
					rec.LastPollAt = s2.lastPollAt
					rec.PollCount++
					_ = store.UpsertHumanPoll(context.Background(), rec)
				}
			}
		}
	}()
}

// SessionCount returns the number of active sessions. Useful for testing.
func (c *PollCoordinator) SessionCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.sessions)
}
