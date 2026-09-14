package intent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/cloche-dev/cloche/internal/intent/embed"
)

// Item is one unit of text to embed and index: a requirement's composed
// embedding text (statement + rationale + hints, domain-prefixed — select.go
// owns that composition) or, in tests, a corpus fixture entry. Index itself
// is agnostic to what ID and Text mean.
type Item struct {
	ID   string
	Text string
}

// Match is one scored result from Index.TopK.
type Match struct {
	ID    string
	Score float32
}

// indexEntry is the persisted record for one Item: its vector plus enough
// metadata (content hash, model ID) to detect staleness without re-reading
// the source text.
type indexEntry struct {
	ID          string    `json:"id"`
	ContentHash string    `json:"content_hash"`
	ModelID     string    `json:"model_id"`
	Vector      []float32 `json:"vector"`
}

type indexFile struct {
	Version int          `json:"version"`
	Entries []indexEntry `json:"entries"`
}

// Index is a flat-file, self-healing embedding index. It is a derived
// artifact under .cloche/intent-index/ — never committed, never a second
// source of truth for requirement content — that caches one vector per
// Item.ID. Sync re-embeds any item whose content hash or model ID no longer
// matches the stored entry, so edits, scan output, fresh clones, and model
// swaps all self-heal with no migration step. Corpus size is expected to be
// hundreds of requirements at most, so TopK is brute-force cosine similarity
// over the in-memory vectors.
type Index struct {
	dir      string // project root, not .cloche/intent-index/ itself
	embedder embed.Embedder

	mu         sync.Mutex
	entries    map[string]indexEntry
	queryCache map[string][]float32 // per-run cache of query text -> vector
}

// NewIndex loads (or, if absent, starts empty for) the index under
// projectDir/.cloche/intent-index/. A missing or unreadable-as-this-version
// index file is treated as empty rather than an error — the whole point of
// content-hash/model-ID tracked entries is that a missing cache just means
// everything gets re-embedded on the next Sync.
func NewIndex(projectDir string, embedder embed.Embedder) (*Index, error) {
	ix := &Index{
		dir:        projectDir,
		embedder:   embedder,
		entries:    make(map[string]indexEntry),
		queryCache: make(map[string][]float32),
	}

	data, err := os.ReadFile(ix.path())
	if err != nil {
		if os.IsNotExist(err) {
			return ix, nil
		}
		return nil, fmt.Errorf("intent: reading index: %w", err)
	}

	var f indexFile
	if err := json.Unmarshal(data, &f); err != nil {
		// A corrupt index is recoverable by re-embedding, not fatal.
		return ix, nil
	}
	for _, e := range f.Entries {
		ix.entries[e.ID] = e
	}
	return ix, nil
}

func (ix *Index) path() string {
	return filepath.Join(ix.dir, ".cloche", "intent-index", "vectors.json")
}

func contentHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// Sync ensures every item in items has an up-to-date vector in the index:
// unchanged items (matching content hash and the embedder's current model
// ID) are kept as-is, everything else (new items, edited text, or a model
// swap) is (re-)embedded in one batch call. Items no longer present in the
// slice are dropped from the index. Persists to disk before returning.
func (ix *Index) Sync(ctx context.Context, items []Item) error {
	modelID := ix.embedder.ModelID()

	ix.mu.Lock()
	fresh := make(map[string]indexEntry, len(items))
	var stale []Item
	for _, it := range items {
		hash := contentHash(it.Text)
		if e, ok := ix.entries[it.ID]; ok && e.ContentHash == hash && e.ModelID == modelID {
			fresh[it.ID] = e
			continue
		}
		stale = append(stale, it)
	}
	ix.mu.Unlock()

	if len(stale) > 0 {
		texts := make([]string, len(stale))
		for i, it := range stale {
			texts[i] = it.Text
		}
		vecs, err := ix.embedder.Embed(ctx, texts)
		if err != nil {
			return fmt.Errorf("intent: embedding %d item(s): %w", len(stale), err)
		}
		if len(vecs) != len(stale) {
			return fmt.Errorf("intent: embedder returned %d vector(s) for %d item(s)", len(vecs), len(stale))
		}
		for i, it := range stale {
			fresh[it.ID] = indexEntry{
				ID:          it.ID,
				ContentHash: contentHash(it.Text),
				ModelID:     modelID,
				Vector:      vecs[i],
			}
		}
	}

	ix.mu.Lock()
	ix.entries = fresh
	ix.mu.Unlock()

	return ix.save()
}

func (ix *Index) save() error {
	ix.mu.Lock()
	f := indexFile{Version: 1, Entries: make([]indexEntry, 0, len(ix.entries))}
	for _, e := range ix.entries {
		f.Entries = append(f.Entries, e)
	}
	ix.mu.Unlock()

	sort.Slice(f.Entries, func(i, j int) bool { return f.Entries[i].ID < f.Entries[j].ID })

	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("intent: marshaling index: %w", err)
	}

	dir := filepath.Dir(ix.path())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("intent: creating intent-index dir: %w", err)
	}

	tmp, err := os.CreateTemp(dir, "vectors-*.json.tmp")
	if err != nil {
		return fmt.Errorf("intent: creating temp index file: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("intent: writing temp index file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("intent: closing temp index file: %w", err)
	}
	if err := os.Rename(tmpPath, ix.path()); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("intent: replacing index file: %w", err)
	}
	return nil
}

// EmbedQuery embeds text, caching the result for the lifetime of this Index
// (in practice, one run) so repeated selection against the same task
// description doesn't re-embed it per step.
func (ix *Index) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	ix.mu.Lock()
	if v, ok := ix.queryCache[text]; ok {
		ix.mu.Unlock()
		return v, nil
	}
	ix.mu.Unlock()

	vecs, err := ix.embedder.Embed(ctx, []string{text})
	if err != nil {
		return nil, fmt.Errorf("intent: embedding query: %w", err)
	}
	if len(vecs) != 1 {
		return nil, fmt.Errorf("intent: embedder returned %d vector(s) for 1 query", len(vecs))
	}

	ix.mu.Lock()
	ix.queryCache[text] = vecs[0]
	ix.mu.Unlock()
	return vecs[0], nil
}

// TopK returns up to k entries ranked by cosine similarity to vector,
// highest first. k <= 0 returns every entry, sorted. Callers that need a
// per-model score floor (select.go, per the design) apply it on top of this.
func (ix *Index) TopK(vector []float32, k int) []Match {
	ix.mu.Lock()
	matches := make([]Match, 0, len(ix.entries))
	for id, e := range ix.entries {
		matches = append(matches, Match{ID: id, Score: cosine(vector, e.Vector)})
	}
	ix.mu.Unlock()

	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Score != matches[j].Score {
			return matches[i].Score > matches[j].Score
		}
		return matches[i].ID < matches[j].ID
	})
	if k > 0 && k < len(matches) {
		matches = matches[:k]
	}
	return matches
}

// cosine returns the cosine similarity of a and b. Vectors are expected to
// be unit-normalized (the Embedder contract), so this is a plain dot
// product; mismatched lengths (e.g. a stale entry from a differently-sized
// model that Sync failed to catch) score 0 rather than panicking.
func cosine(a, b []float32) float32 {
	if len(a) != len(b) {
		return 0
	}
	var sum float32
	for i := range a {
		sum += a[i] * b[i]
	}
	return sum
}
