// Package runcontext provides a shared key-value store (context.json) for
// passing metadata between workflow steps within a single run.
// Files are stored under .cloche/runs/<taskID>/context.json and are
// ephemeral — cleaned up by the host runner after the run completes.
package runcontext

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// ContextPath returns the path to context.json for a given project and task ID.
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
func Cleanup(projectDir, taskID string) error {
	dir := RunDir(projectDir, taskID)
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("cleaning up run directory %s: %w", dir, err)
	}
	return nil
}

// mu serialises concurrent reads/writes within the same process.
var mu sync.Mutex

// Get retrieves a value from the task's context store.
// Returns ("", false, nil) if the key does not exist.
func Get(projectDir, taskID, key string) (string, bool, error) {
	mu.Lock()
	defer mu.Unlock()

	m, err := load(ContextPath(projectDir, taskID))
	if err != nil {
		return "", false, err
	}
	v, ok := m[key]
	return v, ok, nil
}

// Set writes a key-value pair to the task's context store, creating the file
// and parent directories if necessary.
func Set(projectDir, taskID, key, value string) error {
	mu.Lock()
	defer mu.Unlock()

	path := ContextPath(projectDir, taskID)

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("creating context directory: %w", err)
	}

	m, err := load(path)
	if err != nil {
		return err
	}
	m[key] = value
	return save(path, m)
}

// load reads the context map from disk. Returns an empty map if the file does
// not exist.
func load(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]string), nil
		}
		return nil, fmt.Errorf("reading context file: %w", err)
	}
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parsing context file: %w", err)
	}
	return m, nil
}

// save writes the context map to disk as indented JSON.
func save(path string, m map[string]string) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding context: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("writing context file: %w", err)
	}
	return nil
}
