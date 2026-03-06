package beads

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cloche-dev/cloche/internal/ports"
)

// issue is the on-disk representation of a beads issue.
type issue struct {
	ID           string       `json:"id"`
	Title        string       `json:"title"`
	Description  string       `json:"description"`
	Status       string       `json:"status"`
	Priority     int          `json:"priority"`
	IssueType    string       `json:"issue_type"`
	Labels       []string     `json:"labels"`
	Dependencies []dependency `json:"dependencies,omitempty"`
	CreatedAt    time.Time    `json:"created_at"`
	UpdatedAt    time.Time    `json:"updated_at"`
	ClosedAt     time.Time    `json:"closed_at,omitempty"`
	CloseReason  string       `json:"close_reason,omitempty"`
	CreatedBy    string       `json:"created_by"`
}

type dependency struct {
	IssueID     string `json:"issue_id"`
	DependsOnID string `json:"depends_on_id"`
	Type        string `json:"type"`
}

// Tracker implements ports.TaskTracker backed by .beads/issues.jsonl.
// Each Tracker is scoped to a single project directory.
type Tracker struct {
	projectDir string
	mu         sync.Mutex
}

// NewTracker creates a new Beads tracker for the given project directory.
func NewTracker(projectDir string) *Tracker {
	return &Tracker{projectDir: projectDir}
}

// ListReady returns open tasks whose blocking dependencies are all closed,
// ordered by priority (highest first).
func (t *Tracker) ListReady(_ context.Context, project string) ([]ports.TrackerTask, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	issues, err := readIssuesFromFile(t.beadsPath())
	if err != nil {
		return nil, err
	}

	// Build a status lookup for dependency checking.
	statusByID := make(map[string]string, len(issues))
	for _, iss := range issues {
		statusByID[iss.ID] = iss.Status
	}

	var tasks []ports.TrackerTask
	for _, iss := range issues {
		if iss.Status != "open" {
			continue
		}
		if hasOpenBlockers(iss, statusByID) {
			continue
		}
		tasks = append(tasks, ports.TrackerTask{
			ID:          iss.ID,
			Title:       iss.Title,
			Description: iss.Description,
			Labels:      iss.Labels,
			Priority:    iss.Priority,
		})
	}

	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].Priority > tasks[j].Priority
	})

	return tasks, nil
}

// hasOpenBlockers returns true if any of the issue's dependencies are not closed.
func hasOpenBlockers(iss issue, statusByID map[string]string) bool {
	for _, dep := range iss.Dependencies {
		depStatus := statusByID[dep.DependsOnID]
		if depStatus != "closed" {
			return true
		}
	}
	return false
}

// Claim marks a task as "in_progress" by appending an updated line to the JSONL file.
func (t *Tracker) Claim(_ context.Context, taskID string) error {
	return t.appendStatusUpdate(taskID, "in_progress")
}

// Complete marks a task as "closed" by appending an updated line to the JSONL file.
func (t *Tracker) Complete(_ context.Context, taskID string) error {
	return t.appendStatusUpdate(taskID, "closed")
}

// Fail marks a task back to "open" so it can be retried.
func (t *Tracker) Fail(_ context.Context, taskID string) error {
	return t.appendStatusUpdate(taskID, "open")
}

// appendStatusUpdate reads the current issue, updates its status, and appends
// the updated version to the JSONL file.
func (t *Tracker) appendStatusUpdate(taskID, newStatus string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	path := t.beadsPath()
	issues, err := readIssuesFromFile(path)
	if err != nil {
		return err
	}

	var target *issue
	for i := range issues {
		if issues[i].ID == taskID {
			target = &issues[i]
			break
		}
	}
	if target == nil {
		return fmt.Errorf("task %q not found in %s", taskID, path)
	}

	target.Status = newStatus
	target.UpdatedAt = time.Now()
	if newStatus == "closed" {
		now := time.Now()
		target.ClosedAt = now
	}

	data, err := json.Marshal(target)
	if err != nil {
		return fmt.Errorf("marshaling issue: %w", err)
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("opening beads file: %w", err)
	}
	defer f.Close()

	if _, err := f.WriteString("\n" + string(data)); err != nil {
		return fmt.Errorf("appending to beads file: %w", err)
	}

	return nil
}

func (t *Tracker) beadsPath() string {
	return filepath.Join(t.projectDir, ".beads", "issues.jsonl")
}

func readIssuesFromFile(path string) ([]issue, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading beads file: %w", err)
	}

	// JSONL: each issue can appear multiple times; last occurrence wins.
	seen := make(map[string]*issue)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var iss issue
		if err := json.Unmarshal([]byte(line), &iss); err != nil {
			continue // skip malformed lines
		}
		if iss.Status == "tombstone" {
			delete(seen, iss.ID)
			continue
		}
		cp := iss
		seen[iss.ID] = &cp
	}

	result := make([]issue, 0, len(seen))
	for _, iss := range seen {
		result = append(result, *iss)
	}
	return result, nil
}
