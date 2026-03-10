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

	os.WriteFile(filepath.Join(clocheDir, "config.toml"), []byte(`
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

func TestLoadDaemonConfig(t *testing.T) {
	dir := t.TempDir()
	clocheDir := filepath.Join(dir, ".cloche")
	os.MkdirAll(clocheDir, 0755)

	os.WriteFile(filepath.Join(clocheDir, "config.toml"), []byte(`
[daemon]
listen = "localhost:9090"
http = "localhost:8080"
image = "my-agent:v2"
db = "/var/lib/cloche/data.db"
runtime = "local"
agent_path = "/usr/local/bin/cloche-agent"
llm_command = "claude"
`), 0644)

	cfg, err := Load(dir)
	require.NoError(t, err)
	assert.Equal(t, "localhost:9090", cfg.Daemon.Listen)
	assert.Equal(t, "localhost:8080", cfg.Daemon.HTTP)
	assert.Equal(t, "my-agent:v2", cfg.Daemon.Image)
	assert.Equal(t, "/var/lib/cloche/data.db", cfg.Daemon.DB)
	assert.Equal(t, "local", cfg.Daemon.Runtime)
	assert.Equal(t, "/usr/local/bin/cloche-agent", cfg.Daemon.AgentPath)
	assert.Equal(t, "claude", cfg.Daemon.LLMCommand)
}

func TestLoadDaemonConfigDefaults(t *testing.T) {
	dir := t.TempDir()

	cfg, err := Load(dir)
	require.NoError(t, err)
	// All daemon fields default to zero values (empty strings)
	assert.Equal(t, "", cfg.Daemon.Listen)
	assert.Equal(t, "", cfg.Daemon.HTTP)
	assert.Equal(t, "", cfg.Daemon.Image)
	assert.Equal(t, "", cfg.Daemon.DB)
	assert.Equal(t, "", cfg.Daemon.Runtime)
}

func TestLoadGlobalFrom(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config")

	os.WriteFile(configPath, []byte(`
[daemon]
http = "0.0.0.0:3000"
image = "custom:latest"
`), 0644)

	cfg, err := LoadGlobalFrom(configPath)
	require.NoError(t, err)
	assert.Equal(t, "0.0.0.0:3000", cfg.Daemon.HTTP)
	assert.Equal(t, "custom:latest", cfg.Daemon.Image)
	assert.Equal(t, "", cfg.Daemon.Listen) // unset stays empty
}

func TestLoadActiveFlag(t *testing.T) {
	dir := t.TempDir()
	clocheDir := filepath.Join(dir, ".cloche")
	os.MkdirAll(clocheDir, 0755)

	os.WriteFile(filepath.Join(clocheDir, "config.toml"), []byte(`
active = true
`), 0644)

	cfg, err := Load(dir)
	require.NoError(t, err)
	assert.True(t, cfg.Active)
}

func TestLoadActiveFlagDefault(t *testing.T) {
	dir := t.TempDir()

	cfg, err := Load(dir)
	require.NoError(t, err)
	assert.False(t, cfg.Active)
}

func TestLoadOrchestrationConfig(t *testing.T) {
	dir := t.TempDir()
	clocheDir := filepath.Join(dir, ".cloche")
	os.MkdirAll(clocheDir, 0755)

	os.WriteFile(filepath.Join(clocheDir, "config.toml"), []byte(`
[orchestration]
concurrency = 2
stagger_seconds = 3.5
`), 0644)

	cfg, err := Load(dir)
	require.NoError(t, err)
	assert.Equal(t, 2, cfg.Orchestration.Concurrency)
	assert.Equal(t, 3.5, cfg.Orchestration.StaggerSeconds)
}

func TestLoadOrchestrationConfigDefaults(t *testing.T) {
	dir := t.TempDir()

	cfg, err := Load(dir)
	require.NoError(t, err)
	assert.Equal(t, 1, cfg.Orchestration.Concurrency)
	assert.Equal(t, 1.0, cfg.Orchestration.StaggerSeconds)
}

func TestLoadGlobalFromMissing(t *testing.T) {
	cfg, err := LoadGlobalFrom("/nonexistent/path/config")
	require.NoError(t, err)
	// Returns defaults, no error
	assert.True(t, cfg.Evolution.Enabled)
	assert.Equal(t, "", cfg.Daemon.HTTP)
}
