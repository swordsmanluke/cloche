package intent_test

import (
	"context"
	"math"
	"testing"

	"github.com/cloche-dev/cloche/internal/intent"
	"github.com/cloche-dev/cloche/internal/intent/embed"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubEmbedder is a deterministic, callable-counting stand-in for a real
// Embedder: it maps each input string to a 1-D vector encoding a numeric
// value baked into the string itself, so tests can assert exact scores and
// exact call counts without a network dependency.
type stubEmbedder struct {
	modelID string
	calls   int
	vector  func(text string) []float32
}

func (s *stubEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	s.calls++
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = s.vector(t)
	}
	return out, nil
}

func (s *stubEmbedder) ModelID() string { return s.modelID }
func (s *stubEmbedder) Dimensions() int { return 2 }

// lengthVector maps a string to a unit-normalized 2-D vector derived from
// its length, mirroring the Embedder contract that vectors are already
// unit-normalized (index.cosine is a plain dot product, not a full cosine
// computation, relying on that contract).
func lengthVector(t string) []float32 {
	v := []float32{float32(len(t)), 1}
	n := float32(math.Sqrt(float64(v[0]*v[0] + v[1]*v[1])))
	return []float32{v[0] / n, v[1] / n}
}

func newStub(model string) *stubEmbedder {
	return &stubEmbedder{modelID: model, vector: lengthVector}
}

var _ embed.Embedder = (*stubEmbedder)(nil)

func TestIndex_SyncEmbedsNewItemsOnce(t *testing.T) {
	dir := t.TempDir()
	stub := newStub("stub:v1")
	ix, err := intent.NewIndex(dir, stub)
	require.NoError(t, err)

	items := []intent.Item{{ID: "a", Text: "hello"}, {ID: "b", Text: "world!!"}}
	require.NoError(t, ix.Sync(context.Background(), items))
	assert.Equal(t, 1, stub.calls, "one batch call for both new items")

	// Re-syncing unchanged items must not re-embed.
	require.NoError(t, ix.Sync(context.Background(), items))
	assert.Equal(t, 1, stub.calls, "unchanged content+model should not trigger re-embedding")
}

func TestIndex_SelfHealsOnContentEdit(t *testing.T) {
	dir := t.TempDir()
	stub := newStub("stub:v1")
	ix, err := intent.NewIndex(dir, stub)
	require.NoError(t, err)

	require.NoError(t, ix.Sync(context.Background(), []intent.Item{{ID: "a", Text: "original"}}))
	assert.Equal(t, 1, stub.calls)

	// Editing the text changes its content hash, so Sync must re-embed just
	// that item even though nothing else changed.
	require.NoError(t, ix.Sync(context.Background(), []intent.Item{{ID: "a", Text: "edited text"}}))
	assert.Equal(t, 2, stub.calls)

	matches := ix.TopK(lengthVector("edited text"), 1)
	require.Len(t, matches, 1)
	assert.InDelta(t, 1.0, matches[0].Score, 0.0001, "vector should reflect the edited text, not the original")
}

func TestIndex_SelfHealsOnModelSwap(t *testing.T) {
	dir := t.TempDir()
	stubV1 := newStub("stub:v1")
	ix, err := intent.NewIndex(dir, stubV1)
	require.NoError(t, err)

	items := []intent.Item{{ID: "a", Text: "hello"}}
	require.NoError(t, ix.Sync(context.Background(), items))
	assert.Equal(t, 1, stubV1.calls)

	// A model swap (new Index over the same directory, different ModelID)
	// must treat every persisted entry as stale even though content hasn't
	// changed, since the persisted vectors are no longer comparable.
	stubV2 := newStub("stub:v2")
	ix2, err := intent.NewIndex(dir, stubV2)
	require.NoError(t, err)
	require.NoError(t, ix2.Sync(context.Background(), items))
	assert.Equal(t, 1, stubV2.calls, "model swap forces a re-embed even with unchanged content")
}

func TestIndex_SyncDropsItemsNoLongerPresent(t *testing.T) {
	dir := t.TempDir()
	stub := newStub("stub:v1")
	ix, err := intent.NewIndex(dir, stub)
	require.NoError(t, err)

	require.NoError(t, ix.Sync(context.Background(), []intent.Item{
		{ID: "a", Text: "hello"},
		{ID: "b", Text: "world"},
	}))
	require.NoError(t, ix.Sync(context.Background(), []intent.Item{
		{ID: "a", Text: "hello"},
	}))

	matches := ix.TopK([]float32{0, 0}, 0)
	require.Len(t, matches, 1)
	assert.Equal(t, "a", matches[0].ID)
}

func TestIndex_PersistsAcrossLoads(t *testing.T) {
	dir := t.TempDir()
	stub := newStub("stub:v1")
	ix, err := intent.NewIndex(dir, stub)
	require.NoError(t, err)
	require.NoError(t, ix.Sync(context.Background(), []intent.Item{{ID: "a", Text: "hello"}}))

	// A fresh Index over the same directory and model must load the
	// persisted vector rather than re-embedding.
	reloaded, err := intent.NewIndex(dir, stub)
	require.NoError(t, err)
	require.NoError(t, reloaded.Sync(context.Background(), []intent.Item{{ID: "a", Text: "hello"}}))
	assert.Equal(t, 1, stub.calls, "reload should reuse the persisted vector, not re-embed")
}

func TestIndex_MissingIndexFileIsEmptyNotError(t *testing.T) {
	dir := t.TempDir()
	ix, err := intent.NewIndex(dir, newStub("stub:v1"))
	require.NoError(t, err)
	assert.Empty(t, ix.TopK([]float32{1, 0}, 0))
}

func TestIndex_TopKOrdersByScoreDescending(t *testing.T) {
	dir := t.TempDir()
	stub := newStub("stub:v1")
	ix, err := intent.NewIndex(dir, stub)
	require.NoError(t, err)

	require.NoError(t, ix.Sync(context.Background(), []intent.Item{
		{ID: "short", Text: "a"},
		{ID: "long", Text: "a much longer piece of text"},
		{ID: "medium", Text: "medium length text"},
	}))

	query := lengthVector("a much longer piece of text")
	matches := ix.TopK(query, 2)
	require.Len(t, matches, 2)
	assert.Equal(t, "long", matches[0].ID)
}

func TestIndex_EmbedQueryCachesPerText(t *testing.T) {
	dir := t.TempDir()
	stub := newStub("stub:v1")
	ix, err := intent.NewIndex(dir, stub)
	require.NoError(t, err)

	v1, err := ix.EmbedQuery(context.Background(), "what is the status")
	require.NoError(t, err)
	assert.Equal(t, 1, stub.calls)

	v2, err := ix.EmbedQuery(context.Background(), "what is the status")
	require.NoError(t, err)
	assert.Equal(t, 1, stub.calls, "repeated query text should hit the cache")
	assert.Equal(t, v1, v2)

	_, err = ix.EmbedQuery(context.Background(), "a different query")
	require.NoError(t, err)
	assert.Equal(t, 2, stub.calls)
}
