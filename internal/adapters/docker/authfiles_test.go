package docker

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFilterAgentSettings_KeepsOnlyAuthKeys(t *testing.T) {
	in := []byte(`{
  "model": "claude-fable-5-1[1m]",
  "effortLevel": "high",
  "alwaysThinkingEnabled": true,
  "hooks": {"SessionStart": [{"matcher": "*", "hooks": [{"type": "command", "command": "bash /home/host/.claude/hooks/x.sh"}]}]},
  "permissions": {"allow": ["Bash(rm:*)"]},
  "enabledPlugins": {"foo@bar": true},
  "env": {"ANTHROPIC_BASE_URL": "https://proxy.example"},
  "apiKeyHelper": "/usr/local/bin/key-helper"
}`)
	out, kept := filterAgentSettings(in)
	assert.Equal(t, []string{"apiKeyHelper", "env"}, kept)

	var got map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(out, &got))
	assert.Len(t, got, 2)
	assert.JSONEq(t, `{"ANTHROPIC_BASE_URL": "https://proxy.example"}`, string(got["env"]))
	assert.JSONEq(t, `"/usr/local/bin/key-helper"`, string(got["apiKeyHelper"]))
	for _, k := range []string{"model", "effortLevel", "alwaysThinkingEnabled", "hooks", "permissions", "enabledPlugins"} {
		_, present := got[k]
		assert.False(t, present, "%s must not reach an autonomous container", k)
	}
}

func TestFilterAgentSettings_NothingToKeep(t *testing.T) {
	out, kept := filterAgentSettings([]byte(`{"model": "sonnet", "hooks": {}}`))
	assert.Nil(t, kept)
	assert.JSONEq(t, `{}`, string(out))
}

func TestFilterAgentSettings_UnparseableBecomesEmpty(t *testing.T) {
	out, kept := filterAgentSettings([]byte(`{"model": "sonnet",`))
	assert.Nil(t, kept)
	assert.JSONEq(t, `{}`, string(out))

	out, kept = filterAgentSettings([]byte(`[1, 2]`))
	assert.Nil(t, kept)
	assert.JSONEq(t, `{}`, string(out))
}
