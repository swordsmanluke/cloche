package dra

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestListNone(t *testing.T) {
	ds, err := List(t.TempDir())
	if err != nil || len(ds) != 0 {
		t.Fatalf("got %v %v", ds, err)
	}
}

func TestListAndValidate(t *testing.T) {
	root := t.TempDir()
	d := filepath.Join(root, ".cloche", "dras")
	write(t, filepath.Join(d, "auth", "charter.md"), "intro\n```\n# no\n```\n## Authentication\n")
	write(t, filepath.Join(d, "perf", "charter.md"), "# Performance\n")
	write(t, filepath.Join(d, "nocharter", "notes.md"), "")
	write(t, filepath.Join(d, "Bad_Name", "charter.md"), "# x\n")
	write(t, filepath.Join(d, "stray.txt"), "x")

	ds, err := List(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(ds) != 4 {
		t.Fatalf("got %d DRAs: %+v", len(ds), ds)
	}
	by := map[string]DRA{}
	for _, x := range ds {
		by[x.Name] = x
	}
	if by["auth"].Heading != "Authentication" || !by["auth"].Valid() {
		t.Errorf("auth: %+v", by["auth"])
	}
	if by["perf"].Heading != "Performance" {
		t.Errorf("perf: %+v", by["perf"])
	}
	if by["nocharter"].Valid() {
		t.Error("nocharter should be invalid")
	}
	if by["Bad_Name"].Valid() {
		t.Error("bad name should be invalid")
	}
	errs := Validate(root)
	if len(errs) != 2 || !strings.Contains(strings.Join(errs, "\n"), "missing charter.md") {
		t.Errorf("errs: %v", errs)
	}
	// List must not create notes.md.
	if _, err := os.Stat(by["auth"].NotesPath); !os.IsNotExist(err) {
		t.Error("List created notes.md")
	}
	if err := EnsureNotes(by["auth"]); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(by["auth"].NotesPath); err != nil {
		t.Error("notes.md not created")
	}
}

func TestValidName(t *testing.T) {
	for n, want := range map[string]bool{"a-b1": true, "": false, "A": false, "a_b": false, "a b": false} {
		if ValidName(n) != want {
			t.Errorf("%q", n)
		}
	}
}
