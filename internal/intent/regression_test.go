package intent_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/cloche-dev/cloche/internal/intent"
	"github.com/cloche-dev/cloche/internal/intent/embed"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// spikeRequirement and spikeQuery mirror the JSON shape of the corpus
// produced by the 2026-09-13 retrieval spike
// (docs/plans/spikes/2026-09-13-intent-retrieval/corpus.json) — not
// intent.Requirement, which didn't exist yet when the corpus was built.
type spikeRequirement struct {
	ID     string `json:"id"`
	Domain string `json:"domain"`
	Text   string `json:"text"`
}

type spikeQuery struct {
	ID       string   `json:"id"`
	Text     string   `json:"text"`
	Relevant []string `json:"relevant"`
}

type spikeCorpus struct {
	Requirements []spikeRequirement `json:"requirements"`
	Queries      []spikeQuery       `json:"queries"`
}

// minHitAt3 is a regression floor, not a target: the 2026-09-13 spike
// measured hit@3 (top-3 contains at least one relevant requirement) of
// 11/14 ≈ 0.79 for the all-minilm adapter with no retrieval hints — the
// same configuration this test exercises (index.go has no hints/select.go
// composition yet). The bar sits well below that to absorb Ollama version
// drift in the "all-minilm" model itself, while still catching an adapter
// or index regression that meaningfully breaks retrieval.
const minHitAt3 = 0.6

// TestRegression_SpikeCorpus_HitAt3 validates that semantic retrieval
// through the real Index + a live embedder still clears the spike's
// measured floor. It is gated on a live embedder — Ollama serving
// "all-minilm" here, since the onnx adapter doesn't exist until slice 3 —
// and skips otherwise, per the design's "run only when a real embedder is
// available" acceptance criterion.
func TestRegression_SpikeCorpus_HitAt3(t *testing.T) {
	ctx := context.Background()
	if !embed.Available(ctx, "ollama") {
		t.Skip("no live embedder available: ollama is not reachable on this machine")
	}
	embedder, err := embed.Resolve(ctx, "ollama")
	require.NoError(t, err)

	corpus := loadSpikeCorpus(t)

	ix, err := intent.NewIndex(t.TempDir(), embedder)
	require.NoError(t, err)

	items := make([]intent.Item, len(corpus.Requirements))
	for i, r := range corpus.Requirements {
		items[i] = intent.Item{ID: r.ID, Text: r.Domain + ": " + r.Text}
	}
	require.NoError(t, ix.Sync(ctx, items))

	hits := 0
	for _, q := range corpus.Queries {
		qVec, err := ix.EmbedQuery(ctx, q.Text)
		require.NoError(t, err)

		relevant := make(map[string]bool, len(q.Relevant))
		for _, id := range q.Relevant {
			relevant[id] = true
		}

		for _, m := range ix.TopK(qVec, 3) {
			if relevant[m.ID] {
				hits++
				break
			}
		}
	}

	hitRate := float64(hits) / float64(len(corpus.Queries))
	assert.GreaterOrEqualf(t, hitRate, minHitAt3,
		"hit@3 %.2f (%d/%d) fell below the regression floor %.2f", hitRate, hits, len(corpus.Queries), minHitAt3)
}

func loadSpikeCorpus(t *testing.T) spikeCorpus {
	t.Helper()

	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller failed to resolve the test file's own path")
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	path := filepath.Join(repoRoot, "docs", "plans", "spikes", "2026-09-13-intent-retrieval", "corpus.json")

	data, err := os.ReadFile(path)
	require.NoError(t, err, "reading spike corpus fixture")

	var corpus spikeCorpus
	require.NoError(t, json.Unmarshal(data, &corpus))
	require.NotEmpty(t, corpus.Requirements)
	require.NotEmpty(t, corpus.Queries)
	return corpus
}
