package logstream

import (
	"sync"
)

// LogLine is a single log entry broadcast to subscribers.
type LogLine struct {
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"`               // "status", "script", "llm"
	Content   string `json:"content"`             // the log message
	StepName  string `json:"step_name,omitempty"` // originating step
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

// Broadcaster fans out log lines from active runs to multiple subscribers.
// Thread-safe for concurrent use.
type Broadcaster struct {
	mu   sync.Mutex
	runs map[string]*runBroadcast
}

type runBroadcast struct {
	subscribers []*Subscriber
	done        bool
}

// NewBroadcaster creates a new Broadcaster.
func NewBroadcaster() *Broadcaster {
	return &Broadcaster{
		runs: make(map[string]*runBroadcast),
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
