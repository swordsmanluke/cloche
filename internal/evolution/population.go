package evolution

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Population manages candidate prompt variants for a single workflow step.
// Disk layout:
//
//	.cloche/evolution/population/<step-name>/
//	  candidate-001.md
//	  candidate-002.md
//	  meta.jsonl
//	  assignments.jsonl
type Population struct {
	ProjectDir string
	StepName   string
}

// CandidateMeta is one JSONL entry in meta.jsonl.
type CandidateMeta struct {
	ID        string  `json:"id"`        // e.g. "candidate-001"
	Status    string  `json:"status"`    // "active", "archived", "promoted"
	CreatedAt string  `json:"created_at"`
	ParentID  string  `json:"parent_id,omitempty"` // ID of the candidate this was derived from
	Score     float64 `json:"score,omitempty"`
}

// Assignment maps a workflow run to the candidate it used.
type Assignment struct {
	RunID       string `json:"run_id"`
	CandidateID string `json:"candidate_id"`
	AssignedAt  string `json:"assigned_at"`
}

const (
	candidateStatusActive   = "active"
	candidateStatusArchived = "archived"
	candidateStatusPromoted = "promoted"
)

// populationDir returns the directory for this step's population.
func (p *Population) populationDir() string {
	return filepath.Join(p.ProjectDir, ".cloche", "evolution", "population", p.StepName)
}

func (p *Population) metaPath() string {
	return filepath.Join(p.populationDir(), "meta.jsonl")
}

func (p *Population) assignmentsPath() string {
	return filepath.Join(p.populationDir(), "assignments.jsonl")
}

func (p *Population) candidatePath(id string) string {
	return filepath.Join(p.populationDir(), id+".md")
}

// AddCandidate writes a new candidate prompt file and appends its metadata.
// Returns the assigned candidate ID (e.g. "candidate-003").
func (p *Population) AddCandidate(content string, parentID string) (string, error) {
	dir := p.populationDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("creating population dir: %w", err)
	}

	// Determine next candidate number from existing meta.
	all, err := p.readMeta()
	if err != nil {
		return "", fmt.Errorf("reading meta: %w", err)
	}

	nextNum := len(all) + 1
	id := fmt.Sprintf("candidate-%03d", nextNum)

	// Write prompt file.
	if err := os.WriteFile(p.candidatePath(id), []byte(content), 0644); err != nil {
		return "", fmt.Errorf("writing candidate file: %w", err)
	}

	// Append meta entry.
	meta := CandidateMeta{
		ID:        id,
		Status:    candidateStatusActive,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		ParentID:  parentID,
	}
	if err := p.appendMeta(meta); err != nil {
		return "", fmt.Errorf("appending meta: %w", err)
	}

	return id, nil
}

// ListCandidates returns metadata for all active candidates.
func (p *Population) ListCandidates() ([]CandidateMeta, error) {
	all, err := p.readMeta()
	if err != nil {
		return nil, fmt.Errorf("reading meta: %w", err)
	}

	var active []CandidateMeta
	for _, m := range all {
		if m.Status == candidateStatusActive {
			active = append(active, m)
		}
	}
	return active, nil
}

// GetContent reads the prompt content for a candidate.
func (p *Population) GetContent(candidateID string) (string, error) {
	data, err := os.ReadFile(p.candidatePath(candidateID))
	if err != nil {
		return "", fmt.Errorf("reading candidate %s: %w", candidateID, err)
	}
	return string(data), nil
}

// Promote snapshots the current base prompt, copies the winner as the new base,
// and archives all other active candidates.
func (p *Population) Promote(winnerID string, basePath string) error {
	all, err := p.readMeta()
	if err != nil {
		return fmt.Errorf("reading meta: %w", err)
	}

	// Verify winner exists and is active.
	found := false
	for _, m := range all {
		if m.ID == winnerID && m.Status == candidateStatusActive {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("candidate %s not found or not active", winnerID)
	}

	// Snapshot the current base prompt.
	snapDir := filepath.Join(p.ProjectDir, ".cloche", "evolution", "snapshots")
	if err := os.MkdirAll(snapDir, 0755); err != nil {
		return fmt.Errorf("creating snapshots dir: %w", err)
	}

	absBase := basePath
	if !filepath.IsAbs(basePath) {
		absBase = filepath.Join(p.ProjectDir, basePath)
	}

	baseData, err := os.ReadFile(absBase)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading base prompt: %w", err)
	}

	if err == nil {
		snapName := fmt.Sprintf("%s-%s-base%s",
			time.Now().UTC().Format("20060102T150405"),
			p.StepName,
			filepath.Ext(basePath),
		)
		if err := os.WriteFile(filepath.Join(snapDir, snapName), baseData, 0644); err != nil {
			return fmt.Errorf("writing snapshot: %w", err)
		}
	}

	// Copy winner content to base path.
	winnerContent, err := p.GetContent(winnerID)
	if err != nil {
		return fmt.Errorf("reading winner content: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(absBase), 0755); err != nil {
		return fmt.Errorf("creating base dir: %w", err)
	}
	if err := os.WriteFile(absBase, []byte(winnerContent), 0644); err != nil {
		return fmt.Errorf("writing promoted content to base: %w", err)
	}

	// Update statuses: winner -> promoted, others -> archived.
	for i := range all {
		if all[i].Status != candidateStatusActive {
			continue
		}
		if all[i].ID == winnerID {
			all[i].Status = candidateStatusPromoted
		} else {
			all[i].Status = candidateStatusArchived
		}
	}

	return p.writeMeta(all)
}

// RecordAssignment tracks which candidate was used for a workflow run.
func (p *Population) RecordAssignment(runID, candidateID string) error {
	dir := p.populationDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating population dir: %w", err)
	}

	a := Assignment{
		RunID:       runID,
		CandidateID: candidateID,
		AssignedAt:  time.Now().UTC().Format(time.RFC3339),
	}

	data, err := json.Marshal(a)
	if err != nil {
		return fmt.Errorf("marshaling assignment: %w", err)
	}

	f, err := os.OpenFile(p.assignmentsPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("opening assignments file: %w", err)
	}
	defer f.Close()

	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("writing assignment: %w", err)
	}

	return nil
}

// GetAssignment returns the candidate ID assigned to a run, or empty string if none.
func (p *Population) GetAssignment(runID string) (string, error) {
	data, err := os.ReadFile(p.assignmentsPath())
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("reading assignments: %w", err)
	}

	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var a Assignment
		if err := json.Unmarshal([]byte(line), &a); err != nil {
			continue
		}
		if a.RunID == runID {
			return a.CandidateID, nil
		}
	}

	return "", nil
}

// readMeta reads all entries from meta.jsonl.
func (p *Population) readMeta() ([]CandidateMeta, error) {
	data, err := os.ReadFile(p.metaPath())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var entries []CandidateMeta
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var m CandidateMeta
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			continue
		}
		entries = append(entries, m)
	}
	return entries, nil
}

// appendMeta appends a single meta entry to meta.jsonl.
func (p *Population) appendMeta(meta CandidateMeta) error {
	data, err := json.Marshal(meta)
	if err != nil {
		return err
	}

	f, err := os.OpenFile(p.metaPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.Write(append(data, '\n'))
	return err
}

// writeMeta overwrites meta.jsonl with the given entries.
func (p *Population) writeMeta(entries []CandidateMeta) error {
	if err := os.MkdirAll(p.populationDir(), 0755); err != nil {
		return fmt.Errorf("creating population dir: %w", err)
	}

	var sb strings.Builder
	for _, m := range entries {
		data, err := json.Marshal(m)
		if err != nil {
			continue
		}
		sb.Write(data)
		sb.WriteByte('\n')
	}
	return os.WriteFile(p.metaPath(), []byte(sb.String()), 0644)
}
