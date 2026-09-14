package embed

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func always(available bool) func(context.Context) bool {
	return func(context.Context) bool { return available }
}

func stubBuild(name string) func(context.Context) (Embedder, error) {
	return func(context.Context) (Embedder, error) {
		return &stubEmbedder{name: name}, nil
	}
}

type stubEmbedder struct{ name string }

func (s *stubEmbedder) Embed(context.Context, []string) ([][]float32, error) { return nil, nil }
func (s *stubEmbedder) ModelID() string                                      { return s.name }
func (s *stubEmbedder) Dimensions() int                                      { return 0 }

func TestResolve_FirstAvailableInOrderWins(t *testing.T) {
	reg := map[string]candidate{
		"onnx":    {probe: always(false), build: stubBuild("onnx")},
		"ollama":  {probe: always(true), build: stubBuild("ollama")},
		"keyword": {probe: always(true), build: stubBuild("keyword")},
	}

	e, err := resolve(context.Background(), []string{"onnx", "ollama", "keyword"}, reg, "")
	require.NoError(t, err)
	assert.Equal(t, "ollama", e.ModelID())
}

func TestResolve_FallsThroughToKeywordWhenEverythingElseIsDown(t *testing.T) {
	reg := map[string]candidate{
		"onnx":    {probe: always(false), build: stubBuild("onnx")},
		"ollama":  {probe: always(false), build: stubBuild("ollama")},
		"keyword": {probe: always(true), build: stubBuild("keyword")},
	}

	e, err := resolve(context.Background(), []string{"onnx", "ollama", "keyword"}, reg, "")
	require.NoError(t, err)
	assert.Equal(t, "keyword", e.ModelID())
}

func TestResolve_SkipsUnregisteredNames(t *testing.T) {
	// onnx has no registered candidate at all (matches the real registry
	// before the onnx build tag lands) — resolve must skip it, not error.
	reg := map[string]candidate{
		"ollama":  {probe: always(true), build: stubBuild("ollama")},
		"keyword": {probe: always(true), build: stubBuild("keyword")},
	}

	e, err := resolve(context.Background(), []string{"onnx", "ollama", "keyword"}, reg, "")
	require.NoError(t, err)
	assert.Equal(t, "ollama", e.ModelID())
}

func TestResolve_PinUsesNamedAdapterWhenAvailable(t *testing.T) {
	reg := map[string]candidate{
		"ollama":  {probe: always(true), build: stubBuild("ollama")},
		"keyword": {probe: always(true), build: stubBuild("keyword")},
	}

	e, err := resolve(context.Background(), []string{"ollama", "keyword"}, reg, "ollama")
	require.NoError(t, err)
	assert.Equal(t, "ollama", e.ModelID())
}

func TestResolve_PinFallsBackToKeywordWhenUnavailable(t *testing.T) {
	// A pin never blocks a run: if the pinned adapter is down, degrade to
	// keyword rather than erroring — but do not fall through to some other
	// non-pinned, non-keyword adapter.
	reg := map[string]candidate{
		"ollama":  {probe: always(false), build: stubBuild("ollama")},
		"keyword": {probe: always(true), build: stubBuild("keyword")},
	}

	e, err := resolve(context.Background(), []string{"ollama", "keyword"}, reg, "ollama")
	require.NoError(t, err)
	assert.Equal(t, "keyword", e.ModelID())
}

func TestResolve_UnknownPinIsAnError(t *testing.T) {
	reg := map[string]candidate{
		"keyword": {probe: always(true), build: stubBuild("keyword")},
	}

	_, err := resolve(context.Background(), []string{"keyword"}, reg, "nonexistent")
	assert.Error(t, err)
}

func TestResolve_NoAdapterAvailableIsAnError(t *testing.T) {
	reg := map[string]candidate{
		"ollama": {probe: always(false), build: stubBuild("ollama")},
	}

	_, err := resolve(context.Background(), []string{"ollama"}, reg, "")
	assert.Error(t, err)
}

// TestRealRegistry_KeywordAlwaysAvailable exercises the actual package-level
// Resolve (not the injectable resolve helper) to confirm ollama.go and
// keyword.go's init() registrations behave as documented: with no live
// Ollama server in this test environment, an unpinned Resolve still
// succeeds via keyword.
func TestRealRegistry_KeywordAlwaysAvailable(t *testing.T) {
	e, err := Resolve(context.Background(), "")
	require.NoError(t, err)
	assert.NotEmpty(t, e.ModelID())
}
