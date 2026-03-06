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

func TestLoadDaemonConfig(t *testing.T) {
	dir := t.TempDir()
	clocheDir := filepath.Join(dir, ".cloche")
	os.MkdirAll(clocheDir, 0755)

	os.WriteFile(filepath.Join(clocheDir, "config"), []byte(`
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

func TestLoadOrchestrationConfig(t *testing.T) {
	dir := t.TempDir()
	clocheDir := filepath.Join(dir, ".cloche")
	os.MkdirAll(clocheDir, 0755)

	os.WriteFile(filepath.Join(clocheDir, "config"), []byte(`
[orchestration]
enabled = true
tracker = "beads"
concurrency = 3
workflow = "build"
`), 0644)

	cfg, err := Load(dir)
	require.NoError(t, err)
	assert.True(t, cfg.Orchestration.Enabled)
	assert.Equal(t, "beads", cfg.Orchestration.Tracker)
	assert.Equal(t, 3, cfg.Orchestration.Concurrency)
	assert.Equal(t, "build", cfg.Orchestration.Workflow)
}

func TestLoadOrchestrationConfigDefaults(t *testing.T) {
	dir := t.TempDir()

	cfg, err := Load(dir)
	require.NoError(t, err)
	// Defaults: not enabled, beads tracker, concurrency 1, develop workflow
	assert.False(t, cfg.Orchestration.Enabled)
	assert.Equal(t, "beads", cfg.Orchestration.Tracker)
	assert.Equal(t, 1, cfg.Orchestration.Concurrency)
	assert.Equal(t, "develop", cfg.Orchestration.Workflow)
}

func TestLoadVersionDefault(t *testing.T) {
	dir := t.TempDir()
	v, err := LoadVersion(dir)
	require.NoError(t, err)
	assert.Equal(t, 1, v)
}

func TestLoadVersionFromFile(t *testing.T) {
	dir := t.TempDir()
	clocheDir := filepath.Join(dir, ".cloche")
	os.MkdirAll(clocheDir, 0755)
	os.WriteFile(filepath.Join(clocheDir, "version"), []byte("5\n"), 0644)

	v, err := LoadVersion(dir)
	require.NoError(t, err)
	assert.Equal(t, 5, v)
}

func TestIncrementProjectVersion(t *testing.T) {
	dir := t.TempDir()
	clocheDir := filepath.Join(dir, ".cloche")
	os.MkdirAll(clocheDir, 0755)

	// No version file yet — should go from implicit 1 to 2
	err := IncrementProjectVersion(dir)
	require.NoError(t, err)

	v, err := LoadVersion(dir)
	require.NoError(t, err)
	assert.Equal(t, 2, v)

	// Increment again — should go to 3
	err = IncrementProjectVersion(dir)
	require.NoError(t, err)

	v, err = LoadVersion(dir)
	require.NoError(t, err)
	assert.Equal(t, 3, v)

	// Verify file content
	data, err := os.ReadFile(filepath.Join(clocheDir, "version"))
	require.NoError(t, err)
	assert.Equal(t, "3\n", string(data))
}

func TestLoadGlobalFromMissing(t *testing.T) {
	cfg, err := LoadGlobalFrom("/nonexistent/path/config")
	require.NoError(t, err)
	// Returns defaults, no error
	assert.True(t, cfg.Evolution.Enabled)
	assert.Equal(t, "", cfg.Daemon.HTTP)
}
