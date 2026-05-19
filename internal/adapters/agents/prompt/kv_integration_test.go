package prompt_test

import (
	"context"
	"testing"

	"github.com/cloche-dev/cloche/internal/adapters/agents/prompt"
	"github.com/cloche-dev/cloche/internal/adapters/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sqliteKVReader adapts a sqlite.Store as a prompt.KVReader for integration tests.
type sqliteKVReader struct {
	store     *sqlite.Store
	taskID    string
	attemptID string
	runID     string
}

func (r *sqliteKVReader) Get(ctx context.Context, key string) (string, bool, error) {
	return r.store.GetContextKey(ctx, r.taskID, r.attemptID, r.runID, key)
}

func TestResolver_WithSqliteKVReader_ResolvesPreviousStepValue(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })

	ctx := context.Background()
	require.NoError(t, store.SetContextKey(ctx, "task1", "att1", "run1", "output_file", "/workspace/result.json"))

	r := &prompt.Resolver{
		KV: &sqliteKVReader{
			store:     store,
			taskID:    "task1",
			attemptID: "att1",
			runID:     "run1",
		},
		WorkDir: t.TempDir(),
	}

	got, err := r.Resolve(ctx, "Read {{ $output_file }} for the results")
	require.NoError(t, err)
	assert.Equal(t, "Read /workspace/result.json for the results", got)
}

func TestResolver_WithSqliteKVReader_BuiltinShadowsStore(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })

	ctx := context.Background()
	require.NoError(t, store.SetContextKey(ctx, "task1", "att1", "run1", "task_id", "kv-override"))

	r := &prompt.Resolver{
		Builtins: map[string]string{"task_id": "builtin-wins"},
		KV: &sqliteKVReader{
			store:     store,
			taskID:    "task1",
			attemptID: "att1",
			runID:     "run1",
		},
		WorkDir: t.TempDir(),
	}

	got, err := r.Resolve(ctx, "{{ $task_id }}")
	require.NoError(t, err)
	assert.Equal(t, "builtin-wins", got)
}

func TestResolver_WithSqliteKVReader_MissingKeyErrors(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })

	r := &prompt.Resolver{
		KV: &sqliteKVReader{
			store:     store,
			taskID:    "task1",
			attemptID: "att1",
			runID:     "run1",
		},
		WorkDir: t.TempDir(),
	}

	_, err = r.Resolve(context.Background(), "{{ $missing_key }}")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing_key")
}
