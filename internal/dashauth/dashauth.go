// Package dashauth holds the daemon-wide token that guards the dashboard's
// HTTP listener. One token, stored 0600 in the daemon state dir; the daemon
// re-reads it on every check so `cloche dashboard token --rotate` takes effect
// at once.
package dashauth

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

// Store keeps the dashboard token in a single 0600 file.
type Store struct {
	path string
	mu   sync.Mutex
}

// NewStore returns a store backed by the file at path.
func NewStore(path string) *Store { return &Store{path: path} }

func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "clo_dash_" + hex.EncodeToString(b), nil
}

func (s *Store) read() (string, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func (s *Store) write(tok string) error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".dashboard-token-*")
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
	return os.Rename(tmp.Name(), s.path)
}

// Get returns the token, generating and storing one on first use.
func (s *Store) Get() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if tok, err := s.read(); err == nil && tok != "" {
		return tok, nil
	} else if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	tok, err := newToken()
	if err != nil {
		return "", err
	}
	if err := s.write(tok); err != nil {
		return "", fmt.Errorf("storing dashboard token: %w", err)
	}
	return tok, nil
}

// Rotate replaces the token; the old one stops working at once.
func (s *Store) Rotate() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tok, err := newToken()
	if err != nil {
		return "", err
	}
	if err := s.write(tok); err != nil {
		return "", fmt.Errorf("storing dashboard token: %w", err)
	}
	return tok, nil
}

// Peek returns the stored token without creating one ("" if none).
func (s *Store) Peek() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	tok, _ := s.read()
	return tok
}

// Valid reports whether presented is the token. It never creates one, so
// before first use everything is rejected. The daemon calls Get at startup.
// Comparison is constant-time over fixed-length digests.
func (s *Store) Valid(presented string) bool {
	if presented == "" {
		return false
	}
	tok := s.Peek()
	if tok == "" {
		return false
	}
	a := sha256.Sum256([]byte(tok))
	b := sha256.Sum256([]byte(presented))
	return subtle.ConstantTimeCompare(a[:], b[:]) == 1
}
