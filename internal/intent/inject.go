package intent

import (
	"context"
	"log"
	"os"

	"github.com/swordsmanluke/cloche/internal/intent/embed"
)

// Injection is the outcome of resolving one step's requirements block: the
// formatted text to inject (empty when nothing was selected) and the IDs of
// the requirements it contains, for recording in run KV.
type Injection struct {
	Block string
	IDs   []string
}

// Resolve computes the injected requirements block for one agent step: it
// loads the project's intent store, resolves an embedder (per embedderPin,
// config.toml's intent.embedder key; empty uses the default adapter chain),
// syncs the embedding index, and applies Select + FormatBlock.
//
// active reports whether the project has a .cloche/intent/ directory at
// all. Callers use it to decide whether to touch run KV: a project with no
// intent dir gets active=false and a zero Injection without an embedder or
// index being touched — the feature's dormancy guarantee. An intent dir
// that exists but currently selects nothing for this step is not dormant
// (active=true, zero Injection) so an explicit {{ $intent }} reference
// still resolves, just to an empty block.
//
// Embedder unavailability (unsupported platform, ollama down, etc.) is
// logged and degrades selection to deterministic scope matching only; it
// never fails the run.
func Resolve(ctx context.Context, projectDir string, query Query, opts Options, embedderPin string) (injection Injection, active bool, err error) {
	store := NewStore(projectDir)
	if _, statErr := os.Stat(store.IntentDir()); statErr != nil {
		return Injection{}, false, nil
	}

	reqs, err := store.ListRequirements()
	if err != nil {
		return Injection{}, true, err
	}
	dm, err := store.LoadDomains()
	if err != nil {
		return Injection{}, true, err
	}

	var idx *Index
	embedder, embErr := embed.Resolve(ctx, embedderPin)
	if embErr != nil {
		log.Printf("intent: no embedder available, selection degrades to deterministic scoping: %v", embErr)
		embedder = nil
	} else if idx, err = NewIndex(projectDir, embedder); err != nil {
		return Injection{}, true, err
	} else {
		items := make([]Item, 0, len(reqs))
		for _, r := range reqs {
			if r.Status != StatusActive {
				continue
			}
			items = append(items, Item{ID: r.ID, Text: EmbedText(r)})
		}
		if syncErr := idx.Sync(ctx, items); syncErr != nil {
			log.Printf("intent: embedding index sync failed, selection degrades to deterministic scoping: %v", syncErr)
			embedder = nil
			idx = nil
		}
	}

	result, err := Select(ctx, reqs, dm.Domains, idx, embedder, query, opts)
	if err != nil {
		return Injection{}, true, err
	}

	ids := make([]string, 0, len(result.Selected))
	for _, s := range result.Selected {
		ids = append(ids, s.Requirement.ID)
	}
	return Injection{Block: FormatBlock(result), IDs: ids}, true, nil
}
