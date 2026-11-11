package main

import (
	"bytes"
	"github.com/swordsmanluke/cloche/internal/dra"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDRAList(t *testing.T) {
	root := t.TempDir()
	var buf bytes.Buffer
	if err := draCommand([]string{"list", "--project", root}, &buf); err != nil || !strings.Contains(buf.String(), "No DRAs") {
		t.Fatalf("%q %v", buf.String(), err)
	}
	d := filepath.Join(root, ".cloche", "dras", "auth")
	os.MkdirAll(d, 0o755)
	os.WriteFile(filepath.Join(d, "charter.md"), []byte("# Auth domain\n"), 0o644)
	buf.Reset()
	if err := draCommand([]string{"list", "-p", root}, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "auth") || !strings.Contains(out, "Auth domain") {
		t.Errorf("out: %q", out)
	}
	if draCommand(nil, &buf) == nil || draCommand([]string{"list", "x"}, &buf) == nil {
		t.Error("expected usage errors")
	}
}

func TestValidateReportsDRAs(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".cloche", "dras", "Bad"), 0o755)
	errs := validateProject(root, "")
	if !strings.Contains(strings.Join(errs, "\n"), "DRA") {
		t.Errorf("errs: %v", errs)
	}
}

func TestDRAWake(t *testing.T) {
	var gotProject, gotName, gotMsg string
	old := wakeDRAFn
	defer func() { wakeDRAFn = old }()
	wakeDRAFn = func(project, name, message string) (string, string, error) {
		gotProject, gotName, gotMsg = project, name, message
		return "dra-wake-auth-1", "", nil
	}
	root := t.TempDir()
	var buf bytes.Buffer
	if err := draCommand([]string{"wake", "auth", "--message", "main is red", "--project", root}, &buf); err != nil {
		t.Fatal(err)
	}
	if gotProject != root || gotName != "auth" || gotMsg != "main is red" || !strings.Contains(buf.String(), "dra-wake-auth-1") {
		t.Errorf("%q %q %q %q", gotProject, gotName, gotMsg, buf.String())
	}
	for _, bad := range [][]string{{"wake"}, {"wake", "auth"}, {"wake", "--message", "x"}, {"wake", "auth", "--message", " "}, {"list", "--message", "x"}} {
		if draCommand(bad, &buf) == nil {
			t.Errorf("expected an error for %v", bad)
		}
	}
}

func TestDRAListShowsSessionAndLastWake(t *testing.T) {
	root := t.TempDir()
	d := filepath.Join(root, ".cloche", "dras", "auth")
	os.MkdirAll(d, 0o755)
	os.WriteFile(filepath.Join(d, "charter.md"), []byte("# Auth domain\n"), 0o644)
	if err := dra.UpdateSession(root, "auth", func(s *dra.Session) {
		s.ID, s.LastRunID, s.LastResult, s.LastMode = "sess-123", "dra-wake-auth-7", "succeeded", dra.ModeResumed
	}); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := draCommand([]string{"list", "-p", root}, &buf); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"sess-123", "dra-wake-auth-7", "succeeded", "resumed"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("missing %q in %q", want, buf.String())
		}
	}
}

func TestDRARulingAndHeartbeat(t *testing.T) {
	var kind, subject, message, name string
	old := notifyDRAFn
	defer func() { notifyDRAFn = old }()
	notifyDRAFn = func(project, n, k, sub, msg string) error {
		name, kind, subject, message = n, k, sub, msg
		return nil
	}
	root := t.TempDir()
	var buf bytes.Buffer
	if err := draCommand([]string{"ruling", "auth", "--question", "Q7", "--message", "yes", "-p", root}, &buf); err != nil {
		t.Fatal(err)
	}
	if name != "auth" || kind != dra.KindRuling || subject != "Q7" || message != "yes" {
		t.Errorf("%q %q %q %q", name, kind, subject, message)
	}
	if err := draCommand([]string{"heartbeat", "auth", "-p", root}, &buf); err != nil || kind != dra.KindHeartbeat {
		t.Errorf("%v %q", err, kind)
	}
	for _, bad := range [][]string{{"ruling", "auth"}, {"ruling", "auth", "--question", "Q"}, {"heartbeat"}} {
		if draCommand(bad, &buf) == nil {
			t.Errorf("expected an error for %v", bad)
		}
	}
}
