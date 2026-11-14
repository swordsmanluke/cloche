package dra

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// PollOutcome is what one poll invocation reported, using the poll-step
// protocol: a CLOCHE_RESULT marker is a decision (whatever the exit code),
// exit 0 without one is "not yet", non-zero without one is a failure.
type PollOutcome struct {
	Result string // the marker's name; "" when none
	Failed bool   // non-zero exit and no marker
	Output string // the script's output, markers stripped
}

// PollRunner runs one poll invocation.
type PollRunner interface {
	Poll(ctx context.Context, spec PollSpec) PollOutcome
}

// WaitStore keeps pending waits.
type WaitStore interface {
	Save(w Wait) error
	Delete(id string) error
	All() ([]Wait, error)
}

// WaitRegistry holds the pending waits of every DRA and turns each one's end
// (ready, failed or timed out) into one wait event for its DRA.
type WaitRegistry struct {
	mu     sync.Mutex
	store  WaitStore
	runner PollRunner
	notify func(project, dra string, e Event)
	now    func() time.Time
	seq    int
}

// NewWaitRegistry returns a registry. notify delivers a wait event to the
// DRA's inbox (where it is batched like any other event).
func NewWaitRegistry(store WaitStore, runner PollRunner, notify func(project, dra string, e Event)) *WaitRegistry {
	r := &WaitRegistry{store: store, runner: runner, notify: notify, now: time.Now}
	// An unreadable store is not handled here: every Register and Tick reads it
	// again and returns the error, and Register refuses before an id is used.
	if all, err := store.All(); err == nil {
		for _, w := range all {
			var n int
			if _, err := fmt.Sscanf(w.ID, "w-%d", &n); err == nil && n > r.seq {
				r.seq = n
			}
		}
	}
	return r
}

// SetClock overrides the clock (tests).
func (r *WaitRegistry) SetClock(now func() time.Time) { r.now = now }

// Register validates and records a wait for the DRA and returns its id.
func (r *WaitRegistry) Register(project, name string, req WaitRequest) (string, error) {
	if err := req.Normalize(); err != nil {
		return "", err
	}
	if _, err := ResolvePoll(project, name, req); err != nil {
		return "", err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	all, err := r.store.All()
	if err != nil {
		return "", err
	}
	pending := 0
	for _, w := range all {
		if w.Project == project && w.DRA == name {
			pending++
		}
	}
	if pending >= MaxPendingWaits {
		return "", fmt.Errorf("over the %d pending wait limit: cancel a wait first", MaxPendingWaits)
	}
	r.seq++
	w := Wait{ID: fmt.Sprintf("w-%d", r.seq), WaitRequest: req, DRA: name, Project: project, CreatedAt: r.now()}
	if err := r.store.Save(w); err != nil {
		r.seq--
		return "", err
	}
	return w.ID, nil
}

// List returns the DRA's pending waits, oldest first.
func (r *WaitRegistry) List(project, name string) ([]Wait, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	all, err := r.store.All()
	if err != nil {
		return nil, err
	}
	var out []Wait
	for _, w := range all {
		if w.Project == project && w.DRA == name {
			out = append(out, w)
		}
	}
	return out, nil
}

// Cancel removes one of the DRA's own pending waits. Another DRA's wait looks
// the same as one that does not exist.
func (r *WaitRegistry) Cancel(project, name, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	all, err := r.store.All()
	if err != nil {
		return err
	}
	for _, w := range all {
		if w.ID == id && w.Project == project && w.DRA == name {
			return r.store.Delete(id)
		}
	}
	return fmt.Errorf("no pending wait %s", id)
}

// Tick runs one pass: it ends waits past their timeout and polls those whose
// interval has elapsed (the first poll is immediate). A wait ends with exactly
// one event. A wait whose store update fails is left for the next pass; the
// failures are returned together.
func (r *WaitRegistry) Tick(ctx context.Context) error {
	r.mu.Lock()
	all, err := r.store.All()
	r.mu.Unlock()
	if err != nil {
		return fmt.Errorf("listing waits: %w", err)
	}
	var errs []error
	for _, w := range all {
		if err := r.tickOne(ctx, w); err != nil {
			errs = append(errs, fmt.Errorf("wait %s: %w", w.ID, err))
		}
	}
	return errors.Join(errs...)
}

func (r *WaitRegistry) tickOne(ctx context.Context, w Wait) error {
	now := r.now()
	if now.Sub(w.CreatedAt) >= w.Timeout {
		return r.finish(w, WaitTimeout, "", fmt.Sprintf("timed out after %s", w.Timeout))
	}
	if !w.LastPollAt.IsZero() && now.Sub(w.LastPollAt) < w.Interval {
		return nil
	}
	spec, err := ResolvePoll(w.Project, w.DRA, w.WaitRequest)
	if err != nil {
		return r.finish(w, WaitFailed, "", err.Error())
	}
	out := r.runner.Poll(ctx, spec)
	switch {
	case out.Result != "":
		w.LastOutput = out.Output
		return r.finish(w, WaitReady, out.Result, "")
	case out.Failed:
		w.LastOutput = out.Output
		return r.finish(w, WaitFailed, "", "the poll exited non-zero")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	pending, err := r.has(w.ID)
	if err != nil || !pending {
		return err
	}
	w.LastPollAt, w.LastOutput = now, out.Output
	return r.store.Save(w)
}

// has reports whether the wait is still pending. The caller holds r.mu.
func (r *WaitRegistry) has(id string) (bool, error) {
	all, err := r.store.All()
	if err != nil {
		return false, err
	}
	for _, w := range all {
		if w.ID == id {
			return true, nil
		}
	}
	return false, nil
}

// finish ends a wait and sends its event, unless it was cancelled meanwhile.
func (r *WaitRegistry) finish(w Wait, outcome, result, detail string) error {
	r.mu.Lock()
	pending, err := r.has(w.ID)
	if err == nil && pending {
		err = r.store.Delete(w.ID)
	}
	r.mu.Unlock()
	if err != nil || !pending {
		return err
	}
	r.notify(w.Project, w.DRA, Event{Kind: KindWait, Subject: w.ID, Text: waitEventText(w, outcome, result, detail)})
	return nil
}

const maxEventOutput = 1000

func waitEventText(w Wait, outcome, result, detail string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%q (poll %s) %s", w.Note, w.Poll, outcome)
	if result != "" {
		fmt.Fprintf(&b, ", result %s", result)
	}
	if detail != "" {
		fmt.Fprintf(&b, " (%s)", detail)
	}
	out := strings.TrimSpace(w.LastOutput)
	if out == "" {
		b.WriteString("; last output: (none)")
		return b.String()
	}
	if len(out) > maxEventOutput {
		out = out[:maxEventOutput] + "..."
	}
	b.WriteString("; last output: " + strings.Join(strings.Fields(out), " "))
	return b.String()
}
