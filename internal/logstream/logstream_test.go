package logstream

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriter_Log(t *testing.T) {
	dir := t.TempDir()
	w, err := New(dir)
	require.NoError(t, err)
	defer w.Close()

	fixed := time.Date(2026, 3, 3, 10, 15, 0, 0, time.UTC)
	w.Now = func() time.Time { return fixed }

	w.Log(TypeStatus, "step_started: build")
	w.Log(TypeScript, "npm run build\nBuild successful")
	w.Log(TypeStatus, "step_completed: build -> done")
	w.Log(TypeLLM, "Claude: I'll start by reading the relevant files...")

	w.Close()

	data, err := os.ReadFile(filepath.Join(dir, ".cloche", "output", "full.log"))
	require.NoError(t, err)

	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	expected := []string{
		"[2026-03-03T10:15:00Z] [status] step_started: build",
		"[2026-03-03T10:15:00Z] [script] npm run build",
		"[2026-03-03T10:15:00Z] [script] Build successful",
		"[2026-03-03T10:15:00Z] [status] step_completed: build -> done",
		"[2026-03-03T10:15:00Z] [llm] Claude: I'll start by reading the relevant files...",
	}

	assert.Equal(t, expected, lines)
}

func TestWriter_EmptyMessage(t *testing.T) {
	dir := t.TempDir()
	w, err := New(dir)
	require.NoError(t, err)
	defer w.Close()

	w.Log(TypeStatus, "")
	w.Log(TypeStatus, "\n")

	w.Close()

	data, err := os.ReadFile(filepath.Join(dir, ".cloche", "output", "full.log"))
	require.NoError(t, err)
	assert.Empty(t, data)
}

func TestWriter_TrailingNewlineStripped(t *testing.T) {
	dir := t.TempDir()
	w, err := New(dir)
	require.NoError(t, err)
	defer w.Close()

	fixed := time.Date(2026, 3, 3, 10, 0, 0, 0, time.UTC)
	w.Now = func() time.Time { return fixed }

	w.Log(TypeScript, "hello world\n")

	w.Close()

	data, err := os.ReadFile(filepath.Join(dir, ".cloche", "output", "full.log"))
	require.NoError(t, err)

	assert.Equal(t, "[2026-03-03T10:00:00Z] [script] hello world\n", string(data))
}
