package scan

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPassCursor(t *testing.T) {
	cursor := filepath.Join(t.TempDir(), "cursor")

	next, total := NextPass(cursor, 3)
	assert.Equal(t, 0, next, "a missing cursor means the first pass")
	assert.Equal(t, 3, total)

	require.NoError(t, AdvancePass(cursor, 1))
	next, _ = NextPass(cursor, 3)
	assert.Equal(t, 1, next)

	require.NoError(t, AdvancePass(cursor, 3))
	next, total = NextPass(cursor, 3)
	assert.GreaterOrEqual(t, next, total, "past the last pass means done")
}

func TestAggregatePassSummaries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scan-summary.txt")
	assert.Equal(t, "", AggregatePassSummaries(path), "no file, no summary")

	require.NoError(t, AppendPassSummary(path, "alpha", "created 2, superseded 1, merged 0, dropped 3"))
	require.NoError(t, AppendPassSummary(path, "", "created 1, superseded 0, merged 4, dropped 0"))
	assert.Equal(t, "created 3, superseded 1, merged 4, dropped 3", AggregatePassSummaries(path))
}
