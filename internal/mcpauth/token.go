// Package mcpauth mints and validates the per-run bearer token used to
// authenticate the ask_user MCP tool at /mcp. The token is a deterministic
// HMAC of the run ID under a daemon-generated secret, so the daemon never
// needs to keep a token store: it recomputes and compares on each request.
package mcpauth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// Token returns the bearer token a client must present to act as runID:
// hex(HMAC-SHA256(secret, runID)).
func Token(secret []byte, runID string) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(runID))
	return hex.EncodeToString(mac.Sum(nil))
}

// Valid reports whether token is the correct bearer token for runID under secret.
func Valid(secret []byte, runID, token string) bool {
	if token == "" || len(secret) == 0 {
		return false
	}
	return hmac.Equal([]byte(Token(secret, runID)), []byte(token))
}
