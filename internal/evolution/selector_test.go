package evolution

import (
	"math"
	"testing"
)

func TestSelectCandidate_NilOnEmpty(t *testing.T) {
	got := SelectCandidate(nil, nil, nil)
	if got != nil {
		t.Fatalf("expected nil for empty candidates, got %+v", got)
	}

	got = SelectCandidate([]Candidate{}, map[string]FitnessScores{}, []string{})
	if got != nil {
		t.Fatalf("expected nil for empty slice, got %+v", got)
	}
}

func TestSelectCandidate_OnlyFrontCandidates(t *testing.T) {
	candidates := []Candidate{
		{ID: "a", PromptID: "p1", Content: "alpha"},
		{ID: "b", PromptID: "p2", Content: "beta"},
	}
	fitness := map[string]FitnessScores{
		"a": {Scores: map[string]float64{"speed": 0.9}},
		"b": {Scores: map[string]float64{"speed": 0.8}},
	}
	front := []string{"a", "b"}

	for i := 0; i < 50; i++ {
		got := SelectCandidate(candidates, fitness, front)
		if got == nil {
			t.Fatal("expected non-nil result")
		}
		if got.ID != "a" && got.ID != "b" {
			t.Fatalf("unexpected candidate ID: %s", got.ID)
		}
	}
}

func TestSelectCandidate_OnlyNonFrontCandidates(t *testing.T) {
	candidates := []Candidate{
		{ID: "x", PromptID: "p1", Content: "xray"},
	}
	fitness := map[string]FitnessScores{
		"x": {Scores: map[string]float64{"speed": 0.1}},
	}

	got := SelectCandidate(candidates, fitness, nil)
	if got == nil {
		t.Fatal("expected non-nil result")
	}
	if got.ID != "x" {
		t.Fatalf("expected x, got %s", got.ID)
	}
}

func TestSelectCandidate_Distribution(t *testing.T) {
	candidates := []Candidate{
		{ID: "f1", PromptID: "p1", Content: "front1"},
		{ID: "f2", PromptID: "p2", Content: "front2"},
		{ID: "n1", PromptID: "p3", Content: "nonfront1"},
		{ID: "n2", PromptID: "p4", Content: "nonfront2"},
	}
	fitness := map[string]FitnessScores{
		"f1": {Scores: map[string]float64{"speed": 0.9, "quality": 0.8}},
		"f2": {Scores: map[string]float64{"speed": 0.7, "quality": 0.95}},
		"n1": {Scores: map[string]float64{"speed": 0.5, "quality": 0.4}},
		"n2": {Scores: map[string]float64{"speed": 0.3, "quality": 0.6}},
	}
	front := []string{"f1", "f2"}

	const iterations = 10000
	frontCount := 0
	nonFrontCount := 0

	for i := 0; i < iterations; i++ {
		got := SelectCandidate(candidates, fitness, front)
		if got == nil {
			t.Fatal("expected non-nil result")
		}
		if got.ID == "f1" || got.ID == "f2" {
			frontCount++
		} else {
			nonFrontCount++
		}
	}

	frontRatio := float64(frontCount) / float64(iterations)
	nonFrontRatio := float64(nonFrontCount) / float64(iterations)

	// Expect ~70% front, ~30% non-front. Allow 5% tolerance.
	if math.Abs(frontRatio-0.7) > 0.05 {
		t.Errorf("front selection ratio %.3f outside expected 0.70 ± 0.05", frontRatio)
	}
	if math.Abs(nonFrontRatio-0.3) > 0.05 {
		t.Errorf("non-front selection ratio %.3f outside expected 0.30 ± 0.05", nonFrontRatio)
	}
}

func TestSelectCandidate_ReturnsCorrectCandidate(t *testing.T) {
	candidates := []Candidate{
		{ID: "only", PromptID: "p1", Content: "the only one"},
	}
	fitness := map[string]FitnessScores{
		"only": {Scores: map[string]float64{"speed": 0.5}},
	}
	front := []string{"only"}

	got := SelectCandidate(candidates, fitness, front)
	if got == nil {
		t.Fatal("expected non-nil")
	}
	if got.ID != "only" || got.Content != "the only one" {
		t.Fatalf("unexpected candidate: %+v", got)
	}
}

func TestSelectCandidate_CandidatesNotInFrontOrList(t *testing.T) {
	// Pareto front references IDs not in the candidates list — they should be ignored.
	candidates := []Candidate{
		{ID: "c1", PromptID: "p1", Content: "content1"},
	}
	fitness := map[string]FitnessScores{
		"c1": {Scores: map[string]float64{"speed": 0.5}},
	}
	front := []string{"nonexistent"}

	got := SelectCandidate(candidates, fitness, front)
	if got == nil {
		t.Fatal("expected non-nil")
	}
	if got.ID != "c1" {
		t.Fatalf("expected c1, got %s", got.ID)
	}
}
