package domain

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateRunID_Prefix(t *testing.T) {
	id := GenerateRunID("develop")
	assert.True(t, strings.HasPrefix(id, "develop-"), "expected ID to start with workflow name prefix, got %s", id)
}

func TestGenerateRunID_ThreeParts(t *testing.T) {
	id := GenerateRunID("build")
	parts := strings.Split(id, "-")
	require.Len(t, parts, 3, "expected exactly 3 hyphen-separated parts, got %d in %s", len(parts), id)
}

func TestGenerateRunID_Unique(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 50; i++ {
		seen[GenerateRunID("test")] = true
	}
	assert.Greater(t, len(seen), 1, "expected multiple distinct IDs across 50 calls")
}
