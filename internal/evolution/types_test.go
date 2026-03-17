package evolution

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCandidateJSON(t *testing.T) {
	c := Candidate{
		ID:         "c-1",
		PromptID:   "prompt-main",
		Content:    "You are a helpful assistant.",
		Generation: 2,
		ParentID:   "c-0",
		Fitness: FitnessScores{
			Total:   8.5,
			Count:   3,
			Average: 2.833,
		},
	}

	data, err := json.Marshal(c)
	require.NoError(t, err)

	var decoded Candidate
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, c.ID, decoded.ID)
	assert.Equal(t, c.PromptID, decoded.PromptID)
	assert.Equal(t, c.Content, decoded.Content)
	assert.Equal(t, c.Generation, decoded.Generation)
	assert.Equal(t, c.ParentID, decoded.ParentID)
	assert.Equal(t, c.Fitness.Total, decoded.Fitness.Total)
	assert.Equal(t, c.Fitness.Count, decoded.Fitness.Count)
	assert.Equal(t, c.Fitness.Average, decoded.Fitness.Average)
}

func TestCandidateJSONOmitsEmptyParent(t *testing.T) {
	c := Candidate{
		ID:       "c-root",
		PromptID: "prompt-main",
		Content:  "initial prompt",
	}

	data, err := json.Marshal(c)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "parent_id")
}

func TestCandidateGeneration(t *testing.T) {
	c := Candidate{
		ID:         "c-2",
		PromptID:   "prompt-main",
		Content:    "evolved prompt",
		Generation: 3,
		ParentID:   "c-1",
	}
	assert.Equal(t, 3, c.Generation)
	assert.Equal(t, "c-1", c.ParentID)
}

func TestFitnessRecordJSON(t *testing.T) {
	fr := FitnessRecord{
		CandidateID: "c-1",
		RunID:       "run-abc",
		Score:       0.95,
		Details:     "all tests passed",
	}

	data, err := json.Marshal(fr)
	require.NoError(t, err)

	var decoded FitnessRecord
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, fr.CandidateID, decoded.CandidateID)
	assert.Equal(t, fr.RunID, decoded.RunID)
	assert.Equal(t, fr.Score, decoded.Score)
	assert.Equal(t, fr.Details, decoded.Details)
}

func TestFitnessRecordOmitsEmptyDetails(t *testing.T) {
	fr := FitnessRecord{
		CandidateID: "c-1",
		RunID:       "run-abc",
		Score:       0.5,
	}

	data, err := json.Marshal(fr)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "details")
}

func TestFitnessScoresJSON(t *testing.T) {
	fs := FitnessScores{
		Total:   10.0,
		Count:   4,
		Average: 2.5,
		Scores:  map[string]float64{"accuracy": 0.9, "speed": 0.8},
	}

	data, err := json.Marshal(fs)
	require.NoError(t, err)

	var decoded FitnessScores
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, fs.Total, decoded.Total)
	assert.Equal(t, fs.Count, decoded.Count)
	assert.Equal(t, fs.Average, decoded.Average)
	assert.Equal(t, 0.9, decoded.Scores["accuracy"])
	assert.Equal(t, 0.8, decoded.Scores["speed"])
}

func TestFitnessScoresOmitsEmptyScoresMap(t *testing.T) {
	fs := FitnessScores{
		Total:   5.0,
		Count:   2,
		Average: 2.5,
	}

	data, err := json.Marshal(fs)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "scores")
}

func TestPopulationConfigJSON(t *testing.T) {
	pc := PopulationConfig{
		Enabled:          true,
		MaxCandidates:    10,
		MinRunsToPromote: 3,
	}

	data, err := json.Marshal(pc)
	require.NoError(t, err)

	var decoded PopulationConfig
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.True(t, decoded.Enabled)
	assert.Equal(t, 10, decoded.MaxCandidates)
	assert.Equal(t, 3, decoded.MinRunsToPromote)
}

func TestPopulationConfigDefaults(t *testing.T) {
	var pc PopulationConfig
	assert.False(t, pc.Enabled)
	assert.Equal(t, 0, pc.MaxCandidates)
	assert.Equal(t, 0, pc.MinRunsToPromote)
}
