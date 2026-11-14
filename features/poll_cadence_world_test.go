package features_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/swordsmanluke/cloche/internal/host"
)

const cadenceTolerance = 2 * time.Second

// cadence is the per-scenario state for the poll cadence scenarios.
var cadence *cadenceWorld

// fakeScript is the poll script stand-in: it records start/end fake times,
// optionally "runs" for dur of fake time, and always reports pending.
type fakeScript struct {
	clock    *host.FakePollClock
	dur      time.Duration
	mu       sync.Mutex
	starts   []time.Time
	ends     []time.Time
	active   atomic.Int32
	sleeping atomic.Int32
}

func (r *fakeScript) invoke(ctx context.Context) (string, error) {
	r.active.Add(1)
	defer r.active.Add(-1)
	r.mu.Lock()
	r.starts = append(r.starts, r.clock.Now())
	r.mu.Unlock()
	if r.dur > 0 {
		done := make(chan struct{})
		r.sleeping.Add(1)
		stop := r.clock.AfterFunc(r.dur, func() { r.sleeping.Add(-1); close(done) })
		select {
		case <-done:
		case <-ctx.Done():
			stop()
			r.sleeping.Add(-1)
			return "", ctx.Err()
		}
	}
	r.mu.Lock()
	r.ends = append(r.ends, r.clock.Now())
	r.mu.Unlock()
	return "", nil
}

type cadenceWorld struct {
	tick       time.Duration
	standalone bool
	interval   map[string]time.Duration
	timeout    time.Duration
	runners    map[string]*fakeScript
	clock      *host.FakePollClock
	t0         time.Time
	endedAfter time.Duration // fake time at which the step timed out; 0 = never
	order      []string
}

func (w *cadenceWorld) addPoll(name, interval, timeout string) error {
	iv, err := time.ParseDuration(interval)
	if err != nil {
		return err
	}
	to, err := time.ParseDuration(timeout)
	if err != nil {
		return err
	}
	if w.clock == nil {
		w.clock = host.NewFakePollClock()
		w.runners = map[string]*fakeScript{}
	}
	w.interval[name] = iv
	w.timeout = to
	w.runners[name] = &fakeScript{clock: w.clock}
	w.order = append(w.order, name)
	return nil
}

func (w *cadenceWorld) settled(coord *host.PollCoordinator) bool {
	var sleeping, active int32
	for _, r := range w.runners {
		sleeping += r.sleeping.Load()
		active += r.active.Load()
	}
	if active != sleeping {
		return false
	}
	return coord == nil || int32(coord.InFlightCount()) == sleeping
}

func (w *cadenceWorld) settle(coord *host.PollCoordinator) {
	deadline := time.Now().Add(2 * time.Second)
	for !w.settled(coord) && time.Now().Before(deadline) {
		time.Sleep(100 * time.Microsecond)
	}
	if coord == nil {
		time.Sleep(time.Millisecond) // standalone loop has no in-flight counter
	}
}

// simulate advances fake time in 1s steps, ticking the orchestration loop
// (DrivePolls) every w.tick, for total fake time.
func (w *cadenceWorld) simulate(total time.Duration) error {
	w.t0 = w.clock.Now()
	var coord *host.PollCoordinator
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if w.standalone {
		done := make(chan struct{})
		defer func() { cancel(); <-done }()
		go func() {
			defer close(done)
			host.PollStandalone(ctx, "wait", w.interval["wait"], w.runners["wait"].invoke, w.clock)
		}()
		w.clock.Settle = func() { w.settle(nil) }
		w.clock.AfterFunc(w.timeout, func() { w.endedAfter = w.clock.Now().Sub(w.t0); cancel() })
		time.Sleep(time.Millisecond)
	} else {
		coord = host.NewPollCoordinator()
		coord.SetClock(w.clock)
		w.clock.Settle = func() { w.settle(coord) }
		for _, name := range w.order {
			coord.Register("run1", name, w.runners[name].invoke, w.interval[name])
		}
		w.clock.AfterFunc(w.timeout, func() {
			w.endedAfter = w.clock.Now().Sub(w.t0)
			for _, name := range w.order {
				coord.Unregister("run1", name)
			}
		})
	}

	for elapsed := time.Duration(0); elapsed <= total; elapsed += time.Second {
		if coord != nil && elapsed%w.tick == 0 {
			coord.DrivePolls(context.Background(), nil)
			w.settle(coord)
		}
		w.clock.Advance(time.Second)
		w.settle(coord)
	}
	return nil
}

func (w *cadenceWorld) assertCadence(name string, interval time.Duration) error {
	r, ok := w.runners[name]
	if !ok {
		return fmt.Errorf("no poll step %q", name)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.starts) < 2 {
		return fmt.Errorf("%q ran %d times", name, len(r.starts))
	}
	for i := 1; i < len(r.starts); i++ {
		g := r.starts[i].Sub(r.starts[i-1])
		if g < interval-cadenceTolerance || g > interval+cadenceTolerance {
			return fmt.Errorf("%q: gap before run %d is %v, want about %v (starts %v)", name, i, g, interval, r.starts)
		}
	}
	return nil
}
