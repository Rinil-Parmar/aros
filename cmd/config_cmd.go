package cmd

import (
	"fmt"
	"os"
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

var (
	configNoVerify bool
	configProject  bool
)

func init() {
	configSetCmd.Flags().BoolVar(&configNoVerify, "no-verify", false,
		"skip checking that an agent supports the model")
	configSetCmd.Flags().BoolVar(&configProject, "project", false,
		"write to ./.aros/config.toml instead of the global config")
	configCmd.AddCommand(configShowCmd, configSetCmd)
	rootCmd.AddCommand(configCmd)
}

// modelKeyAgent recognises "agents.<name>.model" and returns <name>.
func modelKeyAgent(key string) (string, bool) {
	parts := strings.Split(key, ".")
	if len(parts) == 3 && parts[0] == "agents" && parts[2] == "model" {
		return parts[1], true
	}
	return "", false
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

	path := cfgFile
	if path == "" {
		path = config.GlobalConfigPath()
	}
	if configProject {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		path = config.ProjectPath(cwd)
	}

	// Setting a model is the one key worth checking before it lands in the
	// file: a wrong name is otherwise only discovered mid-phase.
	if a, ok := modelKeyAgent(key); ok && !configNoVerify {
		warning, verr := verifyModelChoice(a, value)
		if verr != nil {
			return fmt.Errorf("%w\n(use --no-verify to set it anyway)", verr)
		}
		if warning != "" {
			fmt.Println("note:", warning)
		}
	}

	// Auto-cast. Integers are tried first: strconv.ParseBool accepts "1" and
	// "0", so checking bools first wrote `max_concurrent = true` into the file
	// for `config set work.max_concurrent 1`.
	var typed any = value
	if n, err := strconv.Atoi(value); err == nil {
		typed = n
	} else if b, err := strconv.ParseBool(value); err == nil {
		typed = b
	}

	// Writes only this key. The previous implementation serialised the whole
	// resolved config, which baked every current default into the user's file.
	if err := config.SetValue(path, key, typed); err != nil {
		return err
	}
	fmt.Printf("Set %s = %s  (saved to %s)\n", key, value, path)
	return nil
}
