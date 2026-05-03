package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Rinil-Parmar/aros/state"
	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init <project-name>",
	Short: "Initialize a new Aros project in the current directory",
	Args:  cobra.ExactArgs(1),
	RunE:  runInit,
}

func init() {
	rootCmd.AddCommand(initCmd)
}

func runInit(cmd *cobra.Command, args []string) error {
	projectName := args[0]

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	arosDir := filepath.Join(cwd, ".aros")

	if _, err := os.Stat(arosDir); err == nil {
		return fmt.Errorf(".aros/ already exists in this directory — use `aros session new %q`", projectName)
	}

	if err := os.MkdirAll(arosDir, 0755); err != nil {
		return fmt.Errorf("creating .aros directory: %w", err)
	}

	meta, _, err := state.CreateSession(arosDir, projectName)
	if err != nil {
		return fmt.Errorf("creating session: %w", err)
	}
	if err := state.SetActiveSession(arosDir, meta.ID); err != nil {
		return fmt.Errorf("setting active session: %w", err)
	}

	// Write default project config
	defaultConfig := `# Aros project config — overrides ~/.aros/config.toml
# Uncomment and edit to customize per-project settings.

# [judge]
# agent = "claude"

# [agents.claude]
# model = "claude-opus-4-7"

# [agents.opencode]
# model = "openai/gpt-4o"
# enabled = true
`
	cfgPath := filepath.Join(arosDir, "config.toml")
	if err := os.WriteFile(cfgPath, []byte(defaultConfig), 0644); err != nil {
		return fmt.Errorf("writing project config: %w", err)
	}

	fmt.Printf("Initialized Aros project: %q\n", projectName)
	fmt.Printf("  Config: %s\n", cfgPath)
	fmt.Println()
	fmt.Println("Next steps:")
	fmt.Printf("  aros plan \"describe your task or feature\"\n")
	return nil
}
