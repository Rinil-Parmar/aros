package cmd

import (
	"fmt"
	"os"

	"github.com/Rinil-Parmar/aros/tui"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/term"
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
	m := tui.New(cfgFile)
	// Cell motion, not all motion: the TUI only consumes wheel up/down, and
	// all-motion reporting sends a sequence for every pixel the mouse moves.
	// That flood is what pushed split escape sequences into the chat input.
	//
	// SafeInput guards the split itself — see tui/input.go.
	opts := []tea.ProgramOption{tea.WithAltScreen(), tea.WithMouseCellMotion()}
	// Only wrap a real terminal. Naming an input of our own also disables
	// bubbletea's "stdin is not a tty, so open /dev/tty" fallback, which is
	// what keeps `aros < /dev/null` and piped invocations working.
	if term.IsTerminal(os.Stdin.Fd()) {
		opts = append(opts, tea.WithInput(tui.SafeInput(os.Stdin)))
	}
	p := tea.NewProgram(m, opts...)
	tui.SetProgram(p)
	_, err := p.Run()
	return err
}
