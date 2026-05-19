package ports

import "context"

// KVReader is the port for looking up a single named value from a run's KV store.
// Implementations are injected into the prompt resolver so templates can reference
// values written by earlier steps without burning LLM tokens to re-discover them.
type KVReader interface {
	Get(ctx context.Context, key string) (value string, found bool, err error)
}
