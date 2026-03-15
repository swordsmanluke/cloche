package evolution

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// AuditLogger records evolution actions and manages snapshots.
type AuditLogger struct {
	ProjectDir       string
	MaxPromptBullets int // 0 means unlimited
}

// Log appends an EvolutionResult as a JSONL entry.
func (a *AuditLogger) Log(result *EvolutionResult) error {
	logPath := filepath.Join(a.ProjectDir, ".cloche", "evolution", "log.jsonl")
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		return fmt.Errorf("creating evolution log dir: %w", err)
	}

	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("opening evolution log: %w", err)
	}
	defer f.Close()

	data, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("marshaling evolution result: %w", err)
	}

	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("writing evolution log: %w", err)
	}

	return nil
}

// Snapshot copies a file to the snapshots directory and returns the snapshot filename.
func (a *AuditLogger) Snapshot(relativePath string) (string, error) {
	srcPath := filepath.Join(a.ProjectDir, relativePath)
	snapDir := filepath.Join(a.ProjectDir, ".cloche", "evolution", "snapshots")
	if err := os.MkdirAll(snapDir, 0755); err != nil {
		return "", fmt.Errorf("creating snapshots dir: %w", err)
	}

	basename := filepath.Base(relativePath)
	snapName := fmt.Sprintf("%s-%s", time.Now().Format("20060102T150405"), basename)
	dstPath := filepath.Join(snapDir, snapName)

	src, err := os.Open(srcPath)
	if err != nil {
		return "", fmt.Errorf("opening source for snapshot: %w", err)
	}
	defer src.Close()

	dst, err := os.Create(dstPath)
	if err != nil {
		return "", fmt.Errorf("creating snapshot file: %w", err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return "", fmt.Errorf("copying to snapshot: %w", err)
	}

	return snapName, nil
}

// KnowledgePath returns the JSONL knowledge base path for a workflow.
func (a *AuditLogger) KnowledgePath(workflowName string) string {
	return filepath.Join(a.ProjectDir, ".cloche", "evolution", "knowledge", workflowName+".jsonl")
}

// readKnowledge reads existing lessons from the JSONL knowledge base.
func readKnowledge(path string) ([]Lesson, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var lessons []Lesson
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var l Lesson
		if err := json.Unmarshal([]byte(line), &l); err != nil {
			continue // skip malformed lines
		}
		lessons = append(lessons, l)
	}
	return lessons, nil
}

// writeKnowledge writes lessons as JSONL, one per line.
func writeKnowledge(path string, lessons []Lesson) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("creating knowledge dir: %w", err)
	}

	var sb strings.Builder
	for _, l := range lessons {
		data, err := json.Marshal(l)
		if err != nil {
			continue
		}
		sb.Write(data)
		sb.WriteByte('\n')
	}
	return os.WriteFile(path, []byte(sb.String()), 0644)
}

// UpdateKnowledge merges lessons into the knowledge base with deduplication
// and optional pruning when MaxPromptBullets is exceeded.
func (a *AuditLogger) UpdateKnowledge(workflowName string, lessons []Lesson) error {
	kbPath := a.KnowledgePath(workflowName)

	existing, err := readKnowledge(kbPath)
	if err != nil {
		return fmt.Errorf("reading knowledge base: %w", err)
	}

	// Build index of existing lesson IDs
	idIndex := make(map[string]int, len(existing))
	for i, l := range existing {
		idIndex[l.ID] = i
	}

	// Merge: update in place if ID exists, append if new
	for _, l := range lessons {
		if idx, ok := idIndex[l.ID]; ok {
			existing[idx] = l // update existing entry
		} else {
			existing = append(existing, l)
			idIndex[l.ID] = len(existing) - 1
		}
	}

	// Prune oldest entries if MaxPromptBullets is set and exceeded
	if a.MaxPromptBullets > 0 && len(existing) > a.MaxPromptBullets {
		existing = existing[len(existing)-a.MaxPromptBullets:]
	}

	return writeKnowledge(kbPath, existing)
}
