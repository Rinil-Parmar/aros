package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/viper"
)

type Config struct {
	Judge    JudgeConfig             `mapstructure:"judge"`
	Agents   map[string]AgentConfig  `mapstructure:"agents"`
	SecondMem SecondMemConfig        `mapstructure:"secondmem"`
	Work     WorkConfig              `mapstructure:"work"`
}

type JudgeConfig struct {
	Agent string `mapstructure:"agent"`
}

type AgentConfig struct {
	Enabled   bool     `mapstructure:"enabled"`
	Model     string   `mapstructure:"model"`
	Strengths []string `mapstructure:"strengths"`
	Dense     string   `mapstructure:"dense"` // "" (off), "lite", "full", "ultra"
	// DangerouslySkipPerms is only used in the work phase
	DangerouslySkipPerms bool `mapstructure:"dangerously_skip_perms"`
}

type SecondMemConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Binary  string `mapstructure:"binary"`
}

type WorkConfig struct {
	MaxConcurrent        int `mapstructure:"max_concurrent"`
	AgentTimeoutSeconds  int `mapstructure:"agent_timeout_seconds"`
}

func DefaultGlobalPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".aros"
	}
	return filepath.Join(home, ".aros")
}

// Load reads global config from ~/.aros/config.toml, then merges project-level
// .aros/config.toml on top. cfgFile overrides the global path if provided.
func Load(cfgFile string) (*Config, error) {
	v := viper.New()

	setDefaults(v)

	if cfgFile != "" {
		v.SetConfigFile(cfgFile)
	} else {
		v.AddConfigPath(DefaultGlobalPath())
		v.SetConfigName("config")
		v.SetConfigType("toml")
	}

	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("reading global config: %w", err)
		}
	}

	// Merge project-level config if present
	cwd, err := os.Getwd()
	if err == nil {
		projectCfg := filepath.Join(cwd, ".aros", "config.toml")
		if _, err := os.Stat(projectCfg); err == nil {
			v.SetConfigFile(projectCfg)
			if err := v.MergeInConfig(); err != nil {
				return nil, fmt.Errorf("merging project config: %w", err)
			}
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	return &cfg, nil
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("judge.agent", "claude")

	// Defaults use cheap/fast models to minimize token usage.
	// Override via: aros config set agents.claude.model claude-sonnet-4-5
	v.SetDefault("agents.claude.enabled", true)
	v.SetDefault("agents.claude.model", "haiku")
	v.SetDefault("agents.claude.strengths", []string{"architecture", "reasoning", "docs", "planning"})
	v.SetDefault("agents.claude.dense", "full")
	v.SetDefault("agents.claude.dangerously_skip_perms", false)

	v.SetDefault("agents.opencode.enabled", true)
	v.SetDefault("agents.opencode.model", "openai/gpt-4o-mini")
	v.SetDefault("agents.opencode.strengths", []string{"implementation", "debugging", "testing"})
	v.SetDefault("agents.opencode.dense", "full")

	v.SetDefault("agents.copilot.enabled", true)
	v.SetDefault("agents.copilot.model", "gpt-4.1")
	v.SetDefault("agents.copilot.strengths", []string{"implementation", "debugging", "refactoring"})
	v.SetDefault("agents.copilot.dense", "full")

	v.SetDefault("secondmem.enabled", true)
	v.SetDefault("secondmem.binary", "secondmem")

	v.SetDefault("work.max_concurrent", 2)
	v.SetDefault("work.agent_timeout_seconds", 300)
}

// GlobalViper returns a Viper instance for config write-back (used by config set command).
func GlobalViper(cfgFile string) (*viper.Viper, string, error) {
	v := viper.New()
	setDefaults(v)

	path := cfgFile
	if path == "" {
		path = filepath.Join(DefaultGlobalPath(), "config.toml")
	}
	v.SetConfigFile(path)
	_ = v.ReadInConfig() // ignore not found
	return v, path, nil
}
