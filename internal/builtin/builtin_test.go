package builtin_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swordsmanluke/cloche/internal/builtin"
)

func TestLookup_Found(t *testing.T) {
	wf, ok := builtin.Lookup("intent-scan")
	require.True(t, ok)
	require.NotNil(t, wf)
	assert.Equal(t, "intent-scan", wf.Name)
	assert.True(t, wf.Builtin)
}

func TestLookup_NotFound(t *testing.T) {
	wf, ok := builtin.Lookup("does-not-exist")
	assert.False(t, ok)
	assert.Nil(t, wf)
}

func TestLookup_FreshInstances(t *testing.T) {
	a, _ := builtin.Lookup("intent-scan")
	b, _ := builtin.Lookup("intent-scan")

	a.Config["host.agent_command"] = "mutated"
	assert.NotEqual(t, "mutated", b.Config["host.agent_command"])
}

func TestAll(t *testing.T) {
	all := builtin.All()
	require.Contains(t, all, "intent-scan")
	assert.Equal(t, "intent-scan", all["intent-scan"].Name)
}
