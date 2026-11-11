package dashauth

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStore(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "dashboard-token")
	s := NewStore(p)
	assert.False(t, s.Valid("anything"), "no token yet: nothing valid")
	assert.Empty(t, s.Peek())

	tok, err := s.Get()
	require.NoError(t, err)
	again, _ := s.Get()
	assert.Equal(t, tok, again)
	fi, err := os.Stat(p)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
	assert.True(t, s.Valid(tok))
	assert.False(t, s.Valid(""))
	assert.False(t, s.Valid(tok+"x"))

	n, err := s.Rotate()
	require.NoError(t, err)
	assert.NotEqual(t, tok, n)
	assert.False(t, s.Valid(tok))
	assert.True(t, s.Valid(n))
}
