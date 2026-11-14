package dra

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Wake-up triggers (autonomous-loop design, Part 5 "It isn't a running
// process" and "Domains are labels"; ruling CLOCHE-A123). The daemon turns
// four kinds of event into a wake-up of the owning DRA:
//
//   - a task in its domain finishes, fails or parks,
//   - a signal arrives naming its domain,
//   - a wait it registered ends (its poll is ready, failed or timed out),
//   - a heartbeat (a poll-step timer calls `cloche dra heartbeat`).
//
// An event never starts a run by itself. It goes into the DRA's Inbox; the
// daemon drains the inbox into ONE wake-up message whenever the DRA is idle
// and the budget gate is open. Events that arrive while the DRA is awake or
// the gate is closed stay in the inbox and ride along in the next wake-up.

// Event kinds.
const (
	KindTaskFinished = "task-finished"
	KindTaskFailed   = "task-failed"
	KindTaskParked   = "task-parked"
	KindSignal       = "signal"
	KindWait         = "wait"
	KindHeartbeat    = "heartbeat"
)

// Wake request protocol for non-wake triggers over the RunWorkflow RPC:
// WorkflowName is NotifyRequest, Env carries WakeEnvDRA, NotifyEnvKind
// (KindHeartbeat), WakeEnvMessage and NotifyEnvSubject.
const (
	NotifyRequest    = "dra-notify"
	NotifyEnvKind    = "kind"
	NotifyEnvSubject = "subject"
)

// DefaultHeartbeat is how often a DRA is woken when nothing else happens.
const DefaultHeartbeat = 4 * time.Hour

// Event is one thing that happened that a DRA should hear about.
type Event struct {
	Kind string    `json:"kind"`
	At   time.Time `json:"at"`
	// Subject identifies what the event is about: a task id, a signal
	// "source/key", a wait id. Empty for a heartbeat.
	Subject string `json:"subject,omitempty"`
	// Text is the human-readable detail.
	Text string `json:"text,omitempty"`
}

// DomainOf returns the DRA that owns a task with these labels: a label equal
// to a DRA's name makes the task that DRA's. One label per task: with two
// matching labels the first (in label order) wins and the rest are returned
// as ignored so the caller can log them. owner is "" when no label matches.
func DomainOf(labels, dras []string) (owner string, ignored []string) {
	known := make(map[string]bool, len(dras))
	for _, d := range dras {
		known[d] = true
	}
	for _, l := range labels {
		if !known[l] {
			continue
		}
		if owner == "" {
			owner = l
		} else if l != owner {
			ignored = append(ignored, l)
		}
	}
	return owner, ignored
}

// ValidNames lists the names of the project's valid DRAs.
func ValidNames(projectDir string) []string {
	ds, _ := List(projectDir)
	var out []string
	for _, d := range ds {
		if d.Valid() {
			out = append(out, d.Name)
		}
	}
	return out
}

// HeartbeatDue reports whether a heartbeat should wake the DRA: it has not
// been woken (for any reason) for a full interval. interval <= 0 disables
// heartbeats. A DRA that has never been woken is always due.
func HeartbeatDue(lastWake, now time.Time, interval time.Duration) bool {
	if interval <= 0 {
		return false
	}
	return lastWake.IsZero() || now.Sub(lastWake) >= interval
}

// FormatMessage renders pending events as the one wake-up message.
func FormatMessage(name string, events []Event) string {
	var b strings.Builder
	if len(events) == 1 {
		fmt.Fprintf(&b, "You were woken up for the %q domain by 1 event:\n\n", name)
	} else {
		fmt.Fprintf(&b, "You were woken up for the %q domain by %d events, delivered together:\n\n", name, len(events))
	}
	for _, e := range events {
		fmt.Fprintf(&b, "- [%s] %s: %s\n", e.At.Format(time.RFC3339), e.Kind, e.line())
	}
	return strings.TrimRight(b.String(), "\n")
}

func (e Event) line() string {
	switch {
	case e.Subject != "" && e.Text != "":
		return e.Subject + " - " + e.Text
	case e.Subject != "":
		return e.Subject
	case e.Text != "":
		return e.Text
	case e.Kind == KindHeartbeat:
		return "periodic check-in: nothing specific happened; review your domain"
	}
	return "(no detail)"
}

// Inbox holds each DRA's undelivered events, per project. It is persisted
// to .cloche/runs/dra-events.json (runtime state) so a daemon restart does
// not drop events.
type Inbox struct {
	mu      sync.Mutex
	pending map[string][]Event // key(project, dra) -> events, oldest first
	now     func() time.Time
}

// NewInbox returns an empty inbox.
func NewInbox() *Inbox {
	return &Inbox{pending: map[string][]Event{}, now: time.Now}
}

// SetClock overrides the clock (tests).
func (in *Inbox) SetClock(now func() time.Time) { in.now = now }

func inboxKey(projectDir, name string) string { return projectDir + "\x00" + name }

// EventsPath is the persisted inbox of a project.
func EventsPath(projectDir string) string {
	return filepath.Join(projectDir, ".cloche", "runs", "dra-events.json")
}

// Add queues an event. A heartbeat already pending is not queued twice (a
// poll step that fires on every tick must not repeat it). It reports whether
// the event was queued.
func (in *Inbox) Add(projectDir, name string, e Event) bool {
	in.mu.Lock()
	defer in.mu.Unlock()
	if e.At.IsZero() {
		e.At = in.now()
	}
	k := inboxKey(projectDir, name)
	if e.Kind == KindHeartbeat {
		for _, p := range in.pending[k] {
			if p.Kind == e.Kind && p.Subject == e.Subject {
				return false
			}
		}
	}
	in.pending[k] = append(in.pending[k], e)
	in.saveLocked(projectDir)
	return true
}

// Take removes and returns every pending event of the DRA, oldest first.
func (in *Inbox) Take(projectDir, name string) []Event {
	in.mu.Lock()
	defer in.mu.Unlock()
	k := inboxKey(projectDir, name)
	ev := in.pending[k]
	if len(ev) == 0 {
		return nil
	}
	delete(in.pending, k)
	in.saveLocked(projectDir)
	return ev
}

// Requeue puts events that could not be delivered back at the front of the
// queue, ahead of anything that arrived meanwhile.
func (in *Inbox) Requeue(projectDir, name string, events []Event) {
	if len(events) == 0 {
		return
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	k := inboxKey(projectDir, name)
	in.pending[k] = append(append([]Event{}, events...), in.pending[k]...)
	in.saveLocked(projectDir)
}

// Pending is how many events wait for the DRA.
func (in *Inbox) Pending(projectDir, name string) int {
	in.mu.Lock()
	defer in.mu.Unlock()
	return len(in.pending[inboxKey(projectDir, name)])
}

// Load restores the persisted events of a project.
func (in *Inbox) Load(projectDir string) error {
	data, err := os.ReadFile(EventsPath(projectDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var m map[string][]Event
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	for name, ev := range m {
		k := inboxKey(projectDir, name)
		in.pending[k] = append(append([]Event{}, ev...), in.pending[k]...)
	}
	return nil
}

// Projects returns the (project, DRA) pairs with pending events.
func (in *Inbox) Projects() [][2]string {
	in.mu.Lock()
	defer in.mu.Unlock()
	var out [][2]string
	for k, ev := range in.pending {
		if len(ev) == 0 {
			continue
		}
		p, n, _ := strings.Cut(k, "\x00")
		out = append(out, [2]string{p, n})
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0]+out[i][1] < out[j][0]+out[j][1] })
	return out
}

// saveLocked writes the project's events; best effort (the runs dir may not
// exist for an unregistered project).
func (in *Inbox) saveLocked(projectDir string) {
	m := map[string][]Event{}
	prefix := projectDir + "\x00"
	for k, ev := range in.pending {
		if strings.HasPrefix(k, prefix) && len(ev) > 0 {
			m[strings.TrimPrefix(k, prefix)] = ev
		}
	}
	path := EventsPath(projectDir)
	if len(m) == 0 {
		_ = os.Remove(path)
		return
	}
	data, err := json.Marshal(m)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o644)
}
