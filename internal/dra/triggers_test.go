package dra

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestDomainOf_FirstMatchWinsAndReportsTheRest(t *testing.T) {
	dras := []string{"auth", "engine"}
	owner, ignored := DomainOf([]string{"bug", "engine", "auth"}, dras)
	if owner != "engine" || len(ignored) != 1 || ignored[0] != "auth" {
		t.Fatalf("owner=%q ignored=%v", owner, ignored)
	}
	if owner, _ := DomainOf([]string{"bug", "ready"}, dras); owner != "" {
		t.Fatalf("no DRA label: owner=%q", owner)
	}
	if owner, ign := DomainOf([]string{"auth", "auth"}, dras); owner != "auth" || len(ign) != 0 {
		t.Fatalf("repeated label is one match: %q %v", owner, ign)
	}
}

func TestHeartbeatDue(t *testing.T) {
	now := time.Now()
	if !HeartbeatDue(time.Time{}, now, DefaultHeartbeat) {
		t.Error("never woken: due")
	}
	if HeartbeatDue(now.Add(-time.Hour), now, DefaultHeartbeat) {
		t.Error("woken 1h ago: not due at 4h")
	}
	if !HeartbeatDue(now.Add(-5*time.Hour), now, DefaultHeartbeat) {
		t.Error("woken 5h ago: due")
	}
	if HeartbeatDue(time.Time{}, now, 0) {
		t.Error("interval 0 disables heartbeats")
	}
}

func TestInbox_BatchesInOrderAndRequeuesFirst(t *testing.T) {
	root := t.TempDir()
	in := NewInbox()
	in.Add(root, "auth", Event{Kind: KindTaskFailed, Subject: "t1"})
	in.Add(root, "auth", Event{Kind: KindSignal, Subject: "src/k"})
	in.Add(root, "engine", Event{Kind: KindTaskFinished, Subject: "t9"})
	if in.Pending(root, "auth") != 2 {
		t.Fatal("pending")
	}
	got := in.Take(root, "auth")
	if len(got) != 2 || got[0].Subject != "t1" || got[1].Subject != "src/k" {
		t.Fatalf("take: %+v", got)
	}
	if in.Pending(root, "auth") != 0 || in.Pending(root, "engine") != 1 {
		t.Fatal("take must only empty the one DRA")
	}
	in.Add(root, "auth", Event{Kind: KindTaskParked, Subject: "t2"})
	in.Requeue(root, "auth", got)
	again := in.Take(root, "auth")
	if len(again) != 3 || again[0].Subject != "t1" || again[2].Subject != "t2" {
		t.Fatalf("requeued events go first: %+v", again)
	}
}

func TestInbox_RepeatedRulingAndHeartbeatNotDoubled(t *testing.T) {
	root := t.TempDir()
	in := NewInbox()
	if !in.Add(root, "auth", Event{Kind: KindRuling, Subject: "Q1", Text: "yes"}) {
		t.Fatal("first ruling queued")
	}
	if in.Add(root, "auth", Event{Kind: KindRuling, Subject: "Q1", Text: "yes"}) {
		t.Fatal("same ruling again is not queued twice")
	}
	if !in.Add(root, "auth", Event{Kind: KindRuling, Subject: "Q2", Text: "no"}) {
		t.Fatal("another question is a new event")
	}
	in.Add(root, "auth", Event{Kind: KindHeartbeat})
	if in.Add(root, "auth", Event{Kind: KindHeartbeat}) {
		t.Fatal("one pending heartbeat is enough")
	}
	if in.Pending(root, "auth") != 3 {
		t.Fatalf("pending %d", in.Pending(root, "auth"))
	}
}

func TestInbox_PersistsAcrossRestart(t *testing.T) {
	root := t.TempDir()
	in := NewInbox()
	in.Add(root, "auth", Event{Kind: KindTaskFailed, Subject: "t1", Text: "task failed"})
	in2 := NewInbox()
	if err := in2.Load(root); err != nil {
		t.Fatal(err)
	}
	got := in2.Take(root, "auth")
	if len(got) != 1 || got[0].Subject != "t1" {
		t.Fatalf("restored: %+v", got)
	}
	if _, err := os.Stat(EventsPath(root)); !os.IsNotExist(err) {
		t.Fatal("file removed once nothing is pending")
	}
}

func TestFormatMessage(t *testing.T) {
	ev := []Event{
		{Kind: KindTaskFailed, Subject: "t1", Text: "task failed", At: time.Unix(0, 0).UTC()},
		{Kind: KindHeartbeat, At: time.Unix(60, 0).UTC()},
	}
	m := FormatMessage("auth", ev)
	for _, want := range []string{`"auth"`, "2 events", "task-failed: t1 - task failed", "heartbeat: periodic check-in"} {
		if !strings.Contains(m, want) {
			t.Errorf("missing %q in:\n%s", want, m)
		}
	}
}
