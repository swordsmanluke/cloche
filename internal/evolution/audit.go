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
	ProjectDir string
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

// UpdateKnowledge appends lessons to the knowledge base file.
func (a *AuditLogger) UpdateKnowledge(workflowName string, lessons []Lesson) error {
	kbDir := filepath.Join(a.ProjectDir, ".cloche", "evolution", "knowledge")
	if err := os.MkdirAll(kbDir, 0755); err != nil {
		return fmt.Errorf("creating knowledge dir: %w", err)
	}

	kbPath := filepath.Join(kbDir, workflowName+".md")

	// Create with header if doesn't exist
	if _, err := os.Stat(kbPath); os.IsNotExist(err) {
		header := fmt.Sprintf("# Knowledge Base: %s workflow\n\n", workflowName)
		if err := os.WriteFile(kbPath, []byte(header), 0644); err != nil {
			return err
		}
	}

	f, err := os.OpenFile(kbPath, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("opening knowledge base: %w", err)
	}
	defer f.Close()

	for _, l := range lessons {
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("\n- **[%s]** (%s, confidence: %s) %s\n",
			l.ID, l.Category, l.Confidence, l.Insight))
		if l.SuggestedAction != "" {
			sb.WriteString(fmt.Sprintf("  _Action: %s_\n", l.SuggestedAction))
		}
		if len(l.Evidence) > 0 {
			sb.WriteString(fmt.Sprintf("  _Evidence: %s_\n", strings.Join(l.Evidence, ", ")))
		}
		if _, err := f.WriteString(sb.String()); err != nil {
			return err
		}
	}

	return nil
}
