package budget

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

type fakeReader struct {
	pct float64
	err error
}

func (f *fakeReader) Read(context.Context) (Reading, error) {
	return Reading{WeeklyPercent: f.pct}, f.err
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newTest(t *testing.T, cap float64, grace time.Duration, r *fakeReader) (*Monitor, *clock) {
	t.Helper()
	c := &clock{t: time.Date(2026, 11, 11, 9, 0, 0, 0, time.Local)}
	m := NewMonitor(Config{UsagePercentPerDay: cap, Grace: grace}, r, filepath.Join(t.TempDir(), "budget.json"))
	m.SetNow(c.now)
	return m, c
}

func TestParseUsage(t *testing.T) {
	text := "You are currently using your subscription\n\nCurrent week (all models): 32% used · resets Nov 11, 12:59am (UTC)\nCurrent week (Fable): 40% used · resets Nov 11, 12:59am (UTC)\n"
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	r, err := ParseUsage(text, now)
	if err != nil {
		t.Fatal(err)
	}
	if r.WeeklyPercent != 40 {
		t.Errorf("percent = %v, want 40 (the fullest weekly window)", r.WeeklyPercent)
	}
	want := time.Date(2026, 11, 11, 0, 59, 0, 0, time.UTC)
	if !r.ResetsAt.Equal(want) {
		t.Errorf("resetsAt = %v, want %v", r.ResetsAt, want)
	}
	if _, err := ParseUsage("garbage", now); err == nil {
		t.Error("expected error for unparseable output")
	}
}

func TestGateClosesOverCapAndReopensUnderIt(t *testing.T) {
	r := &fakeReader{pct: 30}
	m, _ := newTest(t, 10, time.Hour, r)
	ctx := context.Background()

	if m.Check(ctx).Over {
		t.Fatal("first reading of the day is the baseline; must not be over")
	}
	r.pct = 35 // 5 used today
	if m.Check(ctx).Over || m.Blocked() {
		t.Fatal("5% of a 10% cap must not block")
	}
	r.pct = 41 // 11 used today
	st := m.Check(ctx)
	if !st.Over || !m.Blocked() {
		t.Fatalf("11%% of a 10%% cap must block: %+v", st)
	}
	if st.UsedPercent != 11 {
		t.Errorf("used = %v, want 11", st.UsedPercent)
	}
	// Weekly reset mid-day: the total drops; baseline restarts at zero.
	r.pct = 3
	if st := m.Check(ctx); st.Over || st.UsedPercent != 3 {
		t.Errorf("after weekly reset want under cap with 3%% used, got %+v", st)
	}
	if m.Blocked() {
		t.Error("launching must resume on its own once back under the cap")
	}
}

func TestNewDayResetsBaseline(t *testing.T) {
	r := &fakeReader{pct: 10}
	m, c := newTest(t, 5, time.Hour, r)
	ctx := context.Background()
	m.Check(ctx)
	r.pct = 20
	if !m.Check(ctx).Over {
		t.Fatal("expected over cap")
	}
	c.t = c.t.AddDate(0, 0, 1)
	if st := m.Check(ctx); st.Over {
		t.Fatalf("a new day must clear the cap: %+v", st)
	}
}

func TestBaselineSurvivesRestart(t *testing.T) {
	r := &fakeReader{pct: 10}
	path := filepath.Join(t.TempDir(), "b.json")
	c := &clock{t: time.Date(2026, 11, 11, 9, 0, 0, 0, time.Local)}
	m := NewMonitor(Config{UsagePercentPerDay: 5, Grace: time.Hour}, r, path)
	m.SetNow(c.now)
	m.Check(context.Background())

	r.pct = 17
	m2 := NewMonitor(Config{UsagePercentPerDay: 5, Grace: time.Hour}, r, path)
	m2.SetNow(c.now)
	if !m2.Check(context.Background()).Over {
		t.Error("restarted monitor must keep today's baseline and see 7% used")
	}
}

func TestReadErrorKeepsGateOpen(t *testing.T) {
	r := &fakeReader{err: errors.New("boom")}
	m, _ := newTest(t, 10, time.Hour, r)
	m.Check(context.Background())
	if m.Blocked() {
		t.Error("unreadable usage must not block launches")
	}
	if m.Status().ReadErr == "" {
		t.Error("read error should be surfaced in status")
	}
}

func TestDisabledNeverBlocks(t *testing.T) {
	m, _ := newTest(t, 0, time.Hour, &fakeReader{pct: 99})
	if m.Blocked() || m.Status().Enabled {
		t.Error("cap 0 disables the budget")
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}

func TestGraceTimerParksAfterGrace(t *testing.T) {
	r := &fakeReader{pct: 0}
	m, _ := newTest(t, 5, 40*time.Millisecond, r)
	var parked atomic.Int32
	m.SetOnGraceExpired(func() { parked.Add(1) })
	ctx := context.Background()
	m.Check(ctx)
	r.pct = 9
	m.Check(ctx)
	if parked.Load() != 0 {
		t.Fatal("must not park before grace elapses")
	}
	if m.Status().GraceEndsAt.IsZero() {
		t.Error("status should report when grace ends")
	}
	waitFor(t, func() bool { return parked.Load() == 1 })
	m.Check(ctx) // still over: no second park in the same episode
	time.Sleep(80 * time.Millisecond)
	if parked.Load() != 1 {
		t.Errorf("parked %d times, want 1", parked.Load())
	}
	if !m.Status().GraceExpired {
		t.Error("status should show grace expired")
	}
}

func TestGraceTimerCancelledWhenUsageDrops(t *testing.T) {
	r := &fakeReader{pct: 0}
	m, _ := newTest(t, 5, 60*time.Millisecond, r)
	var parked atomic.Int32
	m.SetOnGraceExpired(func() { parked.Add(1) })
	ctx := context.Background()
	m.Check(ctx)
	r.pct = 9
	m.Check(ctx)
	r.pct = 2 // e.g. weekly reset
	m.Check(ctx)
	time.Sleep(120 * time.Millisecond)
	if parked.Load() != 0 {
		t.Error("runs must not be parked once usage is back under the cap")
	}
}

func TestDescribe(t *testing.T) {
	now := time.Date(2026, 11, 11, 20, 0, 0, 0, time.Local)
	st := Status{Enabled: true, CapPercent: 10, UsedPercent: 12.3, Over: true, GraceEndsAt: now.Add(time.Minute), ClearsAt: nextMidnight(now)}
	got := st.Describe(now)
	for _, want := range []string{"OVER CAP", "12.3%", "10%", "00:00"} {
		if !contains(got, want) {
			t.Errorf("%q missing %q", got, want)
		}
	}
	if (Status{}).Describe(now) != "" {
		t.Error("disabled budget has no line")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
