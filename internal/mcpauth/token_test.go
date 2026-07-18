package mcpauth_test

import (
	"testing"

	"github.com/cloche-dev/cloche/internal/mcpauth"
	"github.com/stretchr/testify/assert"
)

func TestToken_Deterministic(t *testing.T) {
	secret := []byte("s3cr3t")
	assert.Equal(t, mcpauth.Token(secret, "run-1"), mcpauth.Token(secret, "run-1"))
	assert.NotEqual(t, mcpauth.Token(secret, "run-1"), mcpauth.Token(secret, "run-2"))
}

func TestValid(t *testing.T) {
	secret := []byte("s3cr3t")
	token := mcpauth.Token(secret, "run-1")

	assert.True(t, mcpauth.Valid(secret, "run-1", token))
	assert.False(t, mcpauth.Valid(secret, "run-2", token))
	assert.False(t, mcpauth.Valid(secret, "run-1", "wrong"))
	assert.False(t, mcpauth.Valid(secret, "run-1", ""))
	assert.False(t, mcpauth.Valid(nil, "run-1", token))
}
