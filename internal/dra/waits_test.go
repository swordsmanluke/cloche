package dra

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// waitProject makes a project with a DRA "qa" owning the given poll scripts.
func waitProject(t *testing.T, polls ...string) string {
	t.Helper()
	root := t.TempDir()
	dir := PollsDir(root, "qa")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range polls {
		if err := os.WriteFile(filepath.Join(dir, p+".sh"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

type sentEvent struct {
	project, dra string
	e            Event
}

func newTestRegistry(t *testing.T, polls ...string) (*WaitRegistry, *FakePollRunner, *[]sentEvent, string, *time.Time) {
	t.Helper()
	root := waitProject(t, polls...)
	runner := NewFakePollRunner()
	var sent []sentEvent
	reg := NewWaitRegistry(NewFakeWaitStore(), runner, func(p, d string, e Event) { sent = append(sent, sentEvent{p, d, e}) })
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	reg.SetClock(func() time.Time { return now })
	return reg, runner, &sent, root, &now
}

func TestWaitRegisterListCancel(t *testing.T) {
	reg, _, sent, root, _ := newTestRegistry(t, "build-done")
	id, err := reg.Register(root, "qa", WaitRequest{Poll: "build-done", Args: map[string]string{"branch": "main"}, Interval: 5 * time.Minute, Note: "main build"})
	if err != nil {
		t.Fatal(err)
	}
	qid, err := reg.Register(root, "qa", WaitRequest{Poll: "questions", Args: map[string]string{"ids": "Q14,Q15"}, Note: "rulings"})
	if err != nil {
		t.Fatal(err)
	}
	if id == qid {
		t.Fatal("ids must be distinct")
	}
	got, _ := reg.List(root, "qa")
	if len(got) != 2 || got[0].Poll != "build-done" || got[0].Note != "main build" {
		t.Fatalf("list: %+v", got)
	}
	if got[1].Interval != DefaultWaitInterval || got[1].Timeout != DefaultWaitTimeout {
		t.Fatalf("defaults not applied: %+v", got[1])
	}
	if err := reg.Cancel(root, "qa", id); err != nil {
		t.Fatal(err)
	}
	if err := reg.Cancel(root, "qa", id); err == nil {
		t.Fatal("cancelling twice must fail")
	}
	if got, _ = reg.List(root, "qa"); len(got) != 1 {
		t.Fatalf("after cancel: %+v", got)
	}
	reg.Tick(context.Background())
	if len(*sent) != 0 {
		t.Fatalf("no event expected: %+v", *sent)
	}
}

func TestWaitsAreOwnedByTheirDRA(t *testing.T) {
	reg, _, _, root, _ := newTestRegistry(t, "build-done")
	id, _ := reg.Register(root, "qa", WaitRequest{Poll: "build-done", Note: "x"})
	if got, _ := reg.List(root, "other"); len(got) != 0 {
		t.Fatalf("another DRA sees %+v", got)
	}
	if err := reg.Cancel(root, "other", id); err == nil {
		t.Fatal("another DRA cancelled a wait it does not own")
	}
	if got, _ := reg.List(root, "qa"); len(got) != 1 {
		t.Fatal("wait must survive")
	}
}

func TestWaitUnknownPollRefusedWithAvailable(t *testing.T) {
	reg, _, _, root, _ := newTestRegistry(t, "build-done", "deploy-ready")
	_, err := reg.Register(root, "qa", WaitRequest{Poll: "no-such-poll", Note: "x"})
	if err == nil || !strings.Contains(err.Error(), "unknown poll no-such-poll; this DRA has: build-done, deploy-ready") {
		t.Fatalf("err: %v", err)
	}
	if got, _ := reg.List(root, "qa"); len(got) != 0 {
		t.Fatal("a refused wait was registered")
	}
	// A poll that is not a plain name never reaches the filesystem.
	for _, bad := range []string{"../build-done", "build-done.sh", "/etc/passwd", ""} {
		if _, err := reg.Register(root, "qa", WaitRequest{Poll: bad, Note: "x"}); err == nil || !strings.Contains(err.Error(), "unknown poll") {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

func TestWaitLimits(t *testing.T) {
	reg, _, _, root, _ := newTestRegistry(t, "build-done")
	req := WaitRequest{Poll: "build-done", Note: "x"}
	for i := 0; i < MaxPendingWaits; i++ {
		if _, err := reg.Register(root, "qa", req); err != nil {
			t.Fatalf("wait %d: %v", i, err)
		}
	}
	if _, err := reg.Register(root, "qa", req); err == nil || !strings.Contains(err.Error(), "20 pending wait limit") {
		t.Fatalf("21st wait: %v", err)
	}
	// The limit is per DRA.
	if err := os.MkdirAll(PollsDir(root, "other"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(PollsDir(root, "other"), "build-done.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Register(root, "other", req); err != nil {
		t.Fatalf("another DRA: %v", err)
	}

	reg2, _, _, root2, _ := newTestRegistry(t, "build-done")
	if _, err := reg2.Register(root2, "qa", WaitRequest{Poll: "build-done", Note: "x", Interval: 30 * time.Second}); err == nil || !strings.Contains(err.Error(), "1m0s minimum interval") {
		t.Fatalf("30s interval: %v", err)
	}
	if _, err := reg2.Register(root2, "qa", WaitRequest{Poll: "build-done", Note: "x", Interval: time.Minute}); err != nil {
		t.Fatalf("1m interval is the minimum, not under it: %v", err)
	}
	id, _ := reg2.Register(root2, "qa", WaitRequest{Poll: "build-done", Note: "x"})
	for _, w := range func() []Wait { l, _ := reg2.List(root2, "qa"); return l }() {
		if w.ID == id && w.Timeout != 72*time.Hour {
			t.Fatalf("default timeout %v", w.Timeout)
		}
	}
	if _, err := reg2.Register(root2, "qa", WaitRequest{Poll: "build-done", Note: "  "}); err == nil {
		t.Fatal("a note is required")
	}
}

func TestQuestionsPollIDsValidated(t *testing.T) {
	root := waitProject(t)
	spec, err := ResolvePoll(root, "qa", WaitRequest{Poll: "questions", Args: map[string]string{"ids": "Q14, Q15"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(spec.Argv, " ") != "cloche questions await Q14 Q15" {
		t.Fatalf("argv: %v", spec.Argv)
	}
	for _, ids := range []string{"", "--help", "Q1;rm", "Q1 $(x)"} {
		if _, err := ResolvePoll(root, "qa", WaitRequest{Poll: "questions", Args: map[string]string{"ids": ids}}); err == nil {
			t.Errorf("ids %q accepted", ids)
		}
	}
	if _, err := ResolvePoll(root, "qa", WaitRequest{Poll: "questions", Args: map[string]string{"ids": "Q1", "x": "y"}}); err == nil {
		t.Error("extra args accepted for the questions poll")
	}
}

// Arguments are environment variables of a directly executed script: text
// that looks like shell is data.
func TestPollArgsReachScriptAsEnvNeverThroughShell(t *testing.T) {
	root := waitProject(t)
	out := filepath.Join(t.TempDir(), "seen")
	pwned := filepath.Join(t.TempDir(), "pwned")
	script := "#!/bin/sh\nprintf %s \"$CLOCHE_WAIT_ARG_BRANCH\" > " + out + "\n"
	if err := os.WriteFile(filepath.Join(PollsDir(root, "qa"), "build-done.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	evil := "main; touch " + pwned + " $(touch " + pwned + ") `touch " + pwned + "`"
	spec, err := ResolvePoll(root, "qa", WaitRequest{Poll: "build-done", Args: map[string]string{"branch": evil}})
	if err != nil {
		t.Fatal(err)
	}
	if err := spec.Command(context.Background()).Run(); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(out); string(got) != evil {
		t.Fatalf("script saw %q", got)
	}
	if _, err := os.Stat(pwned); err == nil {
		t.Fatal("the host executed text from an argument")
	}
}

func TestPollArgsRejectedWhenUnsafe(t *testing.T) {
	root := waitProject(t, "build-done")
	for _, args := range []map[string]string{
		{"bad key": "v"}, {"1x": "v"}, {"k": "a\x00b"}, {"k": strings.Repeat("x", maxWaitArgBytes+1)}, {"=": "v"},
	} {
		if _, err := ResolvePoll(root, "qa", WaitRequest{Poll: "build-done", Args: args}); err == nil {
			t.Errorf("args %v accepted", args)
		}
	}
	if EnvKey("branch") != "CLOCHE_WAIT_ARG_BRANCH" {
		t.Fatal(EnvKey("branch"))
	}
}

func TestWaitReadyDeliversOneEvent(t *testing.T) {
	reg, runner, sent, root, _ := newTestRegistry(t, "build-done")
	reg.Register(root, "qa", WaitRequest{Poll: "build-done", Note: "main build"})
	runner.Script("build-done", PollOutcome{Result: "green", Output: "all 42 passed\n"})
	reg.Tick(context.Background())
	reg.Tick(context.Background())
	if len(*sent) != 1 {
		t.Fatalf("want exactly one event, got %+v", *sent)
	}
	e := (*sent)[0]
	if e.dra != "qa" || e.project != root || e.e.Kind != KindWait {
		t.Fatalf("event: %+v", e)
	}
	for _, want := range []string{"main build", "green", "all 42 passed", WaitReady} {
		if !strings.Contains(e.e.Text, want) {
			t.Errorf("event text %q lacks %q", e.e.Text, want)
		}
	}
	if got, _ := reg.List(root, "qa"); len(got) != 0 {
		t.Fatal("an ended wait stays listed")
	}
}

func TestWaitNotYetKeepsPendingAndHonoursInterval(t *testing.T) {
	reg, runner, sent, root, now := newTestRegistry(t, "build-done")
	reg.Register(root, "qa", WaitRequest{Poll: "build-done", Note: "x", Interval: 5 * time.Minute})
	reg.Tick(context.Background())
	if len(runner.Invoked) != 1 {
		t.Fatalf("first poll is immediate: %d", len(runner.Invoked))
	}
	*now = now.Add(time.Minute)
	reg.Tick(context.Background())
	if len(runner.Invoked) != 1 {
		t.Fatal("polled before the interval elapsed")
	}
	*now = now.Add(5 * time.Minute)
	reg.Tick(context.Background())
	if len(runner.Invoked) != 2 || len(*sent) != 0 {
		t.Fatalf("polls %d events %d", len(runner.Invoked), len(*sent))
	}
	if got, _ := reg.List(root, "qa"); len(got) != 1 {
		t.Fatal("wait should still be pending")
	}
}

func TestWaitFailedAndTimedOutEachDeliverOneEvent(t *testing.T) {
	reg, runner, sent, root, now := newTestRegistry(t, "build-done", "deploy-ready")
	reg.Register(root, "qa", WaitRequest{Poll: "build-done", Note: "build"})
	reg.Register(root, "qa", WaitRequest{Poll: "deploy-ready", Note: "deploy", Timeout: time.Hour})
	runner.Script("build-done", PollOutcome{Failed: true, Output: "boom"})
	runner.Script("deploy-ready", PollOutcome{Output: "still deploying"})
	reg.Tick(context.Background())
	if len(*sent) != 1 || !strings.Contains((*sent)[0].e.Text, "failed") || !strings.Contains((*sent)[0].e.Text, "boom") {
		t.Fatalf("failure event: %+v", *sent)
	}
	*now = now.Add(time.Hour)
	reg.Tick(context.Background())
	reg.Tick(context.Background())
	if len(*sent) != 2 {
		t.Fatalf("want two events: %+v", *sent)
	}
	last := (*sent)[1].e.Text
	for _, want := range []string{"deploy", "timeout", "still deploying"} {
		if !strings.Contains(last, want) {
			t.Errorf("timeout event %q lacks %q", last, want)
		}
	}
}

func TestWaitDeletedScriptFailsTheWait(t *testing.T) {
	reg, _, sent, root, _ := newTestRegistry(t, "build-done")
	reg.Register(root, "qa", WaitRequest{Poll: "build-done", Note: "x"})
	os.Remove(filepath.Join(PollsDir(root, "qa"), "build-done.sh"))
	reg.Tick(context.Background())
	if len(*sent) != 1 || !strings.Contains((*sent)[0].e.Text, "unknown poll") {
		t.Fatalf("%+v", *sent)
	}
}

func TestWaitCancelledDuringPollSendsNothing(t *testing.T) {
	root := waitProject(t, "build-done")
	var sent []Event
	var reg *WaitRegistry
	runner := cancelRunner{}
	reg = NewWaitRegistry(NewFakeWaitStore(), &runner, func(_, _ string, e Event) { sent = append(sent, e) })
	id, _ := reg.Register(root, "qa", WaitRequest{Poll: "build-done", Note: "x"})
	runner.onPoll = func() { reg.Cancel(root, "qa", id) }
	reg.Tick(context.Background())
	if len(sent) != 0 {
		t.Fatalf("event for a cancelled wait: %+v", sent)
	}
}

type cancelRunner struct{ onPoll func() }

func (c *cancelRunner) Poll(context.Context, PollSpec) PollOutcome {
	c.onPoll()
	return PollOutcome{Result: "green"}
}

func TestWaitEventBatchesWithInbox(t *testing.T) {
	root := t.TempDir()
	in := NewInbox()
	in.Add(root, "qa", Event{Kind: KindTaskFailed, Subject: "t1", Text: "task failed"})
	in.Add(root, "qa", Event{Kind: KindWait, Subject: "w-1", Text: `"main build" (poll build-done) ready, result green; last output: ok`})
	msg := FormatMessage("qa", in.Take(root, "qa"))
	if !strings.Contains(msg, "2 events, delivered together") || !strings.Contains(msg, "wait: w-1") || !strings.Contains(msg, "task-failed") {
		t.Fatalf("message:\n%s", msg)
	}
}
