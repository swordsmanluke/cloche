// Package help implements the daemon-side help channel: routing agent
// questions to configured HelpChannels, blocking the asking call until a
// reply arrives (or, in a later phase, parking the run), and serving replies
// back through the ReplySink interface.
package help

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/ports"
)

// DefaultParkAfter is the default grace period before a blocked ask is parked.
const DefaultParkAfter = 5 * time.Minute

// ParkFunc is invoked when a blocked ask's park_after grace period expires
// with no reply. The implementation is responsible for recording the park
// (marking the run parked, tearing down its container) and should return
// quickly — teardown may continue asynchronously. Errors are the
// implementation's concern to log; Ask has no way to surface them since the
// caller (agent/script) is about to be torn down regardless.
type ParkFunc func(ctx context.Context, runID, threadID, title string)

// DefaultRetention is how long archived threads are kept before the daily
// sweep deletes them (30 days).
const DefaultRetention = 30 * 24 * time.Hour

// AskParams groups the inputs to Router.Ask.
type AskParams struct {
	TaskID    string
	AttemptID string
	RunID     string
	StepName  string
	Channel   string // cloche-side channel, e.g. project name
	Question  string
	Title     string // optional; defaults to first line of Question
	Options   []string
	ThreadID  string // optional; continue an existing thread
	AskKey    string // optional idempotency key (reserved for phase 2 replay)
	// NoPark disables parking for this ask: it blocks in place until replied
	// or ctx is cancelled (e.g. by the step's own timeout), instead of
	// parking after parkAfter. For steps that cannot replay safely on resume.
	NoPark bool
}

// AskResult is returned once a reply arrives.
type AskResult struct {
	Answer   string
	ThreadID string
	// Parked is always false in this phase; reserved for phase 2.
	Parked bool
}

type pendingAsk struct {
	threadID string
	replyCh  chan string
}

// Router owns the HelpStore, fans out agent messages to configured
// HelpChannels, accepts replies via ReplySink, and blocks Ask calls in place
// until a reply lands. It enforces one open ask per run.
type Router struct {
	store     ports.HelpStore
	channels  []ports.HelpChannel
	parkAfter time.Duration
	parkFunc  ParkFunc

	mu      sync.Mutex
	pending map[string]*pendingAsk // keyed by RunID
}

// SetParkFunc configures the callback invoked when an ask parks. Must be
// called before any Ask that might park; nil (the default) disables parking
// and Ask blocks past park_after indefinitely, matching phase 1 behavior.
func (r *Router) SetParkFunc(f ParkFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.parkFunc = f
}

// NewRouter constructs a Router. parkAfter <= 0 uses DefaultParkAfter.
func NewRouter(store ports.HelpStore, parkAfter time.Duration, channels ...ports.HelpChannel) *Router {
	if parkAfter <= 0 {
		parkAfter = DefaultParkAfter
	}
	return &Router{
		store:     store,
		channels:  channels,
		parkAfter: parkAfter,
		pending:   make(map[string]*pendingAsk),
	}
}

// Ask opens (or continues) a thread with a new agent question and blocks
// until a user reply arrives or ctx is cancelled. Only one ask may be open
// per RunID at a time; a concurrent second ask is rejected immediately.
func (r *Router) Ask(ctx context.Context, p AskParams) (AskResult, error) {
	if p.RunID == "" {
		return AskResult{}, fmt.Errorf("help: ask requires a run id")
	}
	if strings.TrimSpace(p.Question) == "" {
		return AskResult{}, fmt.Errorf("help: question must not be empty")
	}

	// Idempotent replay: a generic step that re-runs from scratch after a
	// park+resume re-issues the same ask. If this exact ask (identified by
	// ask_key, or a hash of the question body when no key is given) already
	// received a user reply on this run, return it instantly instead of
	// re-blocking or re-posting to channels.
	effectiveKey := p.AskKey
	if effectiveKey == "" {
		effectiveKey = hashAskBody(p.Question)
	}
	if answer, threadID, found, err := r.store.FindAskAnswer(ctx, p.RunID, effectiveKey); err == nil && found {
		return AskResult{Answer: answer, ThreadID: threadID}, nil
	}

	r.mu.Lock()
	if _, exists := r.pending[p.RunID]; exists {
		r.mu.Unlock()
		return AskResult{}, fmt.Errorf("run %s already has an open ask pending; wait for the reply or fold your question into it", p.RunID)
	}
	r.mu.Unlock()

	thread, err := r.resolveOrCreateThread(ctx, p)
	if err != nil {
		return AskResult{}, err
	}

	msg := &domain.HelpMessage{
		ID:        domain.GenerateHelpMessageID(),
		ThreadID:  thread.ID,
		Author:    domain.MessageAuthorAgent,
		Body:      p.Question,
		Options:   p.Options,
		AskKey:    effectiveKey,
		CreatedAt: time.Now(),
	}
	if err := r.store.AppendMessage(ctx, msg); err != nil {
		return AskResult{}, fmt.Errorf("help: append message: %w", err)
	}
	if err := r.store.SetThreadState(ctx, thread.ID, domain.ThreadStateAwaitingUser); err != nil {
		return AskResult{}, fmt.Errorf("help: set thread state: %w", err)
	}

	replyCh := make(chan string, 1)
	r.mu.Lock()
	r.pending[p.RunID] = &pendingAsk{threadID: thread.ID, replyCh: replyCh}
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.pending, p.RunID)
		r.mu.Unlock()
	}()

	r.postToChannels(*thread, *msg)

	// NoPark steps (per-step "cannot replay safely" config) disable the park
	// timer entirely: a nil channel never fires in a select, so the ask just
	// blocks in place until replied or ctx is cancelled (e.g. the step's own
	// timeout).
	var parkC <-chan time.Time
	if !p.NoPark {
		parkTimer := time.NewTimer(r.parkAfter)
		defer parkTimer.Stop()
		parkC = parkTimer.C
	}

	for {
		select {
		case body := <-replyCh:
			return AskResult{Answer: body, ThreadID: thread.ID}, nil
		case <-parkC:
			r.mu.Lock()
			parkFunc := r.parkFunc
			r.mu.Unlock()
			if parkFunc == nil {
				// No park callback configured: keep blocking past park_after.
				continue
			}
			parkFunc(context.Background(), p.RunID, thread.ID, thread.Title)
			return AskResult{ThreadID: thread.ID, Parked: true}, nil
		case <-ctx.Done():
			return AskResult{}, ctx.Err()
		}
	}
}

// Reply implements ports.ReplySink. It appends a user message to a thread
// and, if a run is blocked waiting on that thread, unblocks it.
func (r *Router) Reply(ctx context.Context, threadID string, body string, via string) error {
	thread, _, err := r.store.GetThread(ctx, threadID)
	if err != nil {
		return err
	}

	msg := &domain.HelpMessage{
		ID:        domain.GenerateHelpMessageID(),
		ThreadID:  thread.ID,
		Author:    domain.MessageAuthorUser,
		Body:      body,
		CreatedAt: time.Now(),
	}
	if err := r.store.AppendMessage(ctx, msg); err != nil {
		return fmt.Errorf("help: append reply: %w", err)
	}
	if err := r.store.SetThreadState(ctx, thread.ID, domain.ThreadStateAwaitingAgent); err != nil {
		return fmt.Errorf("help: set thread state: %w", err)
	}

	r.mu.Lock()
	for runID, pa := range r.pending {
		if pa.threadID == thread.ID {
			select {
			case pa.replyCh <- body:
			default:
			}
			delete(r.pending, runID)
			break
		}
	}
	r.mu.Unlock()

	return nil
}

// ReplyByAddress resolves a "<channel>/<name>" address, bare name, or raw
// thread ID and appends a reply to it.
func (r *Router) ReplyByAddress(ctx context.Context, address string, body string, via string) error {
	thread, err := r.store.ResolveThread(ctx, address)
	if err != nil {
		return err
	}
	return r.Reply(ctx, thread.ID, body, via)
}

// ListThreads lists threads, optionally filtered by channel / including archived.
func (r *Router) ListThreads(ctx context.Context, filter ports.HelpThreadFilter) ([]*domain.HelpThread, error) {
	return r.store.ListThreads(ctx, filter)
}

// GetThread resolves an address and returns the thread with its full transcript.
func (r *Router) GetThread(ctx context.Context, address string) (*domain.HelpThread, []domain.HelpMessage, error) {
	thread, err := r.store.ResolveThread(ctx, address)
	if err != nil {
		return nil, nil, err
	}
	return r.store.GetThread(ctx, thread.ID)
}

// ArchiveTaskThreads moves all of a task's open threads to archived. Called
// when the owning task completes successfully.
func (r *Router) ArchiveTaskThreads(ctx context.Context, taskID string) (int64, error) {
	return r.store.ArchiveThreadsByTask(ctx, taskID)
}

// SweepRetention deletes archived threads older than retention.
func (r *Router) SweepRetention(ctx context.Context, retention time.Duration) (int64, error) {
	cutoff := time.Now().Add(-retention)
	return r.store.DeleteArchivedThreadsOlderThan(ctx, cutoff)
}

func (r *Router) postToChannels(thread domain.HelpThread, msg domain.HelpMessage) {
	for _, ch := range r.channels {
		go func(c ports.HelpChannel) {
			// Errors are logged by the channel implementation itself; a
			// failing channel must not affect other channels or the CLI inbox.
			_ = c.Post(context.Background(), thread, msg)
		}(ch)
	}
}

func (r *Router) resolveOrCreateThread(ctx context.Context, p AskParams) (*domain.HelpThread, error) {
	if p.ThreadID != "" {
		thread, _, err := r.store.GetThread(ctx, p.ThreadID)
		if err != nil {
			return nil, fmt.Errorf("help: continue thread %s: %w", p.ThreadID, err)
		}
		return thread, nil
	}

	title := p.Title
	if title == "" {
		title = firstLine(p.Question)
	}
	channel := p.Channel
	if channel == "" {
		channel = "cloche"
	}

	slug := slugify(title)
	if slug == "" {
		slug = "thread"
	}
	count, err := r.store.CountThreadsWithNamePrefix(ctx, channel, slug)
	if err != nil {
		return nil, fmt.Errorf("help: allocate thread name: %w", err)
	}

	now := time.Now()
	// Retry on name collision (concurrent asks allocating the same slug).
	for attempt := 0; attempt < 20; attempt++ {
		name := fmt.Sprintf("%s-%d", slug, count+1+attempt)
		thread := &domain.HelpThread{
			ID:        domain.GenerateHelpThreadID(),
			Channel:   channel,
			Name:      name,
			TaskID:    p.TaskID,
			AttemptID: p.AttemptID,
			RunID:     p.RunID,
			StepName:  p.StepName,
			Title:     title,
			State:     domain.ThreadStateAwaitingUser,
			CreatedAt: now,
			UpdatedAt: now,
		}
		err := r.store.CreateThread(ctx, thread)
		if err == nil {
			return thread, nil
		}
		if !isUniqueConstraintErr(err) {
			return nil, fmt.Errorf("help: create thread: %w", err)
		}
	}
	return nil, fmt.Errorf("help: could not allocate a unique thread name for %q after repeated attempts", slug)
}

// hashAskBody returns a deterministic idempotency key for an ask that didn't
// supply an explicit ask_key, so replay detection still works.
func hashAskBody(question string) string {
	sum := sha256.Sum256([]byte(question))
	return "body:" + hex.EncodeToString(sum[:])
}

func isUniqueConstraintErr(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "UNIQUE constraint")
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

var slugNonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

// slugify lowercases, strips non-alphanumeric runs to single hyphens, and
// truncates to a reasonable thread-name length.
func slugify(s string) string {
	s = strings.ToLower(s)
	s = slugNonAlnum.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	const maxLen = 40
	if len(s) > maxLen {
		s = strings.Trim(s[:maxLen], "-")
	}
	return s
}
