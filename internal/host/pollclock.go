package host

import (
	"sort"
	"sync"
	"time"
)

// PollClock is the time source for poll scheduling. nil means the real clock.
type PollClock interface {
	Now() time.Time
	// AfterFunc runs f after d and returns a function that cancels it.
	AfterFunc(d time.Duration, f func()) (stop func())
}

type realPollClock struct{}

func (realPollClock) Now() time.Time { return time.Now() }
func (realPollClock) AfterFunc(d time.Duration, f func()) func() {
	t := time.AfterFunc(d, f)
	return func() { t.Stop() }
}

// SetClock makes the coordinator use clk for "now" and for its per-session
// timers. Intended for tests (see FakePollClock); call before Register.
func (c *PollCoordinator) SetClock(clk PollClock) {
	c.now = clk.Now
	c.afterFunc = clk.AfterFunc
}

// InFlightCount reports how many poll invocations are currently running.
func (c *PollCoordinator) InFlightCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, s := range c.sessions {
		if s.inFlight {
			n++
		}
	}
	return n
}

// FakePollClock is a manually advanced PollClock for tests: Advance fires due
// timers in order, setting Now to each timer's due time as it runs it.
type FakePollClock struct {
	mu     sync.Mutex
	cur    time.Time
	timers []*fakeTimer
	// Settle, if set, is called after each fired timer so the test can wait
	// for goroutines started by that timer to quiesce.
	Settle func()
}

type fakeTimer struct {
	due time.Time
	f   func()
}

// NewFakePollClock returns a fake clock starting at a fixed instant.
func NewFakePollClock() *FakePollClock {
	return &FakePollClock{cur: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func (f *FakePollClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cur
}

func (f *FakePollClock) AfterFunc(d time.Duration, fn func()) func() {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := &fakeTimer{due: f.cur.Add(d), f: fn}
	f.timers = append(f.timers, t)
	return func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		for i, x := range f.timers {
			if x == t {
				f.timers = append(f.timers[:i], f.timers[i+1:]...)
				return
			}
		}
	}
}

// Advance moves the clock forward by d, running every timer that comes due.
func (f *FakePollClock) Advance(d time.Duration) {
	f.mu.Lock()
	target := f.cur.Add(d)
	f.mu.Unlock()
	for {
		f.mu.Lock()
		sort.SliceStable(f.timers, func(i, j int) bool { return f.timers[i].due.Before(f.timers[j].due) })
		if len(f.timers) == 0 || f.timers[0].due.After(target) {
			f.cur = target
			f.mu.Unlock()
			return
		}
		t := f.timers[0]
		f.timers = f.timers[1:]
		if t.due.After(f.cur) {
			f.cur = t.due
		}
		f.mu.Unlock()
		t.f()
		if f.Settle != nil {
			f.Settle()
		}
	}
}
