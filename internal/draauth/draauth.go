// Package draauth mints and validates the per-wake-up credential a DRA uses to
// reach the daemon's DRA API (/api/dra/...). The credential names the run, the
// project and the DRA, and is signed with the daemon-lifetime secret, so the
// daemon keeps no token store. It is only honoured while the run is active:
// the web handler checks the run's state on every request.
//
// The signed message carries a domain-separation prefix so a DRA credential
// can never double as the run's /mcp token (internal/mcpauth), which signs the
// bare run ID under the same secret.
package draauth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
)

// Claims is what a credential asserts.
type Claims struct {
	RunID   string `json:"run"`
	Project string `json:"project"` // project root directory
	DRA     string `json:"dra"`     // the DRA's name, which is also its domain label
}

func (c Claims) valid() bool { return c.RunID != "" && c.Project != "" && c.DRA != "" }

func sign(secret []byte, payload string) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("cloche-dra-v1\x00"))
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

// Token returns the credential for c, or "" if the secret or claims are empty.
func Token(secret []byte, c Claims) string {
	if len(secret) == 0 || !c.valid() {
		return ""
	}
	raw, _ := json.Marshal(c)
	payload := base64.RawURLEncoding.EncodeToString(raw)
	return payload + "." + sign(secret, payload)
}

// Parse validates token under secret and returns its claims. It says nothing
// about whether the run is still alive; callers must check that.
func Parse(secret []byte, token string) (Claims, bool) {
	if token == "" || len(secret) == 0 {
		return Claims{}, false
	}
	payload, sig, ok := strings.Cut(token, ".")
	if !ok || !hmac.Equal([]byte(sign(secret, payload)), []byte(sig)) {
		return Claims{}, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return Claims{}, false
	}
	var c Claims
	if json.Unmarshal(raw, &c) != nil || !c.valid() {
		return Claims{}, false
	}
	return c, true
}
