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
	assert.False(t, cfg.Orchestration.StopOnError)
	assert.Equal(t, 3, cfg.Orchestration.MaxConsecutiveFailures)
}

func TestLoadOrchestrationStopOnError(t *testing.T) {
	dir := t.TempDir()
	clocheDir := filepath.Join(dir, ".cloche")
	os.MkdirAll(clocheDir, 0755)

	os.WriteFile(filepath.Join(clocheDir, "config.toml"), []byte(`
[orchestration]
stop_on_error = true
concurrency = 1
`), 0644)

	cfg, err := Load(dir)
	require.NoError(t, err)
	assert.True(t, cfg.Orchestration.StopOnError)
	assert.Equal(t, 1, cfg.Orchestration.Concurrency)
}

func TestLoadOrchestrationMaxConsecutiveFailures(t *testing.T) {
	dir := t.TempDir()
	clocheDir := filepath.Join(dir, ".cloche")
	os.MkdirAll(clocheDir, 0755)

	os.WriteFile(filepath.Join(clocheDir, "config.toml"), []byte(`
[orchestration]
max_consecutive_failures = 5
`), 0644)

	cfg, err := Load(dir)
	require.NoError(t, err)
	assert.Equal(t, 5, cfg.Orchestration.MaxConsecutiveFailures)
}

func TestLoadPopulationConfig(t *testing.T) {
	dir := t.TempDir()
	clocheDir := filepath.Join(dir, ".cloche")
	os.MkdirAll(clocheDir, 0755)

	os.WriteFile(filepath.Join(clocheDir, "config.toml"), []byte(`
[evolution]
population_enabled = true
max_candidates = 10
min_runs_to_promote = 3
`), 0644)

	cfg, err := Load(dir)
	require.NoError(t, err)
	assert.True(t, cfg.Evolution.PopulationEnabled)
	assert.Equal(t, 10, cfg.Evolution.MaxCandidates)
	assert.Equal(t, 3, cfg.Evolution.MinRunsToPromote)
}

func TestLoadPopulationConfigDefaults(t *testing.T) {
	dir := t.TempDir()

	cfg, err := Load(dir)
	require.NoError(t, err)
	assert.False(t, cfg.Evolution.PopulationEnabled)
	assert.Equal(t, 5, cfg.Evolution.MaxCandidates)
	assert.Equal(t, 5, cfg.Evolution.MinRunsToPromote)
}

func TestLoadAgentsCodexConfig(t *testing.T) {
	dir := t.TempDir()
	clocheDir := filepath.Join(dir, ".cloche")
	os.MkdirAll(clocheDir, 0755)

	os.WriteFile(filepath.Join(clocheDir, "config.toml"), []byte(`
[agents.codex]
usage_command = "codex usage --last --json"
`), 0644)

	cfg, err := Load(dir)
	require.NoError(t, err)
	assert.Equal(t, "codex usage --last --json", cfg.Agents.Codex.UsageCommand)
}

func TestLoadAgentsCodexConfigDefaults(t *testing.T) {
	dir := t.TempDir()

	cfg, err := Load(dir)
	require.NoError(t, err)
	assert.Equal(t, "", cfg.Agents.Codex.UsageCommand)
}

func TestLoadGlobalFromMissing(t *testing.T) {
	cfg, err := LoadGlobalFrom("/nonexistent/path/config")
	require.NoError(t, err)
	// Returns defaults, no error
	assert.True(t, cfg.Evolution.Enabled)
	assert.Equal(t, "", cfg.Daemon.HTTP)
}

func TestStateDir(t *testing.T) {
	dir := StateDir()
	assert.Contains(t, dir, ".config/cloche")
	assert.True(t, filepath.IsAbs(dir))
}

func TestEnsureStateDir(t *testing.T) {
	// Override HOME to a temp directory so we don't touch the real home.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	dir, err := EnsureStateDir()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(tmp, ".config", "cloche"), dir)

	// Directory should exist
	info, err := os.Stat(dir)
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}

func TestDefaultDBPath(t *testing.T) {
	path := DefaultDBPath()
	assert.Contains(t, path, "cloche.db")
	assert.Contains(t, path, ".config/cloche")
	assert.True(t, filepath.IsAbs(path))
}

func TestDefaultAddr(t *testing.T) {
	addr := DefaultAddr()
	assert.Equal(t, "127.0.0.1:50051", addr)
}

func TestWriteGlobalConfigIfAbsent_CreatesFile(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	path, err := WriteGlobalConfigIfAbsent()
	require.NoError(t, err)

	expected := filepath.Join(tmp, ".config", "cloche", "config")
	assert.Equal(t, expected, path)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), "localhost:8080")
	assert.Contains(t, string(data), "[daemon]")
}

func TestWriteGlobalConfigIfAbsent_SkipsExistingFile(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	dir := filepath.Join(tmp, ".config", "cloche")
	require.NoError(t, os.MkdirAll(dir, 0755))
	configPath := filepath.Join(dir, "config")
	require.NoError(t, os.WriteFile(configPath, []byte("custom content"), 0644))

	path, err := WriteGlobalConfigIfAbsent()
	require.NoError(t, err)
	assert.Equal(t, configPath, path)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "custom content", string(data))
}

func TestWriteGlobalConfigIfAbsent_HTTPDefault(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	_, err := WriteGlobalConfigIfAbsent()
	require.NoError(t, err)

	path := filepath.Join(tmp, ".config", "cloche", "config")
	cfg, err := LoadGlobalFrom(path)
	require.NoError(t, err)
	assert.Equal(t, "localhost:8080", cfg.Daemon.HTTP)
}
