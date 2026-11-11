package draauth_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/swordsmanluke/cloche/internal/draauth"
	"github.com/swordsmanluke/cloche/internal/mcpauth"
)

func TestRoundTrip(t *testing.T) {
	secret := []byte("s3cr3t")
	c := draauth.Claims{RunID: "r1", Project: "/p", DRA: "auth"}
	tok := draauth.Token(secret, c)
	got, ok := draauth.Parse(secret, tok)
	assert.True(t, ok)
	assert.Equal(t, c, got)
}

func TestRejects(t *testing.T) {
	secret := []byte("s3cr3t")
	c := draauth.Claims{RunID: "r1", Project: "/p", DRA: "auth"}
	tok := draauth.Token(secret, c)

	_, ok := draauth.Parse([]byte("other"), tok)
	assert.False(t, ok, "wrong secret")
	_, ok = draauth.Parse(secret, "")
	assert.False(t, ok)
	_, ok = draauth.Parse(secret, "garbage")
	assert.False(t, ok)
	_, ok = draauth.Parse(secret, tok+"0")
	assert.False(t, ok, "tampered signature")
	// Payload of another credential with this credential's signature.
	other := draauth.Token(secret, draauth.Claims{RunID: "r1", Project: "/q", DRA: "auth"})
	forged := other[:len(other)-65] + tok[len(tok)-65:]
	_, ok = draauth.Parse(secret, forged)
	assert.False(t, ok, "signature from another credential")
	// The run's MCP token is not a DRA credential.
	_, ok = draauth.Parse(secret, mcpauth.Token(secret, "r1"))
	assert.False(t, ok)
	assert.Equal(t, "", draauth.Token(nil, c))
	assert.Equal(t, "", draauth.Token(secret, draauth.Claims{RunID: "r1"}))
}
