package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Rinil-Parmar/aros/state"
	"github.com/spf13/cobra"
)

var sessionCmd = &cobra.Command{
	Use:   "session",
	Short: "Manage Aros sessions",
}

var sessionNewCmd = &cobra.Command{
	Use:   "new <name>",
	Short: "Create and switch to a new session",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		name := strings.Join(args, " ")
		arosDir, err := ensureArosDir()
		if err != nil {
			return err
		}
		meta, _, err := state.CreateSession(arosDir, name)
		if err != nil {
			return err
		}
		if err := state.SetActiveSession(arosDir, meta.ID); err != nil {
			return err
		}
		fmt.Printf("Created session %q (%s)\n", meta.Name, meta.ID)
		return nil
	},
}

var sessionListCmd = &cobra.Command{
	Use:   "list",
	Short: "List sessions for this project",
	Args:  cobra.NoArgs,
	RunE: func(_ *cobra.Command, _ []string) error {
		arosDir, err := state.ArosDir()
		if err != nil {
			return err
		}
		activeID, _ := state.ActiveSessionID(arosDir)
		sessions, err := state.ListSessions(arosDir)
		if err != nil {
			return err
		}
		if len(sessions) == 0 {
			fmt.Println("No sessions found.")
			return nil
		}
		fmt.Printf("%-4s %-24s %-24s %s\n", "", "ID", "Name", "Updated")
		for _, s := range sessions {
			prefix := " "
			if s.ID == activeID {
				prefix = "*"
			}
			fmt.Printf("%-4s %-24s %-24s %s\n", prefix, s.ID, s.Name, s.UpdatedAt.Format("2006-01-02 15:04:05"))
		}
		return nil
	},
}

var sessionUseCmd = &cobra.Command{
	Use:   "use <id>",
	Short: "Switch to an existing session",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		arosDir, err := state.ArosDir()
		if err != nil {
			return err
		}
		if err := state.SetActiveSession(arosDir, args[0]); err != nil {
			return err
		}
		meta, err := state.ActiveSession(arosDir)
		if err != nil {
			return err
		}
		fmt.Printf("Active session → %s (%s)\n", meta.Name, meta.ID)
		return nil
	},
}

var sessionRemoveForce bool

var sessionRemoveCmd = &cobra.Command{
	Use:   "rm <id>",
	Short: "Remove a session",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		arosDir, err := state.ArosDir()
		if err != nil {
			return err
		}
		activeID, _ := state.ActiveSessionID(arosDir)
		if args[0] == activeID && !sessionRemoveForce {
			return fmt.Errorf("cannot remove active session without --force")
		}
		if err := state.RemoveSession(arosDir, args[0]); err != nil {
			return err
		}
		if args[0] == activeID {
			sessions, err := state.ListSessions(arosDir)
			if err != nil {
				return err
			}
			if len(sessions) > 0 {
				if err := state.SetActiveSession(arosDir, sessions[0].ID); err != nil {
					return err
				}
			} else {
				if err := state.ClearActiveSession(arosDir); err != nil {
					return err
				}
			}
		}
		fmt.Printf("Removed session %s\n", args[0])
		return nil
	},
}

func init() {
	sessionRemoveCmd.Flags().BoolVar(&sessionRemoveForce, "force", false, "remove active session")
	sessionCmd.AddCommand(sessionNewCmd, sessionListCmd, sessionUseCmd, sessionRemoveCmd)
	rootCmd.AddCommand(sessionCmd)
}

func ensureArosDir() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	arosDir := filepath.Join(cwd, ".aros")
	if _, err := os.Stat(arosDir); err != nil {
		if !os.IsNotExist(err) {
			return "", err
		}
		if err := os.MkdirAll(arosDir, 0755); err != nil {
			return "", fmt.Errorf("creating .aros directory: %w", err)
		}
	}
	cfgPath := filepath.Join(arosDir, "config.toml")
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
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
		if err := os.WriteFile(cfgPath, []byte(defaultConfig), 0644); err != nil {
			return "", fmt.Errorf("writing project config: %w", err)
		}
	}
	return arosDir, nil
}
