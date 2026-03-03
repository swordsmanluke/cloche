package logstream

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// EntryType identifies the source of a log entry in the unified log.
type EntryType string

const (
	TypeStatus EntryType = "status"
	TypeScript EntryType = "script"
	TypeLLM    EntryType = "llm"
)

// Writer writes timestamped, type-prefixed entries to .cloche/output/full.log.
// It is safe for concurrent use.
type Writer struct {
	mu   sync.Mutex
	file *os.File
	Now  func() time.Time // overridable for testing
}

// New creates a Writer that appends to .cloche/output/full.log under workDir.
func New(workDir string) (*Writer, error) {
	dir := filepath.Join(workDir, ".cloche", "output")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("creating output directory: %w", err)
	}
	path := filepath.Join(dir, "full.log")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("opening full.log: %w", err)
	}
	return &Writer{file: f, Now: func() time.Time { return time.Now().UTC() }}, nil
}

// Log writes one or more lines to the unified log with the given type prefix.
// Multi-line messages are split into separate entries sharing the same timestamp.
func (w *Writer) Log(typ EntryType, message string) {
	trimmed := strings.TrimRight(message, "\n")
	if trimmed == "" {
		return
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	ts := w.Now().Format(time.RFC3339)
	for _, line := range strings.Split(trimmed, "\n") {
		fmt.Fprintf(w.file, "[%s] [%s] %s\n", ts, typ, line)
	}
}

// Close closes the underlying file.
func (w *Writer) Close() error {
	if w.file != nil {
		return w.file.Close()
	}
	return nil
}
