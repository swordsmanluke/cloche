package config

import (
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

type EvolutionConfig struct {
	Enabled          bool   `toml:"enabled"`
	DebounceSeconds  int    `toml:"debounce_seconds"`
	MinConfidence    string `toml:"min_confidence"`
	MaxPromptBullets int    `toml:"max_prompt_bullets"`
}

type Config struct {
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
