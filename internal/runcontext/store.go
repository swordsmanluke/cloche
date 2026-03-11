// Package runcontext provides a shared key-value store (context.json) for
// passing metadata between workflow steps within a single run.
package runcontext

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// ContextPath returns the path to context.json for a given project and run ID.
func ContextPath(projectDir, runID string) string {
	return filepath.Join(projectDir, ".cloche", runID, "context.json")
}

// mu serialises concurrent reads/writes within the same process.
var mu sync.Mutex

// Get retrieves a value from the run's context store.
// Returns ("", false, nil) if the key does not exist.
func Get(projectDir, runID, key string) (string, bool, error) {
	mu.Lock()
	defer mu.Unlock()

	m, err := load(ContextPath(projectDir, runID))
	if err != nil {
		return "", false, err
	}
	v, ok := m[key]
	return v, ok, nil
}

// Set writes a key-value pair to the run's context store, creating the file
// and parent directories if necessary.
func Set(projectDir, runID, key, value string) error {
	mu.Lock()
	defer mu.Unlock()

	path := ContextPath(projectDir, runID)

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
