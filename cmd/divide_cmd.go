package cmd

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/Rinil-Parmar/aros/agent"
	"github.com/Rinil-Parmar/aros/config"
	"github.com/Rinil-Parmar/aros/divider"
	"github.com/Rinil-Parmar/aros/memory"
	"github.com/Rinil-Parmar/aros/state"
	"github.com/spf13/cobra"
)

var divideForce bool

var divideCmd = &cobra.Command{
	Use:   "divide",
	Short: "Break the approved plan into tasks and assign them to agents",
	Args:  cobra.NoArgs,
	RunE:  runDivide,
}

func init() {
	divideCmd.Flags().BoolVar(&divideForce, "force", false, "re-run divide even if already in a later phase")
	rootCmd.AddCommand(divideCmd)
}

func runDivide(_ *cobra.Command, _ []string) error {
	arosDir, err := state.ArosDir()
	if err != nil {
		return err
	}

	s, err := state.LoadState(arosDir)
	if err != nil {
		return fmt.Errorf("no Aros project found — run `aros init` and `aros plan` first: %w", err)
	}
	if err := state.RequirePhase(s, divideForce, state.PhasePlan); err != nil {
		return err
	}

	cfg, err := config.Load(cfgFile)
	if err != nil {
		return err
	}

	cwd := filepath.Dir(arosDir)
	reg, err := agent.BuildRegistry(cfg, cwd)
	if err != nil {
		return err
	}

	mem := memory.New(cfg.SecondMem.Binary, cfg.SecondMem.Enabled)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	manifest, err := divider.Run(ctx, s, reg, cfg, mem)
	if err != nil {
		return err
	}

	if err := state.SaveManifest(arosDir, manifest); err != nil {
		return err
	}
	s.Phase = state.PhaseDivide
	if err := state.SaveState(arosDir, s); err != nil {
		return err
	}

	fmt.Printf("Tasks saved. Run `aros work` to start execution.\n")
	return nil
}
