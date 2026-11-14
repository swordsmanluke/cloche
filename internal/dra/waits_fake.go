package dra

import (
	"context"
	"sync"
)

// Test doubles only. The daemon uses FileWaitStore (waitstore.go) and the
// recorded host run dra-wait-<name> (waitrun.go); these remain for tests that
// need a store or poll runner without a filesystem or a daemon.

// FakeWaitStore is an in-memory wait store, lost on restart.
type FakeWaitStore struct {
	mu    sync.Mutex
	waits []Wait
}

// NewFakeWaitStore returns an empty in-memory store.
func NewFakeWaitStore() *FakeWaitStore { return &FakeWaitStore{} }

// Save inserts or replaces a wait.
func (s *FakeWaitStore) Save(w Wait) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.waits {
		if s.waits[i].ID == w.ID {
			s.waits[i] = w
			return nil
		}
	}
	s.waits = append(s.waits, w)
	return nil
}

// Delete removes a wait.
func (s *FakeWaitStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.waits {
		if s.waits[i].ID == id {
			s.waits = append(s.waits[:i], s.waits[i+1:]...)
			break
		}
	}
	return nil
}

// All returns a copy of every wait.
func (s *FakeWaitStore) All() ([]Wait, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Wait(nil), s.waits...), nil
}

// FakePollRunner is a scripted poll runner: it returns scripted
// outcomes (keyed by the program in the spec's Argv[0]'s base name or, for the
// built-in poll, "questions") and records each spec it was given. With nothing
// scripted a poll is "not yet".
type FakePollRunner struct {
	mu      sync.Mutex
	script  map[string][]PollOutcome
	Invoked []PollSpec
}

// NewFakePollRunner returns a runner that always says "not yet".
func NewFakePollRunner() *FakePollRunner { return &FakePollRunner{script: map[string][]PollOutcome{}} }

// Script queues outcomes for the poll named poll; the last one repeats.
func (f *FakePollRunner) Script(poll string, outcomes ...PollOutcome) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.script[poll] = outcomes
}

// Poll returns the next scripted outcome.
func (f *FakePollRunner) Poll(_ context.Context, spec PollSpec) PollOutcome {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Invoked = append(f.Invoked, spec)
	key := pollKey(spec)
	q := f.script[key]
	if len(q) == 0 {
		return PollOutcome{}
	}
	out := q[0]
	if len(q) > 1 {
		f.script[key] = q[1:]
	}
	return out
}

func pollKey(spec PollSpec) string {
	if len(spec.Argv) >= 3 && spec.Argv[0] == "cloche" && spec.Argv[1] == "questions" {
		return QuestionsPoll
	}
	p := spec.Argv[0]
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			p = p[i+1:]
			break
		}
	}
	return p[:len(p)-len(PollScriptExt)]
}
