package main

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"
)

// recordingWaitClient captures what the CLI asked the daemon for.
type recordingWaitClient struct {
	reqs      []waitRequest
	cancelled []string
	waits     []waitInfo
}

func (r *recordingWaitClient) Register(req waitRequest) (string, error) {
	r.reqs = append(r.reqs, req)
	return fmt.Sprintf("w-%d", len(r.reqs)), nil
}
func (r *recordingWaitClient) List() ([]waitInfo, error) { return r.waits, nil }
func (r *recordingWaitClient) Cancel(id string) error {
	r.cancelled = append(r.cancelled, id)
	return nil
}

func withWaitClient(t *testing.T) *recordingWaitClient {
	t.Helper()
	rc := &recordingWaitClient{}
	old := newWaitClient
	newWaitClient = func() (waitClient, error) { return rc, nil }
	t.Cleanup(func() { newWaitClient = old })
	return rc
}

func runDRA(args ...string) (string, error) {
	var buf bytes.Buffer
	err := draCommand(args, &buf)
	return buf.String(), err
}

func TestDRAWaitBuildsRequest(t *testing.T) {
	rc := withWaitClient(t)
	out, err := runDRA("wait", "build-done", "--arg", "branch=main", "--arg", "a=b=c", "--interval", "5m", "--timeout", "1h", "--note", "main build")
	if err != nil || out != "Registered wait w-1\n" {
		t.Fatalf("%q %v", out, err)
	}
	got := rc.reqs[0]
	if got.Poll != "build-done" || got.Args["branch"] != "main" || got.Args["a"] != "b=c" ||
		got.Interval != 5*time.Minute || got.Timeout != time.Hour || got.Note != "main build" {
		t.Fatalf("request: %+v", got)
	}
	// Unset interval and timeout are left to the daemon's defaults.
	if _, err = runDRA("wait", "build-done", "--note", "x"); err != nil {
		t.Fatal(err)
	}
	if g := rc.reqs[1]; g.Interval != 0 || g.Timeout != 0 {
		t.Fatalf("CLI must not invent defaults: %+v", g)
	}
	// The questions poll carries its ids as the "ids" argument.
	if _, err = runDRA("wait", "questions", "--ids", "Q14, Q15", "--note", "rulings"); err != nil {
		t.Fatal(err)
	}
	if g := rc.reqs[2]; g.Poll != "questions" || g.Args["ids"] != "Q14,Q15" {
		t.Fatalf("questions request: %+v", g)
	}
}

func TestDRAWaitCancelAndList(t *testing.T) {
	rc := withWaitClient(t)
	out, err := runDRA("wait", "--cancel", "w-1")
	if err != nil || !strings.Contains(out, "Cancelled wait w-1") || rc.cancelled[0] != "w-1" {
		t.Fatalf("%q %v %v", out, err, rc.cancelled)
	}
	if out, _ = runDRA("waits"); !strings.Contains(out, "No pending waits") {
		t.Fatalf("%q", out)
	}
	rc.waits = []waitInfo{
		{ID: "w-1", WaitRequest: waitRequest{Poll: "build-done", Args: map[string]string{"branch": "main"}, Interval: 5 * time.Minute, Timeout: 72 * time.Hour, Note: "main build"}},
		{ID: "w-2", WaitRequest: waitRequest{Poll: "questions", Args: map[string]string{"ids": "Q14,Q15"}, Note: "rulings"}},
	}
	out, _ = runDRA("waits")
	for _, want := range []string{"build-done", "branch=main", "main build", "questions (Q14, Q15)", "72h0m0s"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %q", want, out)
		}
	}
}

func TestDRAWaitRejectsBadFlags(t *testing.T) {
	rc := withWaitClient(t)
	for _, bad := range [][]string{
		{"wait"},
		{"wait", "build-done"},
		{"wait", "build-done", "--note", " "},
		{"wait", "build-done", "--note", "x", "--arg", "novalue"},
		{"wait", "build-done", "--note", "x", "--interval", "soon"},
		{"wait", "build-done", "--note", "x", "--timeout", "-1h"},
		{"wait", "build-done", "--note", "x", "--bogus"},
		{"wait", "build-done", "--note"},
		{"wait", "build-done", "--note", "x", "--ids", "Q1"},
		{"wait", "questions", "--note", "x"},
		{"wait", "questions", "--ids", "Q1", "--arg", "a=b", "--note", "x"},
		{"wait", "--cancel", "w-1", "--note", "x"},
		{"waits", "extra"},
	} {
		if out, err := runDRA(bad...); err == nil {
			t.Errorf("expected an error for %v, got %q", bad, out)
		}
	}
	if len(rc.reqs) != 0 || len(rc.cancelled) != 0 {
		t.Errorf("a rejected command reached the daemon: %+v %v", rc.reqs, rc.cancelled)
	}
}

func TestDRAWaitNeedsDRACredential(t *testing.T) {
	t.Setenv("CLOCHE_DRA_URL", "")
	t.Setenv("CLOCHE_DRA_TOKEN", "")
	if _, err := runDRA("waits"); err == nil || !strings.Contains(err.Error(), "CLOCHE_DRA_TOKEN") {
		t.Errorf("err: %v", err)
	}
}

func TestDRAModeAllowsOnlyWaitAndQuestionsReadWrite(t *testing.T) {
	c := &draClient{base: "http://127.0.0.1:1", token: "t"}
	for _, args := range [][]string{{"dra", "wake", "x"}, {"dra", "list"}, {"dra", "heartbeat"}, {"questions", "guide"}, {"questions"}, {"dra"}} {
		handled, err := runDRACommand(c, args)
		if !handled || err == nil {
			t.Errorf("%v: handled=%v err=%v", args, handled, err)
		}
	}
}
