package intent_test

import (
	"context"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cloche-dev/cloche/internal/intent"
	"github.com/cloche-dev/cloche/internal/intent/embed"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scoreVector implements the shared stubEmbedder's vector func for semantic
// tests: item text carrying a "SCORE:<float>" marker embeds to a unit
// vector whose dot product with the fixed query vector [1,0] equals that
// float exactly; text without the marker (queries) embeds to [1,0] itself.
// This lets a table test dial in an exact cosine similarity per requirement
// without depending on Select's internal query composition.
func scoreVector(text string) []float32 {
	idx := strings.Index(text, "SCORE:")
	if idx == -1 {
		return []float32{1, 0}
	}
	rest := text[idx+len("SCORE:"):]
	if end := strings.IndexAny(rest, " \n"); end != -1 {
		rest = rest[:end]
	}
	score, err := strconv.ParseFloat(rest, 32)
	if err != nil {
		return []float32{0, 0}
	}
	s := float32(score)
	remainder := float64(1 - s*s)
	if remainder < 0 {
		remainder = 0
	}
	return []float32{s, float32(math.Sqrt(remainder))}
}

// syncedIndex builds an Index over dir, syncing one Item per requirement
// (via intent.EmbedText, the same composition Select's callers are expected
// to use) so semantic retrieval has something to score against.
func syncedIndex(t *testing.T, embedder embed.Embedder, reqs []*intent.Requirement) *intent.Index {
	t.Helper()
	ix, err := intent.NewIndex(t.TempDir(), embedder)
	require.NoError(t, err)

	items := make([]intent.Item, len(reqs))
	for i, r := range reqs {
		items[i] = intent.Item{ID: r.ID, Text: intent.EmbedText(r)}
	}
	require.NoError(t, ix.Sync(context.Background(), items))
	return ix
}

func projectReq(id string, status intent.Status, confidence intent.Confidence, updated time.Time) *intent.Requirement {
	return &intent.Requirement{
		ID:         id,
		Status:     status,
		Scope:      intent.Scope{Level: intent.ScopeLevelProject},
		Confidence: confidence,
		Updated:    updated,
		Body:       "project requirement " + id,
	}
}

func domainReq(id string, status intent.Status, domains []string, confidence intent.Confidence, updated time.Time, body string) *intent.Requirement {
	return &intent.Requirement{
		ID:         id,
		Status:     status,
		Scope:      intent.Scope{Level: intent.ScopeLevelDomain, Domains: domains},
		Confidence: confidence,
		Updated:    updated,
		Body:       body,
	}
}

var t0 = time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

func selectedIDs(res intent.Result) []string {
	ids := make([]string, len(res.Selected))
	for i, s := range res.Selected {
		ids[i] = s.Requirement.ID
	}
	return ids
}

func TestSelect_StatusFilter(t *testing.T) {
	reqs := []*intent.Requirement{
		projectReq("req-0001", intent.StatusActive, intent.ConfidenceHigh, t0),
		projectReq("req-0002", intent.StatusDisabled, intent.ConfidenceHigh, t0),
		projectReq("req-0003", intent.StatusSuperseded, intent.ConfidenceHigh, t0),
	}

	res, err := intent.Select(context.Background(), reqs, nil, nil, nil, intent.Query{}, intent.Options{})
	require.NoError(t, err)
	assert.Equal(t, []string{"req-0001"}, selectedIDs(res))
}

func TestSelect_DeterministicScope(t *testing.T) {
	domains := []intent.Domain{
		{Name: "versioning", Paths: []string{"internal/version/**"}},
	}

	tests := []struct {
		name  string
		query intent.Query
		want  []string // IDs expected among selected, order-independent for this test
	}{
		{
			name:  "project level always included",
			query: intent.Query{},
			want:  []string{"req-proj"},
		},
		{
			name:  "domain level excluded with no domain context",
			query: intent.Query{},
			want:  []string{"req-proj"},
		},
		{
			name:  "explicit domains key includes matching domain requirement",
			query: intent.Query{Domains: []string{"versioning"}},
			want:  []string{"req-proj", "req-dom"},
		},
		{
			name:  "repo path overlap includes matching domain requirement",
			query: intent.Query{Repos: []string{"internal/version"}},
			want:  []string{"req-proj", "req-dom"},
		},
		{
			name:  "repo path for a different domain does not include it",
			query: intent.Query{Repos: []string{"internal/dsl"}},
			want:  []string{"req-proj"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reqs := []*intent.Requirement{
				projectReq("req-proj", intent.StatusActive, intent.ConfidenceHigh, t0),
				domainReq("req-dom", intent.StatusActive, []string{"versioning"}, intent.ConfidenceHigh, t0, "versioning statement"),
			}
			res, err := intent.Select(context.Background(), reqs, domains, nil, nil, tt.query, intent.Options{})
			require.NoError(t, err)
			assert.ElementsMatch(t, tt.want, selectedIDs(res))
		})
	}
}

func TestSelect_SemanticTopK_CatchesOutOfScopeRequirement(t *testing.T) {
	stub := &stubEmbedder{modelID: "stub:v1", vector: scoreVector}
	reqs := []*intent.Requirement{
		domainReq("req-hit", intent.StatusActive, []string{"other-domain"}, intent.ConfidenceLow, t0, "SCORE:0.90 unrelated by scope, related by meaning"),
		domainReq("req-miss", intent.StatusActive, []string{"other-domain"}, intent.ConfidenceLow, t0, "SCORE:0.10 unrelated in every way"),
	}
	ix := syncedIndex(t, stub, reqs)

	// No repos/explicit domains: neither requirement matches deterministically.
	res, err := intent.Select(context.Background(), reqs, nil, ix, stub, intent.Query{TaskDescription: "task text"}, intent.Options{})
	require.NoError(t, err)
	assert.Equal(t, []string{"req-hit"}, selectedIDs(res), "only the requirement scoring above the default floor should surface")
}

func TestSelect_NoEmbedder_DegradesToDeterministicOnly(t *testing.T) {
	reqs := []*intent.Requirement{
		projectReq("req-proj", intent.StatusActive, intent.ConfidenceHigh, t0),
		domainReq("req-dom", intent.StatusActive, []string{"other"}, intent.ConfidenceHigh, t0, "SCORE:0.99 would match semantically"),
	}
	res, err := intent.Select(context.Background(), reqs, nil, nil, nil, intent.Query{TaskDescription: "task text"}, intent.Options{})
	require.NoError(t, err)
	assert.Equal(t, []string{"req-proj"}, selectedIDs(res), "with no embedder wired, only deterministic scope matches should surface")
}

func TestSelect_Ordering_ProjectFirstThenScoreDescending(t *testing.T) {
	stub := &stubEmbedder{modelID: "stub:v1", vector: scoreVector}
	reqs := []*intent.Requirement{
		domainReq("req-mid", intent.StatusActive, []string{"d"}, intent.ConfidenceHigh, t0, "SCORE:0.60 mid"),
		projectReq("req-proj", intent.StatusActive, intent.ConfidenceLow, t0),
		domainReq("req-high", intent.StatusActive, []string{"d"}, intent.ConfidenceHigh, t0, "SCORE:0.90 high"),
	}
	ix := syncedIndex(t, stub, reqs)

	res, err := intent.Select(context.Background(), reqs, nil, ix, stub, intent.Query{TaskDescription: "q"}, intent.Options{})
	require.NoError(t, err)
	assert.Equal(t, []string{"req-proj", "req-high", "req-mid"}, selectedIDs(res))
}

func TestSelect_Ordering_DeterministicOnlyByConfidenceThenRecency(t *testing.T) {
	domains := []intent.Domain{{Name: "d", Paths: []string{"pkg/**"}}}
	older := t0
	newer := t0.Add(24 * time.Hour)

	reqs := []*intent.Requirement{
		domainReq("req-low-new", intent.StatusActive, []string{"d"}, intent.ConfidenceLow, newer, "low confidence, newer"),
		domainReq("req-high-old", intent.StatusActive, []string{"d"}, intent.ConfidenceHigh, older, "high confidence, older"),
		domainReq("req-high-new", intent.StatusActive, []string{"d"}, intent.ConfidenceHigh, newer, "high confidence, newer"),
	}
	// No embedder: every match is deterministic-only, so ordering falls back
	// to confidence desc, then recency desc.
	res, err := intent.Select(context.Background(), reqs, domains, nil, nil, intent.Query{Repos: []string{"pkg"}}, intent.Options{})
	require.NoError(t, err)
	assert.Equal(t, []string{"req-high-new", "req-high-old", "req-low-new"}, selectedIDs(res))
}

func TestSelect_TokenBudget_TruncatesWithMarker(t *testing.T) {
	long := strings.Repeat("word ", 200)
	reqs := []*intent.Requirement{
		projectReq("req-0001", intent.StatusActive, intent.ConfidenceHigh, t0),
		{ID: "req-0002", Status: intent.StatusActive, Scope: intent.Scope{Level: intent.ScopeLevelProject}, Confidence: intent.ConfidenceHigh, Body: long},
		{ID: "req-0003", Status: intent.StatusActive, Scope: intent.Scope{Level: intent.ScopeLevelProject}, Confidence: intent.ConfidenceHigh, Body: long},
	}

	res, err := intent.Select(context.Background(), reqs, nil, nil, nil, intent.Query{}, intent.Options{TokenBudget: 30})
	require.NoError(t, err)
	assert.NotEmpty(t, res.Selected)
	assert.Less(t, len(res.Selected), len(reqs))
	assert.Positive(t, res.Omitted)

	block := intent.FormatBlock(res)
	assert.Contains(t, block, "more requirements omitted; run cloche intent list")
}

func TestSelect_KeywordDegradedMode_ProducesSameShape(t *testing.T) {
	kw := embed.NewKeywordEmbedder()
	reqs := []*intent.Requirement{
		domainReq("req-lexical", intent.StatusActive, []string{"other-domain"}, intent.ConfidenceMedium, t0,
			"cut a new release and publish the changelog"),
	}
	ix := syncedIndex(t, kw, reqs)

	res, err := intent.Select(context.Background(), reqs, nil, ix, kw, intent.Query{TaskDescription: "cut a new release and publish the changelog"}, intent.Options{})
	require.NoError(t, err)
	assert.Equal(t, []string{"req-lexical"}, selectedIDs(res), "keyword mode should still surface a lexically overlapping requirement")

	block := intent.FormatBlock(res)
	assert.Contains(t, block, "## Standing project requirements")
	assert.Contains(t, block, "[req-lexical]")
}
