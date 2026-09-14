package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"sync"
	"time"
)

const (
	ollamaDefaultAddr  = "http://localhost:11434"
	ollamaDefaultModel = "all-minilm" // all-MiniLM-L6-v2, the design's proposed default
	ollamaProbeTimeout = 500 * time.Millisecond
	ollamaCallTimeout  = 30 * time.Second
)

func init() {
	Register("ollama", ollamaAvailable, func(ctx context.Context) (Embedder, error) {
		return NewOllamaEmbedder(ollamaAddr(), ollamaModel()), nil
	})
}

func ollamaAddr() string {
	if v := os.Getenv("CLOCHE_OLLAMA_ADDR"); v != "" {
		return v
	}
	return ollamaDefaultAddr
}

func ollamaModel() string {
	if v := os.Getenv("CLOCHE_OLLAMA_MODEL"); v != "" {
		return v
	}
	return ollamaDefaultModel
}

// ollamaAvailable probes the configured address with a short timeout so a
// down or absent Ollama server doesn't stall chain resolution.
func ollamaAvailable(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, ollamaProbeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ollamaAddr()+"/api/tags", nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// OllamaEmbedder calls a local Ollama server's /api/embed endpoint.
type OllamaEmbedder struct {
	addr   string
	model  string
	client *http.Client

	mu   sync.Mutex
	dims int // learned from the first successful Embed call; 0 until then
}

// NewOllamaEmbedder returns an adapter for the Ollama server at addr using
// model. Construction never contacts the server; use ollamaAvailable (via
// the registered chain) to probe first.
func NewOllamaEmbedder(addr, model string) *OllamaEmbedder {
	return &OllamaEmbedder{
		addr:   addr,
		model:  model,
		client: &http.Client{Timeout: ollamaCallTimeout},
	}
}

func (o *OllamaEmbedder) ModelID() string {
	return "ollama:" + o.model
}

func (o *OllamaEmbedder) Dimensions() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.dims
}

type ollamaEmbedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type ollamaEmbedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
}

func (o *OllamaEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	body, err := json.Marshal(ollamaEmbedRequest{Model: o.model, Input: texts})
	if err != nil {
		return nil, fmt.Errorf("intent: encoding ollama embed request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.addr+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("intent: building ollama embed request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("intent: calling ollama /api/embed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("intent: ollama /api/embed returned %s: %s", resp.Status, string(msg))
	}

	var out ollamaEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("intent: decoding ollama embed response: %w", err)
	}
	if len(out.Embeddings) != len(texts) {
		return nil, fmt.Errorf("intent: ollama returned %d embedding(s) for %d text(s)", len(out.Embeddings), len(texts))
	}

	for _, v := range out.Embeddings {
		normalize(v)
	}

	if len(out.Embeddings[0]) > 0 {
		o.mu.Lock()
		o.dims = len(out.Embeddings[0])
		o.mu.Unlock()
	}

	return out.Embeddings, nil
}

// normalize scales v in place to unit length. A zero vector is left as-is.
func normalize(v []float32) {
	var sumSq float64
	for _, x := range v {
		sumSq += float64(x) * float64(x)
	}
	n := math.Sqrt(sumSq)
	if n == 0 {
		return
	}
	for i := range v {
		v[i] = float32(float64(v[i]) / n)
	}
}
