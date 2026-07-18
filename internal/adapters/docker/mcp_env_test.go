package docker

import (
	"testing"

	"github.com/cloche-dev/cloche/internal/mcpauth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMCPEnvArgs_Disabled(t *testing.T) {
	assert.Nil(t, mcpEnvArgs(nil, "127.0.0.1:8080", "run-1"))
	assert.Nil(t, mcpEnvArgs([]byte("secret"), "", "run-1"))
	assert.Nil(t, mcpEnvArgs([]byte("secret"), "127.0.0.1:8080", ""))
}

func TestMCPEnvArgs_Enabled(t *testing.T) {
	secret := []byte("secret")
	args := mcpEnvArgs(secret, "127.0.0.1:8080", "run-1")
	require.Len(t, args, 4)
	assert.Equal(t, "-e", args[0])
	assert.Equal(t, "CLOCHE_MCP_URL=http://host.docker.internal:8080/mcp?run_id=run-1", args[1])
	assert.Equal(t, "-e", args[2])
	assert.Equal(t, "CLOCHE_MCP_TOKEN="+mcpauth.Token(secret, "run-1"), args[3])
}

func TestMCPEnvArgs_ZeroZeroAddrRewritten(t *testing.T) {
	args := mcpEnvArgs([]byte("secret"), "0.0.0.0:9090", "run-2")
	require.Len(t, args, 4)
	assert.Contains(t, args[1], "host.docker.internal:9090")
}
