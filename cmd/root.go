package cmd

import (
	"fmt"
	"os"

	"github.com/Rinil-Parmar/aros/tui"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
)

var cfgFile string

var rootCmd = &cobra.Command{
	Use:   "aros",
	Short: "Multi-agent AI orchestrator CLI",
	Long:  "Aros coordinates Claude Code, OpenCode, and other AI agents to plan, divide, and execute software projects collaboratively.",
	// When no subcommand is given, open the interactive TUI.
	RunE: func(cmd *cobra.Command, args []string) error {
		return runTUI()
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default: ~/.aros/config.toml)")
}

func runTUI() error {
	m := tui.New()
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseAllMotion())
	tui.SetProgram(p)
	_, err := p.Run()
	return err
}
