package config

import (
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

type DaemonConfig struct {
	Listen     string `toml:"listen"`
	HTTP       string `toml:"http"`
	Image      string `toml:"image"`
	DB         string `toml:"db"`
	Runtime    string `toml:"runtime"`
	AgentPath  string `toml:"agent_path"`
	LLMCommand string `toml:"llm_command"`
}

type EvolutionConfig struct {
	Enabled          bool   `toml:"enabled"`
	DebounceSeconds  int    `toml:"debounce_seconds"`
	MinConfidence    string `toml:"min_confidence"`
	MaxPromptBullets int    `toml:"max_prompt_bullets"`
	PopulationEnabled  bool `toml:"population_enabled"`
	MaxCandidates      int  `toml:"max_candidates"`
	MinRunsToPromote   int  `toml:"min_runs_to_promote"`
}

type OrchestrationConfig struct {
	Concurrency            int     `toml:"concurrency"`
	StaggerSeconds         float64 `toml:"stagger_seconds"`
	ListTasksCommand       string  `toml:"list_tasks_command"`       // shell command to list open tasks (JSON array output)
	DedupSeconds           float64 `toml:"dedup_seconds"`            // dedup window for task assignment (default: 300)
	StopOnError            bool    `toml:"stop_on_error"`            // halt orchestration loop on unrecovered error
	MaxConsecutiveFailures int     `toml:"max_consecutive_failures"` // halt loop after N consecutive failures (default: 3, must be > 0)
}

type AgentCodexConfig struct {
	UsageCommand string `toml:"usage_command"`
}

type AgentsConfig struct {
	Codex AgentCodexConfig `toml:"codex"`
}

type Config struct {
	Active        bool                `toml:"active"`
	Daemon        DaemonConfig        `toml:"daemon"`
	Evolution     EvolutionConfig     `toml:"evolution"`
	Orchestration OrchestrationConfig `toml:"orchestration"`
	Agents        AgentsConfig        `toml:"agents"`
}

func defaults() Config {
	return Config{
		Evolution: EvolutionConfig{
			Enabled:          true,
			DebounceSeconds:  30,
			MinConfidence:    "medium",
			MaxPromptBullets: 50,
			PopulationEnabled:  false,
			MaxCandidates:      5,
			MinRunsToPromote:   5,
		},
		Orchestration: OrchestrationConfig{
			Concurrency:            1,
			StaggerSeconds:         1.0,
			MaxConsecutiveFailures: 3,
		},
	}
}

// Load reads a per-project config from <projectDir>/.cloche/config.toml.
func Load(projectDir string) (*Config, error) {
	cfg := defaults()
	path := filepath.Join(projectDir, ".cloche", "config.toml")

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &cfg, nil
	}
	if err != nil {
		return nil, err
	}

	if _, err := toml.Decode(string(data), &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// LoadGlobal reads the global daemon config from ~/.config/cloche/config.
// Returns defaults if the file does not exist.
func LoadGlobal() (*Config, error) {
	cfg := defaults()

	home, err := os.UserHomeDir()
	if err != nil {
		return &cfg, nil
	}

	path := filepath.Join(home, ".config", "cloche", "config")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &cfg, nil
	}
	if err != nil {
		return nil, err
	}

	if _, err := toml.Decode(string(data), &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// StateDir returns the path to ~/.config/cloche/ and ensures it exists.
// Falls back to a temp directory if the home directory cannot be determined.
func StateDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "cloche")
	}
	return filepath.Join(home, ".config", "cloche")
}

// EnsureStateDir creates the state directory if it doesn't exist.
func EnsureStateDir() (string, error) {
	dir := StateDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	return dir, nil
}

// DefaultDBPath returns the default database path: ~/.config/cloche/cloche.db
func DefaultDBPath() string {
	return filepath.Join(StateDir(), "cloche.db")
}

// DefaultAddr returns the default gRPC listen address. Binds to all interfaces
// so in-container agents can reach the daemon via host.docker.internal.
func DefaultAddr() string {
	return "0.0.0.0:50051"
}

// defaultGlobalConfigContent is written to ~/.config/cloche/config on first init.
var defaultGlobalConfigContent = `# Cloche global daemon configuration
# This file is read by cloched on startup.
# Override any setting with environment variables (CLOCHE_HTTP, CLOCHE_ADDR, etc.).

[daemon]
# gRPC listen address
# listen = "0.0.0.0:50051"

# Web dashboard — comment out to disable
http = "localhost:8080"

# SQLite database path
# db = "~/.config/cloche/cloche.db"
`

// WriteGlobalConfigIfAbsent creates ~/.config/cloche/config with default values
// if the file does not already exist. Returns the path of the config file.
func WriteGlobalConfigIfAbsent() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".config", "cloche")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "config")
	if _, err := os.Stat(path); err == nil {
		return path, nil // already exists
	}
	if err := os.WriteFile(path, []byte(defaultGlobalConfigContent), 0644); err != nil {
		return "", err
	}
	return path, nil
}

// LoadGlobalFrom reads the global config from a specific path.
// Useful for testing.
func LoadGlobalFrom(path string) (*Config, error) {
	cfg := defaults()

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &cfg, nil
	}
	if err != nil {
		return nil, err
	}

	if _, err := toml.Decode(string(data), &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
