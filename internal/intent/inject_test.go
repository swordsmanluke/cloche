package intent_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swordsmanluke/cloche/internal/intent"
)

func TestResolve_NoIntentDir_Dormant(t *testing.T) {
	dir := t.TempDir()

	injection, active, err := intent.Resolve(context.Background(), dir, intent.Query{}, intent.Options{}, "")
	require.NoError(t, err)
	assert.False(t, active)
	assert.Equal(t, intent.Injection{}, injection)
}

func TestResolve_ActiveProjectRequirement_Injected(t *testing.T) {
	dir := t.TempDir()
	store := intent.NewStore(dir)

	created, err := store.CreateRequirement(&intent.Requirement{
		Status:     intent.StatusActive,
		Scope:      intent.Scope{Level: intent.ScopeLevelProject},
		Confidence: intent.ConfidenceHigh,
		Body:       "Never bump the major version unless explicitly told to.",
	})
	require.NoError(t, err)

	injection, active, err := intent.Resolve(context.Background(), dir, intent.Query{TaskDescription: "cut a release"}, intent.Options{}, "")
	require.NoError(t, err)
	assert.True(t, active)
	require.Contains(t, injection.Block, created.ID)
	assert.Contains(t, injection.Block, "Standing project requirements")
	assert.Equal(t, []string{created.ID}, injection.IDs)
}

func TestResolve_IntentDirWithNoActiveRequirements_ActiveButEmpty(t *testing.T) {
	dir := t.TempDir()
	store := intent.NewStore(dir)

	_, err := store.CreateRequirement(&intent.Requirement{
		Status:     intent.StatusDisabled,
		Scope:      intent.Scope{Level: intent.ScopeLevelProject},
		Confidence: intent.ConfidenceHigh,
		Body:       "A disabled requirement.",
	})
	require.NoError(t, err)

	injection, active, err := intent.Resolve(context.Background(), dir, intent.Query{}, intent.Options{}, "")
	require.NoError(t, err)
	assert.True(t, active)
	assert.Equal(t, "", injection.Block)
	assert.Empty(t, injection.IDs)
}

func TestResolve_UnknownEmbedderPin_DegradesRatherThanFails(t *testing.T) {
	dir := t.TempDir()
	store := intent.NewStore(dir)

	created, err := store.CreateRequirement(&intent.Requirement{
		Status:     intent.StatusActive,
		Scope:      intent.Scope{Level: intent.ScopeLevelProject},
		Confidence: intent.ConfidenceHigh,
		Body:       "Some standing requirement.",
	})
	require.NoError(t, err)

	injection, active, err := intent.Resolve(context.Background(), dir, intent.Query{}, intent.Options{}, "not-a-real-adapter")
	require.NoError(t, err)
	assert.True(t, active)
	assert.Equal(t, []string{created.ID}, injection.IDs)
}
