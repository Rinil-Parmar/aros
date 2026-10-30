package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Rinil-Parmar/aros/config"
	"github.com/spf13/cobra"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage Aros configuration",
}

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Display current configuration",
	Args:  cobra.NoArgs,
	RunE:  runConfigShow,
}

var configSetCmd = &cobra.Command{
	Use:   "set <key> <value>",
	Short: "Set a configuration value",
	Example: `  aros config set judge.agent claude
  aros config set agents.claude.model sonnet
  aros config set agents.opencode.model openai/gpt-5.4-mini
  aros config set work.max_concurrent 3`,
	Args: cobra.ExactArgs(2),
	RunE: runConfigSet,
}

func init() {
	configCmd.AddCommand(configShowCmd, configSetCmd)
	rootCmd.AddCommand(configCmd)
}

func runConfigShow(_ *cobra.Command, _ []string) error {
	cfg, err := config.Load(cfgFile)
	if err != nil {
		return err
	}

	fmt.Printf("Judge agent:     %s\n", cfg.Judge.Agent)
	fmt.Printf("SecondMem:       enabled=%v binary=%s\n", cfg.SecondMem.Enabled, cfg.SecondMem.Binary)
	fmt.Printf("Work concurrency: %d  timeout: %ds\n\n", cfg.Work.MaxConcurrent, cfg.Work.AgentTimeoutSeconds)

	fmt.Println("Agents:")
	for name, ac := range cfg.Agents {
		status := "disabled"
		if ac.Enabled {
			status = "enabled"
		}
		fmt.Printf("  %-15s %s  model=%s  strengths=%s\n",
			name, status, ac.Model, strings.Join(ac.Strengths, ","))
	}
	return nil
}

func runConfigSet(_ *cobra.Command, args []string) error {
	key, value := args[0], args[1]

	v, path, err := config.GlobalViper(cfgFile)
	if err != nil {
		return err
	}

	// The config directory may not exist yet on a fresh install — WriteConfigAs
	// does not create it, so `aros config set` would fail on first use.
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}

	// Auto-cast. Integers are tried first: strconv.ParseBool accepts "1" and
	// "0", so checking bools first wrote `max_concurrent = true` into the file
	// for `config set work.max_concurrent 1`.
	if n, err := strconv.Atoi(value); err == nil {
		v.Set(key, n)
	} else if b, err := strconv.ParseBool(value); err == nil {
		v.Set(key, b)
	} else {
		v.Set(key, value)
	}

	if err := v.WriteConfigAs(path); err != nil {
		return fmt.Errorf("writing config to %s: %w", path, err)
	}
	fmt.Printf("Set %s = %s  (saved to %s)\n", key, value, path)
	return nil
}
