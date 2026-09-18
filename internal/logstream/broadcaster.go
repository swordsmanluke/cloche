package logstream

import (
	"sync"
)

// LogLine is a single log entry broadcast to subscribers.
type LogLine struct {
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"`                // "status", "script", "llm"
	Content   string `json:"content"`             // the log message
	StepName  string `json:"step_name,omitempty"` // originating step
	// RunID identifies which run published this line. Populated for
	// live-broadcast lines (see Broadcaster.Publish callers); empty for
	// lines reconstructed from an archived full.log, whose text format
	// doesn't carry run identity. Needed to disambiguate a step name that's
	// shared between a host run and a container run it dispatched, and to
	// let the web console filter a scoped step's lines out of an attempt's
	// fanned-in stream (see internal/adapters/web/static/console.js).
	RunID string `json:"run_id,omitempty"`
}

// Subscriber receives log lines via a channel.
type Subscriber struct {
	C    <-chan LogLine
	ch   chan LogLine
	once sync.Once
}

func newSubscriber(bufSize int) *Subscriber {
	ch := make(chan LogLine, bufSize)
	return &Subscriber{C: ch, ch: ch}
}

func (s *Subscriber) close() {
	s.once.Do(func() { close(s.ch) })
}

// DefaultHistoryCap bounds how many lines a run's in-memory broadcast
// history retains. Older lines are evicted once the cap is exceeded — they
// remain readable from the run's on-disk full.log via the /logs endpoint
// (see handleAPILogs), which is what "Load earlier" backfills from. Matches
// the client's default detail.allLines window (see static/console.js) so a
// freshly (re)connected subscriber's window lines up with what the browser
// is willing to hold in memory.
const DefaultHistoryCap = 5000

// Broadcaster fans out log lines from active runs to multiple subscribers.
// Thread-safe for concurrent use.
type Broadcaster struct {
	mu         sync.Mutex
	runs       map[string]*runBroadcast
	historyCap int
}

type runBroadcast struct {
	subscribers []*Subscriber
	history     []LogLine
	// historyBase is the sequence number of history[0]. It advances past 0
	// once the history cap starts evicting the oldest entries, so a seq
	// number always identifies the same line regardless of eviction.
	historyBase int
	// nextSeq is the sequence number that will be assigned to the next
	// published line. Every published line gets one, whether or not it
	// stays within the retained window.
	nextSeq int
	done    bool
}

// NewBroadcaster creates a new Broadcaster with the default history cap.
func NewBroadcaster() *Broadcaster {
	return NewBroadcasterWithHistoryCap(DefaultHistoryCap)
}

// NewBroadcasterWithHistoryCap creates a Broadcaster whose per-run history
// window is bounded at cap lines (DefaultHistoryCap if cap <= 0). Exposed
// mainly so tests can exercise eviction without publishing thousands of
// lines.
func NewBroadcasterWithHistoryCap(cap int) *Broadcaster {
	if cap <= 0 {
		cap = DefaultHistoryCap
	}
	return &Broadcaster{
		runs:       make(map[string]*runBroadcast),
		historyCap: cap,
	}
}

// Start registers a run as active in the broadcaster. This must be called
// before Publish so that IsActive returns true for the run. Safe to call
// multiple times; subsequent calls are no-ops if the run is already registered.
func (b *Broadcaster) Start(runID string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if _, ok := b.runs[runID]; !ok {
		b.runs[runID] = &runBroadcast{}
	}
}

// Subscribe registers a new subscriber for the given run ID.
// Returns a Subscriber whose channel receives log lines.
// The channel is closed when the run completes or Finish is called.
// If the run is already finished, returns a subscriber with a closed channel.
func (b *Broadcaster) Subscribe(runID string) *Subscriber {
	b.mu.Lock()
	defer b.mu.Unlock()

	rb, ok := b.runs[runID]
	if !ok {
		rb = &runBroadcast{}
		b.runs[runID] = rb
	}

	sub := newSubscriber(256)
	if rb.done {
		sub.close()
		return sub
	}
	rb.subscribers = append(rb.subscribers, sub)
	return sub
}

// SubscribeWithHistory registers a subscriber and returns a snapshot of all
// lines published so far. The snapshot and channel are gap-free: history
// contains everything before the subscription point, and the channel
// receives everything after. No duplicates.
func (b *Broadcaster) SubscribeWithHistory(runID string) (*Subscriber, []LogLine) {
	b.mu.Lock()
	defer b.mu.Unlock()

	rb, ok := b.runs[runID]
	if !ok {
		rb = &runBroadcast{}
		b.runs[runID] = rb
	}

	history := make([]LogLine, len(rb.history))
	copy(history, rb.history)

	sub := newSubscriber(256)
	if rb.done {
		sub.close()
		return sub, history
	}
	rb.subscribers = append(rb.subscribers, sub)
	return sub, history
}

// SubscribeFromSeq registers a subscriber and returns the lines published
// after sequence number afterSeq that are still within the retained
// history window, along with the sequence number of the first returned
// line (or, if none are returned, the sequence number the next line —
// whether from the returned slice or the subscriber's channel — will
// carry). Every line delivered after via the channel continues that same
// sequence, one per line, since Publish appends to history and fans out to
// subscribers atomically under the same lock.
//
// ok is false when afterSeq predates the retained window (its lines have
// already been evicted by the history cap) — the caller cannot resume
// gap-free and should fall back to a full resync (see handleAPIStream's
// "reset" SSE event) rather than presenting a silently incomplete stream.
// Pass afterSeq -1 to request everything currently retained.
func (b *Broadcaster) SubscribeFromSeq(runID string, afterSeq int) (sub *Subscriber, lines []LogLine, baseSeq int, ok bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	rb, exists := b.runs[runID]
	if !exists {
		rb = &runBroadcast{}
		b.runs[runID] = rb
	}

	gapFree := afterSeq+1 >= rb.historyBase
	startIdx := afterSeq + 1 - rb.historyBase
	if startIdx < 0 {
		startIdx = 0
	}
	if startIdx < len(rb.history) {
		lines = make([]LogLine, len(rb.history)-startIdx)
		copy(lines, rb.history[startIdx:])
	}
	baseSeq = rb.historyBase + startIdx

	sub = newSubscriber(256)
	if rb.done {
		sub.close()
		return sub, lines, baseSeq, gapFree
	}
	rb.subscribers = append(rb.subscribers, sub)
	return sub, lines, baseSeq, gapFree
}

// Unsubscribe removes a subscriber. Safe to call multiple times.
func (b *Broadcaster) Unsubscribe(runID string, sub *Subscriber) {
	b.mu.Lock()
	defer b.mu.Unlock()

	rb, ok := b.runs[runID]
	if !ok {
		return
	}

	for i, s := range rb.subscribers {
		if s == sub {
			rb.subscribers = append(rb.subscribers[:i], rb.subscribers[i+1:]...)
			break
		}
	}
	sub.close()
}

// Publish sends a log line to all subscribers of the given run.
// Non-blocking: if a subscriber's buffer is full, the line is dropped for that subscriber.
func (b *Broadcaster) Publish(runID string, line LogLine) {
	b.mu.Lock()
	defer b.mu.Unlock()

	rb, ok := b.runs[runID]
	if !ok {
		return
	}

	rb.nextSeq++
	rb.history = append(rb.history, line)
	historyCap := b.historyCap
	if historyCap <= 0 {
		historyCap = DefaultHistoryCap
	}
	if len(rb.history) > historyCap {
		evict := len(rb.history) - historyCap
		// Zero the evicted slots before reslicing: history[evict:] keeps
		// the same backing array, so without this the evicted LogLines'
		// (often large — a full Claude stream-JSON line) Content strings
		// would stay reachable through it and never be freed, defeating
		// the cap's whole point of bounding memory.
		for i := range rb.history[:evict] {
			rb.history[i] = LogLine{}
		}
		rb.history = rb.history[evict:]
		rb.historyBase += evict
	}

	for _, sub := range rb.subscribers {
		select {
		case sub.ch <- line:
		default:
			// drop if subscriber is slow
		}
	}
}

// Finish marks the run as complete and closes all subscriber channels.
// Future Subscribe calls for this run will return a closed channel.
func (b *Broadcaster) Finish(runID string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	rb, ok := b.runs[runID]
	if !ok {
		return
	}

	rb.done = true
	for _, sub := range rb.subscribers {
		sub.close()
	}
	rb.subscribers = nil
	rb.history = nil // free memory; completed runs serve from full.log
}

// GetHistory returns a snapshot of all log lines published for a run so far.
// Returns nil if the run is unknown or its history has been cleared.
func (b *Broadcaster) GetHistory(runID string) []LogLine {
	b.mu.Lock()
	defer b.mu.Unlock()

	rb, ok := b.runs[runID]
	if !ok || len(rb.history) == 0 {
		return nil
	}
	history := make([]LogLine, len(rb.history))
	copy(history, rb.history)
	return history
}

// IsActive reports whether the given run has an active broadcast (not finished).
func (b *Broadcaster) IsActive(runID string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	rb, ok := b.runs[runID]
	if !ok {
		return false
	}
	return !rb.done
}
