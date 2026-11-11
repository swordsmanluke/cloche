package signal

import (
	"context"
	"log"
	"sync"
)

// WorkflowName is the host workflow every signal is handed to.
const WorkflowName = "handle-notification"

// Store persists received signals.
type Store interface {
	SaveSignal(ctx context.Context, projectDir string, sig Signal) (int64, error)
}

// InFlight is a running handler for one (project, source, key). Signals that
// arrive while it runs are added to it rather than starting another handler.
type InFlight struct {
	mu     sync.Mutex
	count  int
	latest Signal
	onAdd  func(count int, latest Signal)
	closed bool
}

// Count is how many occurrences this handler run covers (>= 1).
func (f *InFlight) Count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.count
}

// OnAdd registers fn to be told, with the new total and the newest signal,
// each time another occurrence joins this run. If occurrences have already
// joined, fn is called once immediately with the current state. Calls are
// serialised.
func (f *InFlight) OnAdd(fn func(count int, latest Signal)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.onAdd = fn
	if f.count > 1 {
		fn(f.count, f.latest)
	}
}

// add joins sig to the run; false means the run already finished.
func (f *InFlight) add(sig Signal) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return false
	}
	f.count++
	f.latest = sig
	if f.onAdd != nil {
		f.onAdd(f.count, f.latest)
	}
	return true
}

func (f *InFlight) close() {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
}

// HandlerFunc runs the handle-notification workflow for sig. It should call
// run.OnAdd to learn of occurrences that join while it is running.
type HandlerFunc func(ctx context.Context, projectDir string, sig Signal, run *InFlight) error

// Dispatcher stores signals and runs the handler, coalescing by
// (project, source, key) while a handler is running.
type Dispatcher struct {
	Store   Store
	Handler HandlerFunc
	// BaseContext is the context handler runs use; the request context ends
	// when the HTTP response is written. Defaults to context.Background().
	BaseContext context.Context

	mu       sync.Mutex
	inflight map[string]*InFlight
	wg       sync.WaitGroup
}

// Result of Submit.
type Result struct {
	ID        int64
	Coalesced bool // joined a handler that was already running
}

func flightKey(projectDir string, sig Signal) string {
	return projectDir + "\x00" + sig.Source + "\x00" + sig.Key
}

// Submit stores sig and either adds it to the running handler for the same
// source and key or starts a handler. It returns once the signal is stored;
// the handler runs in the background.
func (d *Dispatcher) Submit(ctx context.Context, projectDir string, sig Signal) (Result, error) {
	id, err := d.Store.SaveSignal(ctx, projectDir, sig)
	if err != nil {
		return Result{}, err
	}
	k := flightKey(projectDir, sig)

	var f *InFlight
	for {
		d.mu.Lock()
		if d.inflight == nil {
			d.inflight = map[string]*InFlight{}
		}
		cur, ok := d.inflight[k]
		if !ok {
			f = &InFlight{count: 1, latest: sig}
			d.inflight[k] = f
			d.mu.Unlock()
			break
		}
		d.mu.Unlock()
		if cur.add(sig) {
			return Result{ID: id, Coalesced: true}, nil
		}
		// The handler finished between lookup and join: drop it and retry.
		d.mu.Lock()
		if d.inflight[k] == cur {
			delete(d.inflight, k)
		}
		d.mu.Unlock()
	}

	base := d.BaseContext
	if base == nil {
		base = context.Background()
	}
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		err := d.Handler(base, projectDir, sig, f)
		if err != nil {
			log.Printf("signal: %s handler for %s/%s failed: %v", WorkflowName, sig.Source, sig.Key, err)
		}
		f.close()
		d.mu.Lock()
		if d.inflight[k] == f {
			delete(d.inflight, k)
		}
		d.mu.Unlock()
	}()
	return Result{ID: id}, nil
}

// Wait blocks until every running handler has finished (for tests/shutdown).
func (d *Dispatcher) Wait() { d.wg.Wait() }
