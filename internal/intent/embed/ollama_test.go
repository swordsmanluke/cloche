package embed

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOllamaEmbedder_Embed_NormalizesAndReportsDimensions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/embed", r.URL.Path)

		var req ollamaEmbedRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, "all-minilm", req.Model)
		require.Len(t, req.Input, 2)

		json.NewEncoder(w).Encode(ollamaEmbedResponse{
			Embeddings: [][]float32{{3, 4}, {0, 0}},
		})
	}))
	defer srv.Close()

	e := NewOllamaEmbedder(srv.URL, "all-minilm")
	vecs, err := e.Embed(context.Background(), []string{"hello", "world"})
	require.NoError(t, err)
	require.Len(t, vecs, 2)

	// {3,4} normalizes to {0.6, 0.8}.
	assert.InDelta(t, 0.6, vecs[0][0], 0.0001)
	assert.InDelta(t, 0.8, vecs[0][1], 0.0001)
	// A zero vector stays zero rather than dividing by zero.
	assert.Equal(t, []float32{0, 0}, vecs[1])

	assert.Equal(t, 2, e.Dimensions())
	assert.Equal(t, "ollama:all-minilm", e.ModelID())
}

func TestOllamaEmbedder_Embed_MismatchedCountIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(ollamaEmbedResponse{Embeddings: [][]float32{{1}}})
	}))
	defer srv.Close()

	e := NewOllamaEmbedder(srv.URL, "all-minilm")
	_, err := e.Embed(context.Background(), []string{"one", "two"})
	assert.Error(t, err)
}

func TestOllamaEmbedder_Embed_NonOKStatusIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("boom"))
	}))
	defer srv.Close()

	e := NewOllamaEmbedder(srv.URL, "all-minilm")
	_, err := e.Embed(context.Background(), []string{"one"})
	assert.Error(t, err)
}

func TestOllamaAvailable_TrueWhenServerResponds(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/tags", r.URL.Path)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	t.Setenv("CLOCHE_OLLAMA_ADDR", srv.URL)
	assert.True(t, ollamaAvailable(context.Background()))
}

func TestOllamaAvailable_FalseWhenNothingListens(t *testing.T) {
	t.Setenv("CLOCHE_OLLAMA_ADDR", "http://127.0.0.1:1") // reserved, nothing listens
	assert.False(t, ollamaAvailable(context.Background()))
}

func TestNormalize_ZeroVectorUnchanged(t *testing.T) {
	v := []float32{0, 0, 0}
	normalize(v)
	assert.Equal(t, []float32{0, 0, 0}, v)
}

func TestNormalize_UnitLength(t *testing.T) {
	v := []float32{1, 2, 2}
	normalize(v)
	var sumSq float64
	for _, x := range v {
		sumSq += float64(x) * float64(x)
	}
	assert.InDelta(t, 1.0, math.Sqrt(sumSq), 0.0001)
}
