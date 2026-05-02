package cmd

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/Rinil-Parmar/aros/agent"
	"github.com/Rinil-Parmar/aros/config"
	"github.com/Rinil-Parmar/aros/memory"
	"github.com/Rinil-Parmar/aros/state"
	"github.com/Rinil-Parmar/aros/worker"
	"github.com/spf13/cobra"
)

var workCmd = &cobra.Command{
	Use:   "work",
	Short: "Execute assigned tasks using the configured agents",
	Args:  cobra.NoArgs,
	RunE:  runWork,
}

func init() {
	rootCmd.AddCommand(workCmd)
}

func runWork(_ *cobra.Command, _ []string) error {
	arosDir, err := state.ArosDir()
	if err != nil {
		return err
	}

	s, err := state.LoadState(arosDir)
	if err != nil {
		return fmt.Errorf("no Aros project found: %w", err)
	}
	if err := state.RequirePhase(s, false, state.PhaseDivide); err != nil {
		return err
	}

	manifest, err := state.LoadManifest(arosDir)
	if err != nil {
		return fmt.Errorf("loading manifest: %w", err)
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

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Hour)
	defer cancel()

	if err := worker.Run(ctx, manifest, arosDir, reg, mem, cfg.Work.MaxConcurrent, cfg.Work.AgentTimeoutSeconds); err != nil {
		return err
	}

	s.Phase = state.PhaseDone
	if err := state.SaveState(arosDir, s); err != nil {
		return fmt.Errorf("saving final state: %w", err)
	}

	fmt.Printf("\nProject %q complete!\n", s.ProjectName)
	return nil
}
