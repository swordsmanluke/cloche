package ports

import "context"

// KVReader is the port for looking up a single named value from a run's KV store.
// Implementations are injected into the prompt resolver so templates can reference
// values written by earlier steps without burning LLM tokens to re-discover them.
type KVReader interface {
	Get(ctx context.Context, key string) (value string, found bool, err error)
}

// KVWriter is the port for writing a single named value to a run's KV store.
// Used by the prompt adapter to persist Claude's session_id before a step
// might be parked, so a later resume can `claude --resume <session-id>`.
type KVWriter interface {
	Set(ctx context.Context, key, value string) error
}
