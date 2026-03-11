package runcontext

import (
	"os"
	"path/filepath"
	"testing"
)

func TestContextPath(t *testing.T) {
	got := ContextPath("/projects/myapp", "develop-swift-oak-a1b2")
	want := filepath.Join("/projects/myapp", ".cloche", "develop-swift-oak-a1b2", "context.json")
	if got != want {
		t.Errorf("ContextPath = %q, want %q", got, want)
	}
}

func TestSetAndGet(t *testing.T) {
	dir := t.TempDir()
	runID := "test-run-1234"

	// Set a value
	if err := Set(dir, runID, "branch", "feature-x"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Get it back
	val, ok, err := Get(dir, runID, "branch")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("expected key to exist")
	}
	if val != "feature-x" {
		t.Errorf("Get = %q, want %q", val, "feature-x")
	}
}

func TestGet_MissingKey(t *testing.T) {
	dir := t.TempDir()
	runID := "test-run-1234"

	// Set one key
	if err := Set(dir, runID, "a", "1"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Get a different key
	_, ok, err := Get(dir, runID, "b")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if ok {
		t.Error("expected key not to exist")
	}
}

func TestGet_NoFile(t *testing.T) {
	dir := t.TempDir()

	// No context.json exists
	_, ok, err := Get(dir, "nonexistent-run", "key")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if ok {
		t.Error("expected key not to exist when file is absent")
	}
}

func TestSet_OverwritesExistingKey(t *testing.T) {
	dir := t.TempDir()
	runID := "test-run-1234"

	if err := Set(dir, runID, "k", "v1"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := Set(dir, runID, "k", "v2"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	val, ok, err := Get(dir, runID, "k")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok || val != "v2" {
		t.Errorf("expected v2, got %q (ok=%v)", val, ok)
	}
}

func TestSet_MultipleKeys(t *testing.T) {
	dir := t.TempDir()
	runID := "test-run-1234"

	if err := Set(dir, runID, "a", "1"); err != nil {
		t.Fatalf("Set a: %v", err)
	}
	if err := Set(dir, runID, "b", "2"); err != nil {
		t.Fatalf("Set b: %v", err)
	}

	v1, ok1, _ := Get(dir, runID, "a")
	v2, ok2, _ := Get(dir, runID, "b")

	if !ok1 || v1 != "1" {
		t.Errorf("a: got %q ok=%v", v1, ok1)
	}
	if !ok2 || v2 != "2" {
		t.Errorf("b: got %q ok=%v", v2, ok2)
	}
}

func TestSet_CreatesDirectories(t *testing.T) {
	dir := t.TempDir()
	runID := "test-run-1234"

	if err := Set(dir, runID, "key", "val"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	path := ContextPath(dir, runID)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("expected context.json to be created")
	}
}

func TestLoad_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	runID := "test-run-1234"
	ctxDir := filepath.Join(dir, ".cloche", runID)
	os.MkdirAll(ctxDir, 0755)
	os.WriteFile(filepath.Join(ctxDir, "context.json"), []byte("not json"), 0644)

	_, _, err := Get(dir, runID, "key")
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}
