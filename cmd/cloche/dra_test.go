package main

import (
	"bytes"
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
