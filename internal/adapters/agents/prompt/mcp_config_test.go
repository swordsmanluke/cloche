package prompt

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMCPConfigArgs_NonClaudeCommand(t *testing.T) {
	t.Setenv("CLOCHE_MCP_URL", "http://host.docker.internal:8080/mcp?run_id=run-1")
	t.Setenv("CLOCHE_MCP_TOKEN", "tok")
	assert.Nil(t, mcpConfigArgs("opencode"))
}

func TestMCPConfigArgs_NoEnv(t *testing.T) {
	t.Setenv("CLOCHE_MCP_URL", "")
	t.Setenv("CLOCHE_MCP_TOKEN", "")
	assert.Nil(t, mcpConfigArgs("claude"))
}

func TestMCPConfigArgs_MissingToken(t *testing.T) {
	t.Setenv("CLOCHE_MCP_URL", "http://host.docker.internal:8080/mcp?run_id=run-1")
	t.Setenv("CLOCHE_MCP_TOKEN", "")
	assert.Nil(t, mcpConfigArgs("claude"))
}

func TestMCPConfigArgs_WritesConfigFile(t *testing.T) {
	t.Setenv("CLOCHE_MCP_URL", "http://host.docker.internal:8080/mcp?run_id=run-1")
	t.Setenv("CLOCHE_MCP_TOKEN", "sekret")

	args := mcpConfigArgs("claude")
	require.Len(t, args, 2)
	assert.Equal(t, "--mcp-config", args[0])
	path := args[1]
	t.Cleanup(func() { os.Remove(path) })

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var cfg struct {
		MCPServers map[string]struct {
			Type    string            `json:"type"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	require.NoError(t, json.Unmarshal(data, &cfg))
	server, ok := cfg.MCPServers["cloche-help"]
	require.True(t, ok)
	assert.Equal(t, "http", server.Type)
	assert.Equal(t, "http://host.docker.internal:8080/mcp?run_id=run-1", server.URL)
	assert.Equal(t, "Bearer sekret", server.Headers["Authorization"])
}
