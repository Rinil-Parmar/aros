package cmd

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/Rinil-Parmar/aros/agent"
	"github.com/Rinil-Parmar/aros/config"
	"github.com/Rinil-Parmar/aros/memory"
	"github.com/Rinil-Parmar/aros/planner"
	"github.com/Rinil-Parmar/aros/state"
	"github.com/spf13/cobra"
)

var planForce bool

var planCmd = &cobra.Command{
	Use:   "plan <task-description>",
	Short: "Generate and approve a project plan using multiple AI agents",
	Args:  cobra.MinimumNArgs(1),
	RunE:  runPlan,
}

func init() {
	planCmd.Flags().BoolVar(&planForce, "force", false, "allow re-running plan even if already in a later phase")
	rootCmd.AddCommand(planCmd)
}

func runPlan(cmd *cobra.Command, args []string) error {
	task := joinArgs(args)

	arosDir, err := state.ArosDir()
	if err != nil {
		return err
	}

	s, err := state.LoadState(arosDir)
	if err != nil {
		return fmt.Errorf("no Aros project found — run `aros init <name>` first: %w", err)
	}
	if err := state.RequirePhase(s, planForce, state.PhaseInit, state.PhasePlan); err != nil {
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

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	approvedPlan, err := planner.Run(ctx, task, reg, cfg.Judge.Agent, mem)
	if err != nil {
		return err
	}

	s.Task = task
	s.ApprovedPlan = approvedPlan
	s.Phase = state.PhasePlan
	if err := state.SaveState(arosDir, s); err != nil {
		return err
	}

	if err := mem.Ingest(ctx, fmt.Sprintf("Approved plan for %s:\n%s", s.ProjectName, approvedPlan)); err != nil {
		fmt.Printf("warning: could not ingest plan to secondmem: %v\n", err)
	}

	fmt.Printf("\nPlan approved and saved. Run `aros divide` to assign tasks.\n")
	return nil
}

func joinArgs(args []string) string {
	result := ""
	for i, a := range args {
		if i > 0 {
			result += " "
		}
		result += a
	}
	return result
}
