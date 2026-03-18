package domain

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateRunID_FormatWithStep(t *testing.T) {
	id := GenerateRunID("develop", "implement")
	parts := strings.Split(id, ":")
	require.Len(t, parts, 3, "expected attempt:workflow:step format, got %s", id)
	assert.Len(t, parts[0], 4, "attempt segment should be 4 chars, got %s", parts[0])
	assert.Equal(t, "develop", parts[1])
	assert.Equal(t, "implement", parts[2])
}

func TestGenerateRunID_FormatWithoutStep(t *testing.T) {
	id := GenerateRunID("develop", "")
	parts := strings.Split(id, ":")
	require.Len(t, parts, 2, "expected attempt:workflow format, got %s", id)
	assert.Len(t, parts[0], 4, "attempt segment should be 4 chars, got %s", parts[0])
	assert.Equal(t, "develop", parts[1])
}

func TestGenerateRunID_AttemptAlphanumeric(t *testing.T) {
	for i := 0; i < 20; i++ {
		id := GenerateRunID("test", "")
		attempt := strings.SplitN(id, ":", 2)[0]
		for _, c := range attempt {
			assert.True(t, (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9'),
				"attempt char %q not alphanumeric in id %s", c, id)
		}
	}
}

func TestGenerateRunID_Unique(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 50; i++ {
		seen[GenerateRunID("test", "")] = true
	}
	assert.Greater(t, len(seen), 1, "expected multiple distinct IDs across 50 calls")
}

func TestParseRunID_WithStep(t *testing.T) {
	attempt, workflow, step := ParseRunID("a12z:develop:implement")
	assert.Equal(t, "a12z", attempt)
	assert.Equal(t, "develop", workflow)
	assert.Equal(t, "implement", step)
}

func TestParseRunID_WithoutStep(t *testing.T) {
	attempt, workflow, step := ParseRunID("a12z:develop")
	assert.Equal(t, "a12z", attempt)
	assert.Equal(t, "develop", workflow)
	assert.Equal(t, "", step)
}

func TestParseRunID_RoundTrip(t *testing.T) {
	original := GenerateRunID("main", "build")
	attempt, workflow, step := ParseRunID(original)
	assert.Len(t, attempt, 4)
	assert.Equal(t, "main", workflow)
	assert.Equal(t, "build", step)
}
