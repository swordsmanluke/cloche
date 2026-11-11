package docker

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swordsmanluke/cloche/internal/draauth"
	"github.com/swordsmanluke/cloche/internal/mcpauth"
	"github.com/swordsmanluke/cloche/internal/ports"
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

func TestDRAEnvArgs(t *testing.T) {
	secret := []byte("secret")
	cfg := ports.ContainerConfig{RunID: "wake-1", ProjectDir: "/p", DRA: "auth"}

	assert.Nil(t, draEnvArgs(secret, "127.0.0.1:8080", ports.ContainerConfig{RunID: "r", ProjectDir: "/p"}), "not a DRA")
	assert.Nil(t, draEnvArgs(nil, "127.0.0.1:8080", cfg))
	assert.Nil(t, draEnvArgs(secret, "", cfg))

	args := draEnvArgs(secret, "0.0.0.0:8080", cfg)
	assert.Contains(t, args, "CLOCHE_DRA_URL=http://host.docker.internal:8080")
	assert.Contains(t, args, "CLOCHE_DRA_NAME=auth")
	var tok string
	for _, a := range args {
		if strings.HasPrefix(a, "CLOCHE_DRA_TOKEN=") {
			tok = strings.TrimPrefix(a, "CLOCHE_DRA_TOKEN=")
		}
	}
	c, ok := draauth.Parse(secret, tok)
	assert.True(t, ok)
	assert.Equal(t, draauth.Claims{RunID: "wake-1", Project: "/p", DRA: "auth"}, c)

	// The live project root wins over a snapshot seed dir.
	cfg.HostProjectDir = "/live"
	for _, a := range draEnvArgs(secret, "0.0.0.0:8080", cfg) {
		if strings.HasPrefix(a, "CLOCHE_DRA_TOKEN=") {
			c, _ := draauth.Parse(secret, strings.TrimPrefix(a, "CLOCHE_DRA_TOKEN="))
			assert.Equal(t, "/live", c.Project)
		}
	}
}
