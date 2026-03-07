package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

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
}

type OrchestrationConfig struct {
	Enabled     bool   `toml:"enabled"`
	Tracker     string `toml:"tracker"`
	Concurrency int    `toml:"concurrency"`
	Workflow    string `toml:"workflow"`
}

type Config struct {
	Daemon        DaemonConfig        `toml:"daemon"`
	Evolution     EvolutionConfig     `toml:"evolution"`
	Orchestration OrchestrationConfig `toml:"orchestration"`
}

func defaults() Config {
	return Config{
		Evolution: EvolutionConfig{
			Enabled:          true,
			DebounceSeconds:  30,
			MinConfidence:    "medium",
			MaxPromptBullets: 50,
		},
		Orchestration: OrchestrationConfig{
			Tracker:     "beads",
			Concurrency: 1,
			Workflow:     "develop",
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

// LoadVersion reads the project version from <projectDir>/.cloche/version.
// Returns 1 if the file does not exist.
func LoadVersion(projectDir string) (int, error) {
	path := filepath.Join(projectDir, ".cloche", "version")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return 1, nil
	}
	if err != nil {
		return 0, err
	}
	v, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("invalid version file: %w", err)
	}
	return v, nil
}

// IncrementVersion increments the integer in <projectDir>/.cloche/version.
// If the file does not exist, it treats the current version as 1 and writes 2.
func IncrementVersion(projectDir string) error {
	v, err := LoadVersion(projectDir)
	if err != nil {
		return err
	}
	path := filepath.Join(projectDir, ".cloche", "version")
	return os.WriteFile(path, []byte(fmt.Sprintf("%d\n", v+1)), 0644)
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
