// Package embed defines the Embedder port used by the intent package's
// semantic retrieval and the pure-Go adapters that satisfy it (ollama,
// keyword). A third adapter, onnx, is added behind a build tag in a later
// slice; the chain below already accounts for it by name so no change is
// needed here when it lands. See
// docs/plans/2026-09-13-intent-continuity-design.md for the full design.
package embed

import (
	"context"
	"fmt"
)

// Embedder turns text into vectors for semantic similarity search.
type Embedder interface {
	// Embed returns one unit-normalized vector per input text, in order.
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	// ModelID identifies the model (and revision/config) producing vectors.
	// Index entries are invalidated when a requirement's stored ModelID
	// no longer matches.
	ModelID() string
	// Dimensions reports the vector length. Adapters that only learn this
	// after their first Embed call (e.g. a remote model) may return 0 until
	// then.
	Dimensions() int
}

// candidate is one adapter's entry in the chain: a cheap availability check
// and a constructor, run only once probe reports true.
type candidate struct {
	probe func(ctx context.Context) bool
	build func(ctx context.Context) (Embedder, error)
}

// registry holds every adapter that has registered itself via Register.
// Adapters register unconditionally from init() (ollama, keyword) or only
// under a build tag (onnx), so the set of known names varies by build.
var registry = map[string]candidate{}

// defaultOrder is the chain's default preference. onnx is listed even though
// nothing registers it yet (that lands in a later slice) — resolve simply
// skips names with no registered candidate.
var defaultOrder = []string{"onnx", "ollama", "keyword"}

// keywordName is the always-available fallback appended to a pinned chain,
// so an explicit intent.embedder pin can still never block a run: if the
// pinned adapter is unavailable, resolution degrades to keyword rather than
// erroring, matching the design's "never blocks a run" guarantee.
const keywordName = "keyword"

// Register adds an adapter to the chain under name. Called from adapter
// init() functions; a name registered twice overwrites the earlier entry.
func Register(name string, probe func(ctx context.Context) bool, build func(ctx context.Context) (Embedder, error)) {
	registry[name] = candidate{probe: probe, build: build}
}

// Resolve picks an Embedder using the process-wide adapter registry. With an
// empty pin it walks the default chain (onnx, ollama, keyword) and returns
// the first available adapter. With a non-empty pin (the intent.embedder
// config key) it prefers that named adapter, falling back only to keyword —
// never to a different non-pinned adapter — if the pin is unavailable. An
// unknown pin name is a configuration error.
func Resolve(ctx context.Context, pin string) (Embedder, error) {
	return resolve(ctx, defaultOrder, registry, pin)
}

// Available reports whether the named adapter is currently registered and
// its probe succeeds. Unlike Resolve with a pin, this never falls back to
// keyword — it answers "is this specific adapter live right now", which is
// what callers that need to gate on a real (non-degraded) embedder, such as
// the spike-corpus regression test, actually need.
func Available(ctx context.Context, name string) bool {
	c, ok := registry[name]
	return ok && c.probe(ctx)
}

func resolve(ctx context.Context, order []string, reg map[string]candidate, pin string) (Embedder, error) {
	chain := order
	if pin != "" {
		if _, ok := reg[pin]; !ok {
			return nil, fmt.Errorf("intent: unknown embedder %q", pin)
		}
		chain = []string{pin, keywordName}
	}

	for _, name := range chain {
		c, ok := reg[name]
		if !ok {
			continue
		}
		if c.probe(ctx) {
			return c.build(ctx)
		}
	}
	return nil, fmt.Errorf("intent: no embedder available")
}
