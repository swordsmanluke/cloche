package runcontext

import (
	"os"
	"path/filepath"
	"testing"
)

func TestContextPath(t *testing.T) {
	got := ContextPath("/projects/myapp", "cloche-abc1")
	want := filepath.Join("/projects/myapp", ".cloche", "runs", "cloche-abc1", "context.json")
	if got != want {
		t.Errorf("ContextPath = %q, want %q", got, want)
	}
}

func TestRunDir(t *testing.T) {
	got := RunDir("/projects/myapp", "cloche-abc1")
	want := filepath.Join("/projects/myapp", ".cloche", "runs", "cloche-abc1")
	if got != want {
		t.Errorf("RunDir = %q, want %q", got, want)
	}
}

func TestPromptPath(t *testing.T) {
	got := PromptPath("/projects/myapp", "cloche-abc1")
	want := filepath.Join("/projects/myapp", ".cloche", "runs", "cloche-abc1", "prompt.txt")
	if got != want {
		t.Errorf("PromptPath = %q, want %q", got, want)
	}
}

func TestSetAndGet(t *testing.T) {
	dir := t.TempDir()
	taskID := "cloche-abc1"

	// Set a value
	if err := Set(dir, taskID, "branch", "feature-x"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Get it back
	val, ok, err := Get(dir, taskID, "branch")
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
	taskID := "cloche-abc1"

	// Set one key
	if err := Set(dir, taskID, "a", "1"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Get a different key
	_, ok, err := Get(dir, taskID, "b")
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
	_, ok, err := Get(dir, "nonexistent-task", "key")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if ok {
		t.Error("expected key not to exist when file is absent")
	}
}

func TestSet_OverwritesExistingKey(t *testing.T) {
	dir := t.TempDir()
	taskID := "cloche-abc1"

	if err := Set(dir, taskID, "k", "v1"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := Set(dir, taskID, "k", "v2"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	val, ok, err := Get(dir, taskID, "k")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok || val != "v2" {
		t.Errorf("expected v2, got %q (ok=%v)", val, ok)
	}
}

func TestSet_MultipleKeys(t *testing.T) {
	dir := t.TempDir()
	taskID := "cloche-abc1"

	if err := Set(dir, taskID, "a", "1"); err != nil {
		t.Fatalf("Set a: %v", err)
	}
	if err := Set(dir, taskID, "b", "2"); err != nil {
		t.Fatalf("Set b: %v", err)
	}

	v1, ok1, _ := Get(dir, taskID, "a")
	v2, ok2, _ := Get(dir, taskID, "b")

	if !ok1 || v1 != "1" {
		t.Errorf("a: got %q ok=%v", v1, ok1)
	}
	if !ok2 || v2 != "2" {
		t.Errorf("b: got %q ok=%v", v2, ok2)
	}
}

func TestSet_CreatesDirectories(t *testing.T) {
	dir := t.TempDir()
	taskID := "cloche-abc1"

	if err := Set(dir, taskID, "key", "val"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	path := ContextPath(dir, taskID)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("expected context.json to be created")
	}
}

func TestLoad_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	taskID := "cloche-abc1"
	ctxDir := filepath.Join(dir, ".cloche", "runs", taskID)
	os.MkdirAll(ctxDir, 0755)
	os.WriteFile(filepath.Join(ctxDir, "context.json"), []byte("not json"), 0644)

	_, _, err := Get(dir, taskID, "key")
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestCleanup(t *testing.T) {
	dir := t.TempDir()
	taskID := "cloche-abc1"

	// Create some state
	if err := Set(dir, taskID, "key", "val"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Verify it exists
	runDir := RunDir(dir, taskID)
	if _, err := os.Stat(runDir); os.IsNotExist(err) {
		t.Fatal("expected run directory to exist before cleanup")
	}

	// Cleanup
	if err := Cleanup(dir, taskID); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}

	// Verify it's gone
	if _, err := os.Stat(runDir); !os.IsNotExist(err) {
		t.Error("expected run directory to be removed after cleanup")
	}
}

func TestSeedRunContext(t *testing.T) {
	dir := t.TempDir()
	taskID := "cloche-abc1"

	if err := SeedRunContext(dir, taskID, "jifo", "develop", "jifo-develop"); err != nil {
		t.Fatalf("SeedRunContext: %v", err)
	}

	cases := map[string]string{
		"task_id":    taskID,
		"attempt_id": "jifo",
		"workflow":   "develop",
		"run_id":     "jifo-develop",
	}
	for key, want := range cases {
		got, ok, err := Get(dir, taskID, key)
		if err != nil {
			t.Fatalf("Get(%q): %v", key, err)
		}
		if !ok {
			t.Errorf("key %q missing", key)
		}
		if got != want {
			t.Errorf("key %q = %q, want %q", key, got, want)
		}
	}
}

func TestSetPrevStep(t *testing.T) {
	dir := t.TempDir()
	taskID := "cloche-abc1"

	if err := SetPrevStep(dir, taskID, "implement", "success"); err != nil {
		t.Fatalf("SetPrevStep: %v", err)
	}

	step, _, _ := Get(dir, taskID, "prev_step")
	result, _, _ := Get(dir, taskID, "prev_result")
	if step != "implement" {
		t.Errorf("prev_step = %q, want %q", step, "implement")
	}
	if result != "success" {
		t.Errorf("prev_result = %q, want %q", result, "success")
	}
}

func TestSetPrevStep_EntryStep(t *testing.T) {
	dir := t.TempDir()
	taskID := "cloche-abc1"

	if err := SetPrevStep(dir, taskID, "", ""); err != nil {
		t.Fatalf("SetPrevStep: %v", err)
	}

	step, ok, _ := Get(dir, taskID, "prev_step")
	if !ok {
		t.Error("expected prev_step key to exist")
	}
	if step != "" {
		t.Errorf("prev_step = %q, want empty string", step)
	}
}

func TestSetStepResult(t *testing.T) {
	dir := t.TempDir()
	taskID := "cloche-abc1"

	if err := SetStepResult(dir, taskID, "develop", "implement", "success"); err != nil {
		t.Fatalf("SetStepResult: %v", err)
	}

	got, ok, err := Get(dir, taskID, "develop:implement:result")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Error("expected develop:implement:result to exist")
	}
	if got != "success" {
		t.Errorf("develop:implement:result = %q, want %q", got, "success")
	}
}

func TestSetStepResult_MultipleSteps(t *testing.T) {
	dir := t.TempDir()
	taskID := "cloche-abc1"

	if err := SetStepResult(dir, taskID, "develop", "implement", "success"); err != nil {
		t.Fatalf("SetStepResult implement: %v", err)
	}
	if err := SetStepResult(dir, taskID, "main", "commit", "fail"); err != nil {
		t.Fatalf("SetStepResult commit: %v", err)
	}

	v1, _, _ := Get(dir, taskID, "develop:implement:result")
	v2, _, _ := Get(dir, taskID, "main:commit:result")
	if v1 != "success" {
		t.Errorf("develop:implement:result = %q, want success", v1)
	}
	if v2 != "fail" {
		t.Errorf("main:commit:result = %q, want fail", v2)
	}
}

func TestCleanup_NonexistentDir(t *testing.T) {
	dir := t.TempDir()

	// Cleaning up a non-existent directory should not error
	if err := Cleanup(dir, "nonexistent-task"); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
}
