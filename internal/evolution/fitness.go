package evolution

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cloche-dev/cloche/internal/domain"
)

// FitnessRecord holds metrics extracted from a single run evaluation.
type FitnessRecord struct {
	RunID        string        `json:"run_id"`
	WorkflowName string        `json:"workflow_name"`
	Candidate    string        `json:"candidate"`
	Success      bool          `json:"success"`
	RetryCount   int           `json:"retry_count"`
	Duration     time.Duration `json:"duration"`
	Timestamp    time.Time     `json:"timestamp"`
}

// CandidateFitness holds aggregated fitness for a candidate across runs.
type CandidateFitness struct {
	Candidate   string  `json:"candidate"`
	SuccessRate float64 `json:"success_rate"`
	Efficiency  float64 `json:"efficiency"`
	RunCount    int     `json:"run_count"`
}

// EvaluateRun extracts fitness metrics from a domain.Run.
// The candidate identifier groups runs for aggregation (e.g. a prompt version).
func EvaluateRun(r *domain.Run, candidate string) FitnessRecord {
	retries := 0
	seen := make(map[string]int)
	for _, exec := range r.StepExecutions {
		seen[exec.StepName]++
		if seen[exec.StepName] > 1 {
			retries++
		}
	}

	dur := r.CompletedAt.Sub(r.StartedAt)
	if dur < 0 {
		dur = 0
	}

	return FitnessRecord{
		RunID:        r.ID,
		WorkflowName: r.WorkflowName,
		Candidate:    candidate,
		Success:      r.State == domain.RunStateSucceeded,
		RetryCount:   retries,
		Duration:     dur,
		Timestamp:    r.CompletedAt,
	}
}

// RecordFitness appends a FitnessRecord to the JSONL file at path.
func RecordFitness(path string, record FitnessRecord) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("creating fitness dir: %w", err)
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("opening fitness file: %w", err)
	}
	defer f.Close()

	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshaling fitness record: %w", err)
	}

	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("writing fitness record: %w", err)
	}

	return nil
}

// LoadFitness reads all FitnessRecords from a JSONL file.
func LoadFitness(path string) ([]FitnessRecord, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading fitness file: %w", err)
	}

	var records []FitnessRecord
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var rec FitnessRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		records = append(records, rec)
	}
	return records, nil
}

// AggregateFitness groups records by candidate and computes success rate
// and efficiency for each. Efficiency is defined as 1 / (1 + avgRetries + avgDurationSeconds/60),
// giving higher values to faster candidates with fewer retries.
func AggregateFitness(records []FitnessRecord) []CandidateFitness {
	type accum struct {
		successes    int
		totalRetries int
		totalDur     time.Duration
		count        int
	}

	groups := make(map[string]*accum)
	for _, r := range records {
		a, ok := groups[r.Candidate]
		if !ok {
			a = &accum{}
			groups[r.Candidate] = a
		}
		a.count++
		if r.Success {
			a.successes++
		}
		a.totalRetries += r.RetryCount
		a.totalDur += r.Duration
	}

	results := make([]CandidateFitness, 0, len(groups))
	for candidate, a := range groups {
		avgRetries := float64(a.totalRetries) / float64(a.count)
		avgDurMin := a.totalDur.Seconds() / float64(a.count) / 60.0
		results = append(results, CandidateFitness{
			Candidate:   candidate,
			SuccessRate: float64(a.successes) / float64(a.count),
			Efficiency:  1.0 / (1.0 + avgRetries + avgDurMin),
			RunCount:    a.count,
		})
	}
	return results
}

// Dominates returns true if a dominates b in multi-objective comparison.
// a dominates b when a is at least as good in all objectives and strictly
// better in at least one. Both objectives (success rate, efficiency) are
// maximized.
func Dominates(a, b CandidateFitness) bool {
	atLeastAsGood := a.SuccessRate >= b.SuccessRate && a.Efficiency >= b.Efficiency
	strictlyBetter := a.SuccessRate > b.SuccessRate || a.Efficiency > b.Efficiency
	return atLeastAsGood && strictlyBetter
}

// ComputeParetoFront returns the non-dominated set from the given candidates.
// A candidate is non-dominated if no other candidate dominates it.
func ComputeParetoFront(candidates []CandidateFitness) []CandidateFitness {
	var front []CandidateFitness
	for i := range candidates {
		dominated := false
		for j := range candidates {
			if i == j {
				continue
			}
			if Dominates(candidates[j], candidates[i]) {
				dominated = true
				break
			}
		}
		if !dominated {
			front = append(front, candidates[i])
		}
	}
	return front
}

// FitnessPath returns the conventional JSONL fitness file path for a workflow.
func FitnessPath(projectDir, workflowName string) string {
	return filepath.Join(projectDir, ".cloche", "evolution", "fitness", workflowName+".jsonl")
}
