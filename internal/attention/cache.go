package attention

import (
	"context"
	"log"
	"sync"
	"time"
)

// DefaultRefreshInterval is how often the cache recomputes every project,
// absent an explicit interval.
const DefaultRefreshInterval = 60 * time.Second

// DefaultMaxParallelRefresh bounds how many projects the background
// refresher recomputes concurrently, absent an explicit value.
const DefaultMaxParallelRefresh = 4

// Snapshot is a cached attention computation for one project: the items as
// of ComputedAt. A project that has never been refreshed reports a zero
// Snapshot (nil Items, zero ComputedAt).
type Snapshot struct {
	Items      []Item
	ComputedAt time.Time
}

// ComputeFunc performs the fresh, tracker-hitting computation for a single
// project (typically Compute itself, wired up with a project's deps). The
// Cache calls this only from its background refresher — never from a
// request path.
type ComputeFunc func(ctx context.Context, projectDir string) ([]Item, error)

// ProjectLister returns the set of project directories to refresh on each
// timer tick.
type ProjectLister func(ctx context.Context) ([]string, error)

// Cache holds the most recently computed attention Snapshot for each
// project, kept fresh by a background loop rather than on the request path:
// Compute runs a project's list-tasks host workflow, which costs seconds per
// project and does not scale to a dashboard-polled endpoint serving many
// projects. Get always returns instantly. All exported methods are safe for
// concurrent use.
type Cache struct {
	compute  ComputeFunc
	interval time.Duration
	parallel int

	entriesMu sync.RWMutex
	entries   map[string]Snapshot

	sfMu     sync.Mutex
	inflight map[string]chan struct{}
	pending  map[string]bool
}

// NewCache constructs a Cache. interval <= 0 uses DefaultRefreshInterval;
// parallel <= 0 uses DefaultMaxParallelRefresh.
func NewCache(compute ComputeFunc, interval time.Duration, parallel int) *Cache {
	if interval <= 0 {
		interval = DefaultRefreshInterval
	}
	if parallel <= 0 {
		parallel = DefaultMaxParallelRefresh
	}
	return &Cache{
		compute:  compute,
		interval: interval,
		parallel: parallel,
		entries:  make(map[string]Snapshot),
		inflight: make(map[string]chan struct{}),
		pending:  make(map[string]bool),
	}
}

// Get returns the most recently computed Snapshot for projectDir without
// blocking, even while a refresh for it is in flight. A project that has
// never been refreshed reports a zero Snapshot.
func (c *Cache) Get(projectDir string) Snapshot {
	c.entriesMu.RLock()
	defer c.entriesMu.RUnlock()
	return c.entries[projectDir]
}

// Refresh triggers an asynchronous recomputation of projectDir and returns
// immediately, without waiting for it to finish. If a refresh for
// projectDir is already running, this queues exactly one more run right
// after it finishes (so a burst of triggers during a slow compute still
// ends with one fully up-to-date recomputation) rather than starting a
// second one concurrently.
func (c *Cache) Refresh(projectDir string) {
	c.startOrJoin(projectDir)
}

// Run starts the background refresh loop: it refreshes every known project
// immediately, then again every interval, bounding concurrent refreshes to
// c.parallel so a slow tracker script on one project cannot pile up work for
// the rest. Blocks until ctx is cancelled; callers should run it in a
// goroutine.
func (c *Cache) Run(ctx context.Context, list ProjectLister) {
	c.tick(ctx, list)

	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.tick(ctx, list)
		}
	}
}

// tick refreshes every project returned by list, running up to c.parallel
// refreshes at a time, and waits for the sweep to finish (or ctx to be
// cancelled) before returning.
func (c *Cache) tick(ctx context.Context, list ProjectLister) {
	dirs, err := list(ctx)
	if err != nil {
		log.Printf("attention cache: listing projects: %v", err)
		return
	}

	sem := make(chan struct{}, c.parallel)
	var wg sync.WaitGroup
	for _, dir := range dirs {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			wg.Wait()
			return
		}
		wg.Add(1)
		go func(projectDir string) {
			defer wg.Done()
			defer func() { <-sem }()
			c.refreshAndWait(ctx, projectDir)
		}(dir)
	}
	wg.Wait()
}

// startOrJoin ensures a refresh for projectDir is running (starting one if
// none is) and returns a channel that's closed once that refresh — including
// any rerun queued via Refresh while it was in flight — completes.
func (c *Cache) startOrJoin(projectDir string) <-chan struct{} {
	c.sfMu.Lock()
	if ch, ok := c.inflight[projectDir]; ok {
		c.pending[projectDir] = true
		c.sfMu.Unlock()
		return ch
	}
	ch := make(chan struct{})
	c.inflight[projectDir] = ch
	c.sfMu.Unlock()

	go c.runUntilQuiescent(projectDir, ch)
	return ch
}

// refreshAndWait triggers a refresh of projectDir and blocks until it (and
// any queued rerun) completes, or ctx is cancelled.
func (c *Cache) refreshAndWait(ctx context.Context, projectDir string) {
	ch := c.startOrJoin(projectDir)
	select {
	case <-ch:
	case <-ctx.Done():
	}
}

// runUntilQuiescent computes projectDir's attention set, storing the result,
// and repeats immediately if another Refresh arrived while it was running —
// so the cache always converges on the most recently requested state before
// releasing waiters.
func (c *Cache) runUntilQuiescent(projectDir string, done chan struct{}) {
	for {
		items, err := c.compute(context.Background(), projectDir)
		if err != nil {
			log.Printf("attention cache: refresh %s: %v", projectDir, err)
		} else {
			c.entriesMu.Lock()
			c.entries[projectDir] = Snapshot{Items: items, ComputedAt: time.Now()}
			c.entriesMu.Unlock()
		}

		c.sfMu.Lock()
		if c.pending[projectDir] {
			c.pending[projectDir] = false
			c.sfMu.Unlock()
			continue
		}
		delete(c.inflight, projectDir)
		c.sfMu.Unlock()
		close(done)
		return
	}
}
