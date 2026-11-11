// Package budget enforces the daemon-wide daily usage cap (see
// docs/plans/2026-11-10-autonomous-loop-design.md, Part 1).
//
// Usage is measured by reading the subscription's weekly usage percentage
// (the figure behind Claude Code's /usage; see
// docs/plans/spikes/2026-11-usage-allowance.md). That figure is a running
// weekly total, so "used today" is the difference from a reading taken at the
// start of the (local) day. When used-today reaches the cap the Monitor is
// "over": the daemon launches nothing new, and runs already in flight get the
// grace period before they are parked. Launching resumes by itself when the
// day rolls over or the usage figure drops back under the cap.
package budget

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultGrace is how long runs in flight may continue once the cap is hit.
const DefaultGrace = 30 * time.Minute

// DefaultInterval is how often the Monitor re-reads usage in the background.
const DefaultInterval = 2 * time.Minute

// Config is the resolved [budget] configuration.
type Config struct {
	// UsagePercentPerDay is the cap: the share of the weekly allowance that
	// may be used per day. Zero or negative disables the budget.
	UsagePercentPerDay float64
	// Grace is how long in-flight runs may continue once the cap is hit.
	Grace time.Duration
	// EmergencyOverflow is read by the emergency-launch work: true lets
	// emergency tasks launch past the cap. The Monitor itself never reads it.
	EmergencyOverflow bool
}

// Enabled reports whether a cap is configured.
func (c Config) Enabled() bool { return c.UsagePercentPerDay > 0 }

// Reading is one measurement of the weekly usage allowance.
type Reading struct {
	// WeeklyPercent is the share of the weekly allowance used, 0-100.
	WeeklyPercent float64
	// ResetsAt is when the weekly window resets; zero when it couldn't be parsed.
	ResetsAt time.Time
}

// Reader measures current usage.
type Reader interface {
	Read(ctx context.Context) (Reading, error)
}

// weeklyAnyRe matches every weekly window, e.g. "Current week (Fable): 41% used".
var weeklyAnyRe = regexp.MustCompile(`Current week \([^)]*\):\s*(\d+(?:\.\d+)?)%\s*used`)

var weeklyAllRe = regexp.MustCompile(`Current week \(all models\):\s*(\d+(?:\.\d+)?)%\s*used(?:\s*·\s*resets\s*([^\n]+))?`)

// ParseUsage extracts the weekly reading from the text
// printed by `claude -p "/usage"`. The format is free-form text, so this is
// deliberately strict about the percentage (an error, never a guess) and
// lenient about the reset time (zero when unparseable).
func ParseUsage(text string, now time.Time) (Reading, error) {
	m := weeklyAllRe.FindStringSubmatch(text)
	if m == nil {
		return Reading{}, fmt.Errorf("no 'Current week (all models)' line in usage output")
	}
	pct, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return Reading{}, fmt.Errorf("parsing usage percentage %q: %w", m[1], err)
	}
	// A per-model weekly window can run out before the all-models one, so
	// the reading is the fullest window.
	for _, w := range weeklyAnyRe.FindAllStringSubmatch(text, -1) {
		if p, err := strconv.ParseFloat(w[1], 64); err == nil && p > pct {
			pct = p
		}
	}
	r := Reading{WeeklyPercent: pct}
	if len(m) > 2 {
		r.ResetsAt = parseReset(strings.TrimSpace(m[2]), now)
	}
	return r, nil
}

// parseReset understands "Nov 11, 12:59am (UTC)". The year is inferred as the
// next occurrence at or after now.
func parseReset(s string, now time.Time) time.Time {
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "(UTC)"))
	for _, layout := range []string{"Jan 2, 3:04pm", "Jan 2, 3pm"} {
		t, err := time.ParseInLocation(layout, s, time.UTC)
		if err != nil {
			continue
		}
		t = time.Date(now.UTC().Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), 0, 0, time.UTC)
		if t.Before(now.Add(-24 * time.Hour)) {
			t = t.AddDate(1, 0, 0)
		}
		return t
	}
	return time.Time{}
}

// ClaudeReader reads usage by running `claude -p "/usage"`: about a second,
// no model call, no tokens.
type ClaudeReader struct {
	// Command overrides the command line; empty uses `claude -p /usage`.
	Command []string
	Now     func() time.Time
}

// Read implements Reader.
func (c *ClaudeReader) Read(ctx context.Context) (Reading, error) {
	cmd := c.Command
	if len(cmd) == 0 {
		cmd = []string{"claude", "-p", "/usage"}
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, cmd[0], cmd[1:]...).Output()
	if err != nil {
		return Reading{}, fmt.Errorf("running %s: %w", strings.Join(cmd, " "), err)
	}
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	return ParseUsage(string(out), now())
}

// Status is a snapshot of the budget for display and gating.
type Status struct {
	Enabled     bool
	CapPercent  float64
	UsedPercent float64 // used today, in percentage points of the weekly allowance
	Over        bool
	OverSince   time.Time
	// ClearsAt is when the day's window rolls over and the baseline resets.
	ClearsAt time.Time
	// GraceEndsAt is when in-flight runs get parked; zero when not over.
	GraceEndsAt time.Time
	// GraceExpired is true once the grace period elapsed and runs were parked.
	GraceExpired bool
	CheckedAt    time.Time
	// ReadErr is the last read failure, empty when the last read succeeded.
	// A failed read never closes the gate: the previous state is kept.
	ReadErr string
}

type persisted struct {
	Day             string  `json:"day"`
	BaselinePercent float64 `json:"baseline_percent"`
}

// Monitor tracks daily usage against the cap.
type Monitor struct {
	cfg    Config
	reader Reader
	now    func() time.Time
	path   string // baseline state file; empty disables persistence

	// onGraceExpired is called once per over-cap episode when the grace
	// period ends with the budget still exceeded.
	onGraceExpired func()

	mu       sync.Mutex
	st       Status
	base     persisted
	lastPct  float64
	timer    *time.Timer
	timerGen int
}

// NewMonitor builds a Monitor. statePath, when non-empty, persists the day's
// baseline so a daemon restart doesn't forget what was used earlier today.
func NewMonitor(cfg Config, reader Reader, statePath string) *Monitor {
	if cfg.Grace <= 0 {
		cfg.Grace = DefaultGrace
	}
	m := &Monitor{cfg: cfg, reader: reader, now: time.Now, path: statePath}
	m.st.Enabled = cfg.Enabled()
	m.st.CapPercent = cfg.UsagePercentPerDay
	m.loadBase()
	return m
}

// SetNow overrides the clock (tests).
func (m *Monitor) SetNow(now func() time.Time) { m.now = now }

// SetOnGraceExpired registers the callback that parks runs in flight.
func (m *Monitor) SetOnGraceExpired(fn func()) {
	m.mu.Lock()
	m.onGraceExpired = fn
	m.mu.Unlock()
}

// Config returns the resolved configuration.
func (m *Monitor) Config() Config { return m.cfg }

func (m *Monitor) loadBase() {
	if m.path == "" {
		return
	}
	data, err := os.ReadFile(m.path)
	if err != nil {
		return
	}
	var p persisted
	if json.Unmarshal(data, &p) == nil {
		m.base = p
	}
}

func (m *Monitor) saveBase() {
	if m.path == "" {
		return
	}
	data, _ := json.Marshal(m.base)
	if err := os.MkdirAll(filepath.Dir(m.path), 0o755); err == nil {
		_ = os.WriteFile(m.path, data, 0o644)
	}
}

func dayKey(t time.Time) string { return t.Format("2006-01-02") }

func nextMidnight(t time.Time) time.Time {
	y, mo, d := t.Date()
	return time.Date(y, mo, d+1, 0, 0, 0, 0, t.Location())
}

// Check reads usage and updates the gate. A read error keeps the previous
// state (fail open on first failure, since nothing is known) and is reported
// in Status.ReadErr.
func (m *Monitor) Check(ctx context.Context) Status {
	if !m.cfg.Enabled() || m.reader == nil {
		return m.Status()
	}
	rd, err := m.reader.Read(ctx)
	now := m.now()

	m.mu.Lock()
	defer m.mu.Unlock()
	m.st.CheckedAt = now
	if err != nil {
		m.st.ReadErr = err.Error()
		log.Printf("budget: reading usage failed: %v", err)
		return m.st
	}
	m.st.ReadErr = ""

	day := dayKey(now)
	switch {
	case m.base.Day != day:
		// First reading of a new day: everything so far counts as yesterday's.
		m.base = persisted{Day: day, BaselinePercent: rd.WeeklyPercent}
		m.saveBase()
	case rd.WeeklyPercent < m.base.BaselinePercent:
		// The weekly window reset mid-day; the running total restarted at 0.
		m.base.BaselinePercent = 0
		m.saveBase()
	}
	m.lastPct = rd.WeeklyPercent

	used := rd.WeeklyPercent - m.base.BaselinePercent
	m.st.UsedPercent = used
	m.st.ClearsAt = nextMidnight(now)
	over := used >= m.cfg.UsagePercentPerDay

	switch {
	case over && !m.st.Over:
		m.st.Over = true
		m.st.OverSince = now
		m.st.GraceEndsAt = now.Add(m.cfg.Grace)
		m.st.GraceExpired = false
		m.armTimerLocked(m.cfg.Grace)
		log.Printf("budget: daily cap reached (%.1f%% of %.1f%%); no new launches, in-flight runs parked in %s", used, m.cfg.UsagePercentPerDay, m.cfg.Grace)
	case !over && m.st.Over:
		m.st.Over = false
		m.st.OverSince = time.Time{}
		m.st.GraceEndsAt = time.Time{}
		m.st.GraceExpired = false
		m.stopTimerLocked()
		log.Printf("budget: usage back under the daily cap (%.1f%% of %.1f%%); launching resumed", used, m.cfg.UsagePercentPerDay)
	}
	return m.st
}

func (m *Monitor) stopTimerLocked() {
	m.timerGen++
	if m.timer != nil {
		m.timer.Stop()
		m.timer = nil
	}
}

func (m *Monitor) armTimerLocked(d time.Duration) {
	m.stopTimerLocked()
	gen := m.timerGen
	m.timer = time.AfterFunc(d, func() { m.graceExpired(gen) })
}

func (m *Monitor) graceExpired(gen int) {
	m.mu.Lock()
	if gen != m.timerGen || !m.st.Over || m.st.GraceExpired {
		m.mu.Unlock()
		return
	}
	m.st.GraceExpired = true
	fn := m.onGraceExpired
	m.mu.Unlock()
	log.Printf("budget: grace period over; parking runs in flight")
	if fn != nil {
		fn()
	}
}

// Status returns the last known state without reading usage.
func (m *Monitor) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.st
	if st.Enabled && !st.CheckedAt.IsZero() {
		st.ClearsAt = nextMidnight(m.now())
		if st.Over && !m.now().Before(st.ClearsAt) {
			// The day rolled over since the last read; the next Check will
			// reset the baseline. Report the window as clearing now.
			st.ClearsAt = m.now()
		}
	}
	return st
}

// Blocked reports whether new launches must be held back, from the last
// reading Run took. It never reads usage itself: a read takes seconds, and
// callers (the loops, `cloche status`) must not wait on it.
func (m *Monitor) Blocked() bool {
	return m.cfg.Enabled() && m.Status().Over
}

// Run re-reads usage every interval until ctx is cancelled.
func (m *Monitor) Run(ctx context.Context, interval time.Duration) {
	if !m.cfg.Enabled() {
		return
	}
	if interval <= 0 {
		interval = DefaultInterval
	}
	m.Check(ctx)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			m.mu.Lock()
			m.stopTimerLocked()
			m.mu.Unlock()
			return
		case <-t.C:
			m.Check(ctx)
		}
	}
}

// Describe renders the one-line "Budget:" status text.
func (st Status) Describe(now time.Time) string {
	if !st.Enabled {
		return ""
	}
	switch {
	case st.Over && st.GraceExpired:
		return fmt.Sprintf("OVER CAP — %.1f%% of %.0f%% daily cap used; launches paused, in-flight runs parked; clears %s", st.UsedPercent, st.CapPercent, clearsText(st.ClearsAt, now))
	case st.Over:
		return fmt.Sprintf("OVER CAP — %.1f%% of %.0f%% daily cap used; launches paused, in-flight runs parked at %s; clears %s", st.UsedPercent, st.CapPercent, st.GraceEndsAt.Format("15:04"), clearsText(st.ClearsAt, now))
	case st.ReadErr != "" && st.CheckedAt.IsZero():
		return fmt.Sprintf("%.0f%% daily cap, usage unknown (%s)", st.CapPercent, st.ReadErr)
	default:
		s := fmt.Sprintf("%.1f%% of %.0f%% daily cap used today", st.UsedPercent, st.CapPercent)
		if st.ReadErr != "" {
			s += " (last read failed: " + st.ReadErr + ")"
		}
		return s
	}
}

func clearsText(t, now time.Time) string {
	if t.IsZero() {
		return "at the next day boundary"
	}
	d := t.Sub(now).Round(time.Minute)
	if d <= 0 {
		return "now"
	}
	return fmt.Sprintf("at %s (in %s)", t.Format("15:04"), d)
}
