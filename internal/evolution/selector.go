package evolution

import "math/rand"

// Candidate represents a prompt variant in a GEPA population that can be
// selected for the next evolution cycle.
type Candidate struct {
	ID         string        `json:"id"`
	PromptID   string        `json:"prompt_id"`
	Content    string        `json:"content"`
	Generation int           `json:"generation"`
	ParentID   string        `json:"parent_id,omitempty"`
	Fitness    FitnessScores `json:"fitness"`
}

// FitnessScores holds multi-objective fitness values for a candidate.
type FitnessScores struct {
	Scores  map[string]float64 `json:"scores,omitempty"` // objective name -> score
	Total   float64            `json:"total"`
	Count   int                `json:"count"`
	Average float64            `json:"average"`
}

// SelectCandidate picks a candidate for the next evolution cycle.
// Strategy: 70% chance to pick a random candidate from the Pareto front,
// 30% chance to pick a random non-front candidate (exploration).
// Returns nil if no candidates exist.
func SelectCandidate(candidates []Candidate, fitness map[string]FitnessScores, paretoFront []string) *Candidate {
	if len(candidates) == 0 {
		return nil
	}

	// Build sets for fast lookup.
	frontSet := make(map[string]bool, len(paretoFront))
	for _, id := range paretoFront {
		frontSet[id] = true
	}

	var front, nonFront []Candidate
	for _, c := range candidates {
		if frontSet[c.ID] {
			front = append(front, c)
		} else {
			nonFront = append(nonFront, c)
		}
	}

	// If only one pool has candidates, pick from it.
	if len(front) == 0 && len(nonFront) == 0 {
		return nil
	}
	if len(front) == 0 {
		picked := nonFront[rand.Intn(len(nonFront))]
		return &picked
	}
	if len(nonFront) == 0 {
		picked := front[rand.Intn(len(front))]
		return &picked
	}

	// 70/30 split.
	if rand.Float64() < 0.7 {
		picked := front[rand.Intn(len(front))]
		return &picked
	}
	picked := nonFront[rand.Intn(len(nonFront))]
	return &picked
}
