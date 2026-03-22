// Package runcontext provides path helpers for per-task runtime files
// (prompt.txt, etc.) stored under .cloche/runs/<taskID>/.
// The key-value store previously hosted here has moved to the daemon's
// gRPC-backed SQLite KV store (GetContextKey / SetContextKey RPCs).
package runcontext

import (
	"os"
	"path/filepath"
)

// ContextPath returns the path to context.json for a given project and task ID.
// Kept for backward compatibility; prefer the gRPC KV store for new code.
func ContextPath(projectDir, taskID string) string {
	return filepath.Join(projectDir, ".cloche", "runs", taskID, "context.json")
}

// RunDir returns the .cloche/runs/<taskID> directory for a given project.
func RunDir(projectDir, taskID string) string {
	return filepath.Join(projectDir, ".cloche", "runs", taskID)
}

// PromptPath returns the path to prompt.txt for a given project and task ID.
func PromptPath(projectDir, taskID string) string {
	return filepath.Join(projectDir, ".cloche", "runs", taskID, "prompt.txt")
}

// Cleanup removes the .cloche/runs/<taskID> directory and all its contents.
// Deprecated: use store.DeleteContextKeys + os.RemoveAll(RunDir(...)) directly.
func Cleanup(projectDir, taskID string) error {
	return os.RemoveAll(RunDir(projectDir, taskID))
}
