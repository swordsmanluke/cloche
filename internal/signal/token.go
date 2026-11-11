package signal

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// TokenStore keeps one bearer token per project as a 0600 file in a private
// directory (under the daemon state dir). The CLI and the daemon share the
// directory: the daemon re-reads on every check, so a rotation takes effect
// immediately without a restart.
type TokenStore struct {
	dir string
	mu  sync.Mutex
}

// NewTokenStore returns a store rooted at dir.
func NewTokenStore(dir string) *TokenStore { return &TokenStore{dir: dir} }

func (s *TokenStore) path(projectDir string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(projectDir)))
	return filepath.Join(s.dir, hex.EncodeToString(sum[:])[:32]+".token")
}

func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "clo_sig_" + hex.EncodeToString(b), nil
}

func (s *TokenStore) read(projectDir string) (string, error) {
	data, err := os.ReadFile(s.path(projectDir))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func (s *TokenStore) write(projectDir, tok string) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	p := s.path(projectDir)
	tmp, err := os.CreateTemp(s.dir, ".token-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(tok + "\n"); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), p)
}

// Get returns the project's token, generating and storing one on first use.
func (s *TokenStore) Get(projectDir string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if tok, err := s.read(projectDir); err == nil && tok != "" {
		return tok, nil
	} else if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	tok, err := newToken()
	if err != nil {
		return "", err
	}
	if err := s.write(projectDir, tok); err != nil {
		return "", fmt.Errorf("storing token: %w", err)
	}
	return tok, nil
}

// Rotate replaces the project's token with a fresh one and returns it. The old
// token stops working at once.
func (s *TokenStore) Rotate(projectDir string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tok, err := newToken()
	if err != nil {
		return "", err
	}
	if err := s.write(projectDir, tok); err != nil {
		return "", fmt.Errorf("storing token: %w", err)
	}
	return tok, nil
}

// Valid reports whether presented is the project's token. It never creates a
// token: a project that has none rejects everything. Comparison is
// constant-time (over fixed-length digests, so length doesn't leak either).
func (s *TokenStore) Valid(projectDir, presented string) bool {
	if presented == "" {
		return false
	}
	s.mu.Lock()
	tok, err := s.read(projectDir)
	s.mu.Unlock()
	if err != nil || tok == "" {
		return false
	}
	a := sha256.Sum256([]byte(tok))
	b := sha256.Sum256([]byte(presented))
	return subtle.ConstantTimeCompare(a[:], b[:]) == 1
}
