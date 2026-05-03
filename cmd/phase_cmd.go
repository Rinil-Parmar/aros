package cmd

import (
	"fmt"
	"strings"

	"github.com/Rinil-Parmar/aros/state"
	"github.com/spf13/cobra"
)

var phaseCmd = &cobra.Command{
	Use:   "phase <init|plan|divide|work|done>",
	Short: "Set the project phase (including moving backward)",
	Args:  cobra.ExactArgs(1),
	RunE:  runPhase,
}

func init() {
	rootCmd.AddCommand(phaseCmd)
}

func runPhase(_ *cobra.Command, args []string) error {
	arosDir, err := state.ArosDir()
	if err != nil {
		return err
	}
	s, err := state.LoadState(arosDir)
	if err != nil {
		return fmt.Errorf("no Aros project found — run `aros init` first: %w", err)
	}
	phaseInput := strings.ToLower(args[0])
	phase, ok := state.ParsePhase(phaseInput)
	if !ok {
		return fmt.Errorf("unknown phase %q (valid: init, plan, divide, work, done)", args[0])
	}
	if s.Phase == phase {
		fmt.Printf("Phase already %q\n", phase)
		return nil
	}
	s.Phase = phase
	if err := state.SaveState(arosDir, s); err != nil {
		return err
	}
	fmt.Printf("Phase set → %s\n", phase)
	return nil
}
