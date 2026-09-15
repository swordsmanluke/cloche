package attention

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMuteUnmute(t *testing.T) {
	tmpDir := t.TempDir()
	key := "builtin-failures:intent-scan"

	assert.False(t, IsMuted(tmpDir, key))

	require.NoError(t, Mute(tmpDir, key))
	assert.True(t, IsMuted(tmpDir, key))
	// A different key stays unaffected.
	assert.False(t, IsMuted(tmpDir, "builtin-failures:other-workflow"))

	require.NoError(t, Unmute(tmpDir, key))
	assert.False(t, IsMuted(tmpDir, key))
}

func TestMute_PersistsAcrossLoads(t *testing.T) {
	tmpDir := t.TempDir()
	key := "builtin-failures:intent-scan"

	require.NoError(t, Mute(tmpDir, key))

	data, err := os.ReadFile(filepath.Join(tmpDir, ".cloche", ".attention-mutes.json"))
	require.NoError(t, err)
	assert.Contains(t, string(data), key)

	// A fresh read (simulating a daemon restart) still sees the mute.
	assert.True(t, IsMuted(tmpDir, key))
}

func TestIsMuted_MissingFile(t *testing.T) {
	tmpDir := t.TempDir()
	assert.False(t, IsMuted(tmpDir, "anything"))
}

func TestUnmute_NotMuted(t *testing.T) {
	tmpDir := t.TempDir()
	require.NoError(t, Unmute(tmpDir, "never-muted"))
}
