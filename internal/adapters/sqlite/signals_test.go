package sqlite_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/swordsmanluke/cloche/internal/adapters/sqlite"
	"github.com/swordsmanluke/cloche/internal/signal"
)

func TestPruneSignalsOlderThan(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()
	ctx := context.Background()
	sig := signal.Signal{Source: "s", Key: "k", Severity: "low", Title: "t"}
	_, err = store.SaveSignal(ctx, "/p", sig)
	require.NoError(t, err)

	n, err := store.PruneSignalsOlderThan(ctx, time.Now().Add(-time.Hour))
	require.NoError(t, err)
	require.EqualValues(t, 0, n)

	n, err = store.PruneSignalsOlderThan(ctx, time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	recs, err := store.ListSignals(ctx, "/p")
	require.NoError(t, err)
	require.Empty(t, recs)
}
