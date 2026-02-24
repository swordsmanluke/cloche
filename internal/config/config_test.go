package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadEvolutionConfig(t *testing.T) {
	dir := t.TempDir()
	clocheDir := filepath.Join(dir, ".cloche")
	os.MkdirAll(clocheDir, 0755)

	os.WriteFile(filepath.Join(clocheDir, "config"), []byte(`
[evolution]
enabled = true
debounce_seconds = 45
min_confidence = "high"
max_prompt_bullets = 30
`), 0644)

	cfg, err := Load(dir)
	require.NoError(t, err)
	assert.True(t, cfg.Evolution.Enabled)
	assert.Equal(t, 45, cfg.Evolution.DebounceSeconds)
	assert.Equal(t, "high", cfg.Evolution.MinConfidence)
	assert.Equal(t, 30, cfg.Evolution.MaxPromptBullets)
}

func TestLoadEvolutionConfigDefaults(t *testing.T) {
	dir := t.TempDir()

	cfg, err := Load(dir)
	require.NoError(t, err)
	assert.True(t, cfg.Evolution.Enabled)
	assert.Equal(t, 30, cfg.Evolution.DebounceSeconds)
	assert.Equal(t, "medium", cfg.Evolution.MinConfidence)
	assert.Equal(t, 50, cfg.Evolution.MaxPromptBullets)
}
