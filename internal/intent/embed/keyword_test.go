package embed

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKeywordEmbedder_SimilarTextScoresHigherThanUnrelated(t *testing.T) {
	e := NewKeywordEmbedder()
	vecs, err := e.Embed(context.Background(), []string{
		"never bump the major version unless explicitly told",
		"do not bump the major version without explicit approval",
		"the dashboard renders a workflow DAG for each run",
	})
	require.NoError(t, err)
	require.Len(t, vecs, 3)

	simRelated := dot(vecs[0], vecs[1])
	simUnrelated := dot(vecs[0], vecs[2])
	assert.Greater(t, simRelated, simUnrelated)
}

func TestKeywordEmbedder_IdenticalTextScoresOne(t *testing.T) {
	e := NewKeywordEmbedder()
	vecs, err := e.Embed(context.Background(), []string{"same text here", "same text here"})
	require.NoError(t, err)
	assert.InDelta(t, 1.0, dot(vecs[0], vecs[1]), 0.0001)
}

func TestKeywordEmbedder_AlwaysAvailable(t *testing.T) {
	assert.True(t, always(true)(context.Background())) // sanity on the test helper
	c := registry[keywordName]
	require.NotNil(t, c.probe)
	assert.True(t, c.probe(context.Background()))
}

func TestTokenize_DropsStopwordsAndShortTokens(t *testing.T) {
	toks := tokenize("This is a test of the tokenizer, v2!")
	assert.NotContains(t, toks, "is")
	assert.NotContains(t, toks, "a")
	assert.NotContains(t, toks, "of")
	assert.Contains(t, toks, "test")
	assert.Contains(t, toks, "tokenizer")
	assert.Contains(t, toks, "v2")
}

func dot(a, b []float32) float32 {
	var s float32
	for i := range a {
		s += a[i] * b[i]
	}
	return s
}
