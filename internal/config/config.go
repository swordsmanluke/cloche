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
}

type Config struct {
	Daemon    DaemonConfig    `toml:"daemon"`
	Evolution EvolutionConfig `toml:"evolution"`
}

func defaults() Config {
	return Config{
		Evolution: EvolutionConfig{
			Enabled:          true,
			DebounceSeconds:  30,
			MinConfidence:    "medium",
			MaxPromptBullets: 50,
		},
	}
}

// Load reads a per-project config from <projectDir>/.cloche/config.
func Load(projectDir string) (*Config, error) {
	cfg := defaults()
	path := filepath.Join(projectDir, ".cloche", "config")

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
