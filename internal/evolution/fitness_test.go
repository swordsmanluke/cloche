package evolution_test

import (
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/evolution"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEvaluateRun(t *testing.T) {
	tests := []struct {
		name       string
		run        *domain.Run
		candidate  string
		wantSucc   bool
		wantRetry  int
		wantDurPos bool
	}{
		{
			name: "succeeded run no retries",
			run: &domain.Run{
				ID:           "r1",
				WorkflowName: "develop",
				State:        domain.RunStateSucceeded,
				StartedAt:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
				CompletedAt:  time.Date(2026, 1, 1, 0, 5, 0, 0, time.UTC),
				StepExecutions: []*domain.StepExecution{
					{StepName: "code"},
					{StepName: "test"},
				},
			},
			candidate:  "v1",
			wantSucc:   true,
			wantRetry:  0,
			wantDurPos: true,
		},
		{
			name: "failed run with retries",
			run: &domain.Run{
				ID:           "r2",
				WorkflowName: "develop",
				State:        domain.RunStateFailed,
				StartedAt:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
				CompletedAt:  time.Date(2026, 1, 1, 0, 10, 0, 0, time.UTC),
				StepExecutions: []*domain.StepExecution{
					{StepName: "code"},
					{StepName: "test"},
					{StepName: "code"}, // retry
					{StepName: "test"}, // retry
				},
			},
			candidate:  "v1",
			wantSucc:   false,
			wantRetry:  2,
			wantDurPos: true,
		},
		{
			name: "cancelled run zero duration",
			run: &domain.Run{
				ID:           "r3",
				WorkflowName: "develop",
				State:        domain.RunStateCancelled,
				StartedAt:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
				CompletedAt:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			},
			candidate:  "v2",
			wantSucc:   false,
			wantRetry:  0,
			wantDurPos: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := evolution.EvaluateRun(tt.run, tt.candidate)
			assert.Equal(t, tt.run.ID, rec.RunID)
			assert.Equal(t, tt.candidate, rec.Candidate)
			assert.Equal(t, tt.wantSucc, rec.Success)
			assert.Equal(t, tt.wantRetry, rec.RetryCount)
			if tt.wantDurPos {
				assert.Greater(t, rec.Duration, time.Duration(0))
			}
		})
	}
}

func TestRecordAndLoadFitness(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fitness.jsonl")

	// Load from nonexistent file returns nil
	records, err := evolution.LoadFitness(path)
	require.NoError(t, err)
	assert.Nil(t, records)

	// Write two records
	r1 := evolution.FitnessRecord{
		RunID:     "r1",
		Candidate: "v1",
		Success:   true,
		Duration:  5 * time.Minute,
	}
	r2 := evolution.FitnessRecord{
		RunID:      "r2",
		Candidate:  "v1",
		Success:    false,
		RetryCount: 2,
		Duration:   10 * time.Minute,
	}

	require.NoError(t, evolution.RecordFitness(path, r1))
	require.NoError(t, evolution.RecordFitness(path, r2))

	records, err = evolution.LoadFitness(path)
	require.NoError(t, err)
	require.Len(t, records, 2)
	assert.Equal(t, "r1", records[0].RunID)
	assert.True(t, records[0].Success)
	assert.Equal(t, "r2", records[1].RunID)
	assert.Equal(t, 2, records[1].RetryCount)
}

func TestAggregateFitness(t *testing.T) {
	records := []evolution.FitnessRecord{
		{Candidate: "a", Success: true, RetryCount: 0, Duration: 60 * time.Second},
		{Candidate: "a", Success: true, RetryCount: 0, Duration: 60 * time.Second},
		{Candidate: "b", Success: true, RetryCount: 0, Duration: 60 * time.Second},
		{Candidate: "b", Success: false, RetryCount: 2, Duration: 120 * time.Second},
	}

	agg := evolution.AggregateFitness(records)
	sort.Slice(agg, func(i, j int) bool { return agg[i].Candidate < agg[j].Candidate })

	require.Len(t, agg, 2)

	// Candidate "a": 2/2 success, 0 retries, 60s avg → efficiency = 1/(1+0+1) = 0.5
	assert.Equal(t, "a", agg[0].Candidate)
	assert.Equal(t, 1.0, agg[0].SuccessRate)
	assert.InDelta(t, 0.5, agg[0].Efficiency, 0.001)
	assert.Equal(t, 2, agg[0].RunCount)

	// Candidate "b": 1/2 success, avg retries=1, avg dur=90s → efficiency = 1/(1+1+1.5) = 1/3.5
	assert.Equal(t, "b", agg[1].Candidate)
	assert.Equal(t, 0.5, agg[1].SuccessRate)
	assert.InDelta(t, 1.0/3.5, agg[1].Efficiency, 0.001)
	assert.Equal(t, 2, agg[1].RunCount)
}

func TestDominates(t *testing.T) {
	tests := []struct {
		name string
		a, b evolution.CandidateFitness
		want bool
	}{
		{
			name: "a dominates b both better",
			a:    evolution.CandidateFitness{SuccessRate: 0.9, Efficiency: 0.8},
			b:    evolution.CandidateFitness{SuccessRate: 0.7, Efficiency: 0.5},
			want: true,
		},
		{
			name: "a dominates b equal success better efficiency",
			a:    evolution.CandidateFitness{SuccessRate: 0.9, Efficiency: 0.8},
			b:    evolution.CandidateFitness{SuccessRate: 0.9, Efficiency: 0.5},
			want: true,
		},
		{
			name: "no domination equal",
			a:    evolution.CandidateFitness{SuccessRate: 0.9, Efficiency: 0.8},
			b:    evolution.CandidateFitness{SuccessRate: 0.9, Efficiency: 0.8},
			want: false,
		},
		{
			name: "no domination tradeoff",
			a:    evolution.CandidateFitness{SuccessRate: 0.9, Efficiency: 0.3},
			b:    evolution.CandidateFitness{SuccessRate: 0.7, Efficiency: 0.8},
			want: false,
		},
		{
			name: "b dominates a",
			a:    evolution.CandidateFitness{SuccessRate: 0.5, Efficiency: 0.3},
			b:    evolution.CandidateFitness{SuccessRate: 0.9, Efficiency: 0.8},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, evolution.Dominates(tt.a, tt.b))
		})
	}
}

func TestComputeParetoFront(t *testing.T) {
	tests := []struct {
		name       string
		candidates []evolution.CandidateFitness
		wantFront  []string // expected candidate names in front
	}{
		{
			name: "single candidate is front",
			candidates: []evolution.CandidateFitness{
				{Candidate: "a", SuccessRate: 0.8, Efficiency: 0.5},
			},
			wantFront: []string{"a"},
		},
		{
			name: "one dominates the other",
			candidates: []evolution.CandidateFitness{
				{Candidate: "a", SuccessRate: 0.9, Efficiency: 0.8},
				{Candidate: "b", SuccessRate: 0.7, Efficiency: 0.5},
			},
			wantFront: []string{"a"},
		},
		{
			name: "tradeoff front",
			candidates: []evolution.CandidateFitness{
				{Candidate: "fast", SuccessRate: 0.6, Efficiency: 0.9},
				{Candidate: "reliable", SuccessRate: 0.95, Efficiency: 0.3},
				{Candidate: "dominated", SuccessRate: 0.5, Efficiency: 0.2},
			},
			wantFront: []string{"fast", "reliable"},
		},
		{
			name: "all on front",
			candidates: []evolution.CandidateFitness{
				{Candidate: "a", SuccessRate: 1.0, Efficiency: 0.1},
				{Candidate: "b", SuccessRate: 0.5, Efficiency: 0.5},
				{Candidate: "c", SuccessRate: 0.1, Efficiency: 1.0},
			},
			wantFront: []string{"a", "b", "c"},
		},
		{
			name: "complex front with interior points",
			candidates: []evolution.CandidateFitness{
				{Candidate: "p1", SuccessRate: 1.0, Efficiency: 0.2},
				{Candidate: "p2", SuccessRate: 0.8, Efficiency: 0.7},
				{Candidate: "p3", SuccessRate: 0.5, Efficiency: 0.9},
				{Candidate: "d1", SuccessRate: 0.7, Efficiency: 0.3}, // dominated by p2
				{Candidate: "d2", SuccessRate: 0.4, Efficiency: 0.5}, // dominated by p2
				{Candidate: "d3", SuccessRate: 0.3, Efficiency: 0.8}, // dominated by p3
			},
			wantFront: []string{"p1", "p2", "p3"},
		},
		{
			name:       "empty input",
			candidates: nil,
			wantFront:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			front := evolution.ComputeParetoFront(tt.candidates)

			var names []string
			for _, c := range front {
				names = append(names, c.Candidate)
			}
			sort.Strings(names)

			var expected []string
			if tt.wantFront != nil {
				expected = make([]string, len(tt.wantFront))
				copy(expected, tt.wantFront)
				sort.Strings(expected)
			}

			assert.Equal(t, expected, names)
		})
	}
}

func TestFitnessPath(t *testing.T) {
	got := evolution.FitnessPath("/home/user/project", "develop")
	assert.Equal(t, "/home/user/project/.cloche/evolution/fitness/develop.jsonl", got)
}
