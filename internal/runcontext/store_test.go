package runcontext

import (
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
