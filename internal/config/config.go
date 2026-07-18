package config

import (
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

type DaemonConfig struct {
	Listen     string `toml:"listen"`
	HTTP       string `toml:"http"`
	Debug      string `toml:"debug"`
	Image      string `toml:"image"`
	DB         string `toml:"db"`
	Runtime    string `toml:"runtime"`
	AgentPath  string `toml:"agent_path"`
	LLMCommand string `toml:"llm_command"`
}

type EvolutionConfig struct {
	Enabled           bool   `toml:"enabled"`
	DebounceSeconds   int    `toml:"debounce_seconds"`
	MinConfidence     string `toml:"min_confidence"`
	MaxPromptBullets  int    `toml:"max_prompt_bullets"`
	PopulationEnabled bool   `toml:"population_enabled"`
	MaxCandidates     int    `toml:"max_candidates"`
	MinRunsToPromote  int    `toml:"min_runs_to_promote"`
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

// AgentConfig controls how prompt steps are dispatched.
// Mode "prompt" (default) launches a headless claude -p process;
// mode "mcp" parks prompt steps until an MCP client claims and completes them.
type AgentConfig struct {
	Mode string `toml:"mode"` // "prompt" (default) or "mcp"
}

// HelpConfig controls the help channel: how long an AskHelp call blocks in
// place before parking (parking itself lands in a later phase), how long
// archived threads are retained before the daemon's daily sweep deletes them,
// and which user-side HelpChannel integrations (e.g. Slack) are configured.
type HelpConfig struct {
	ParkAfter string              `toml:"park_after"` // duration string, e.g. "5m"; default 5m
	Retention string              `toml:"retention"`  // duration string, e.g. "720h"; default 720h (30 days)
	Channels  []HelpChannelConfig `toml:"channel"`    // [[help.channel]] entries; unknown Type fails daemon start
}

// HelpChannelConfig configures one user-side HelpChannel integration.
// Corresponds to a `[[help.channel]]` table in the daemon config, e.g.:
//
//	[[help.channel]]
//	type          = "slack"
//	channel       = "#cloche"
//	token_env     = "SLACK_BOT_TOKEN"
//	app_token_env = "SLACK_APP_TOKEN"
type HelpChannelConfig struct {
	Type        string `toml:"type"` // "slack" (only supported type so far)
	Channel     string `toml:"channel"`
	TokenEnv    string `toml:"token_env"`
	AppTokenEnv string `toml:"app_token_env"`
	// ChannelMap optionally routes distinct cloche channels (project names) to
	// distinct Slack channels, e.g. { mazd = "#mazd-cloche" }. Cloche channels
	// absent from the map fall back to Channel.
	ChannelMap map[string]string `toml:"channel_map"`
}

// GitConfig controls the git identity used for cloche-authored commits
// (extraction commits and scaffolded merge scripts). When unset, commits
// fall back to the built-in "cloche <cloche@local>" identity. SSHKey is a
// path to a private key used for git push in workflow scripts; the host
// executor composes it into CLOCHE_GIT_SSH_COMMAND for scripts to consume.
type GitConfig struct {
	Name   string `toml:"name"`
	Email  string `toml:"email"`
	SSHKey string `toml:"ssh_key"`
}

// RepositoryConfig describes a repository entry declared in a project's
// .cloche/config.toml via [[repositories]]. Path is stored as declared
// (relative to the project root).
type RepositoryConfig struct {
	Name string `toml:"name"`
	Path string `toml:"path"`
	URL  string `toml:"url"`
}

type Config struct {
	Active        bool                `toml:"active"`
	Daemon        DaemonConfig        `toml:"daemon"`
	Evolution     EvolutionConfig     `toml:"evolution"`
	Orchestration OrchestrationConfig `toml:"orchestration"`
	Agents        AgentsConfig        `toml:"agents"`
	Agent         AgentConfig         `toml:"agent"`
	Git           GitConfig           `toml:"git"`
	Help          HelpConfig          `toml:"help"`
	Repositories  []RepositoryConfig  `toml:"repositories"`
}

func defaults() Config {
	return Config{
		Agent: AgentConfig{
			Mode: "prompt",
		},
		Evolution: EvolutionConfig{
			Enabled:           true,
			DebounceSeconds:   30,
			MinConfidence:     "medium",
			MaxPromptBullets:  50,
			PopulationEnabled: false,
			MaxCandidates:     5,
			MinRunsToPromote:  5,
		},
		Orchestration: OrchestrationConfig{
			Concurrency:            1,
			StaggerSeconds:         1.0,
			MaxConsecutiveFailures: 3,
		},
		Help: HelpConfig{
			ParkAfter: "5m",
			Retention: "720h",
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

// LoadMerged returns a Config where global values are overlaid by per-project
// values. Project settings win when set (non-zero for strings/ints/bools).
// Missing files are treated as empty overlays.
func LoadMerged(projectDir string) (*Config, error) {
	global, err := LoadGlobal()
	if err != nil {
		return nil, err
	}
	project, err := Load(projectDir)
	if err != nil {
		return nil, err
	}
	mergeInto(global, project)
	return global, nil
}

// mergeInto overlays non-zero fields from src onto dst. Currently only fields
// that can be meaningfully overridden per-project are merged here; extend as
// new overrideable settings are added.
func mergeInto(dst, src *Config) {
	if src.Git.Name != "" {
		dst.Git.Name = src.Git.Name
	}
	if src.Git.Email != "" {
		dst.Git.Email = src.Git.Email
	}
	if src.Git.SSHKey != "" {
		dst.Git.SSHKey = src.Git.SSHKey
	}
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

// DefaultLogPath returns the default path for the daemon's stdout/stderr log:
// ~/.config/cloche/cloched.log. Honors CLOCHE_LOG if set, so `cloche shutdown
// --restart` and `cloched` agree on where daemon output goes.
func DefaultLogPath() string {
	if p := os.Getenv("CLOCHE_LOG"); p != "" {
		return p
	}
	return filepath.Join(StateDir(), "cloched.log")
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

# Git identity used for cloche-authored commits (container extraction and
# scaffolded merge scripts). When unset, commits attribute to "cloche
# <cloche@local>". Per-project overrides go in <project>/.cloche/config.toml.
# [git]
# name = "cloche-bot"
# email = "cloche-bot@users.noreply.github.com"
# ssh_key = "~/.ssh/cloche_bot"  # used by workflow scripts that push

# Help channel: agent questions always flow via "cloche threads"; configuring
# a channel below also delivers them to it (e.g. Slack via Socket Mode).
# [[help.channel]]
# type          = "slack"
# channel       = "#cloche"           # default Slack channel
# token_env     = "SLACK_BOT_TOKEN"   # env var holding the bot token (xoxb-...)
# app_token_env = "SLACK_APP_TOKEN"   # env var holding the app-level token (xapp-...)
# [help.channel.channel_map]          # optional: route cloche channels to distinct Slack channels
# mazd = "#mazd-cloche"
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
