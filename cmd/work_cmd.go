package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
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
	if err := state.RequirePhase(s, false, state.PhaseDivide, state.PhaseWork); err != nil {
		return err
	}

	manifest, err := state.LoadManifest(arosDir)
	if err != nil {
		return fmt.Errorf("loading manifest: %w", err)
	}
	if len(manifest.Tasks) == 0 {
		return fmt.Errorf("no tasks to execute; run `aros divide` first")
	}
	allDone := true
	for _, t := range manifest.Tasks {
		if t.Status != state.TaskDone {
			allDone = false
			break
		}
	}
	if allDone {
		s.Phase = state.PhaseDone
		_ = state.SaveState(arosDir, s)
		fmt.Printf("All tasks are already complete.\n")
		return nil
	}
	s.Phase = state.PhaseWork
	if err := state.SaveState(arosDir, s); err != nil {
		return fmt.Errorf("saving work phase state: %w", err)
	}

	cfg, err := config.Load(cfgFile)
	if err != nil {
		return err
	}

	cwd := filepath.Dir(arosDir)
	reg, warnings, err := agent.BuildRegistry(cfg, cwd)
	for _, w := range warnings {
		fmt.Println("warning:", w)
	}
	if err != nil {
		return err
	}

	mem := memory.New(cfg.SecondMem.Binary, cfg.SecondMem.Enabled)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	ctx, cancelTimeout := context.WithTimeout(ctx, 4*time.Hour)
	defer cancelTimeout()

	fmt.Printf("\n=== WORK PHASE ===\n%d tasks to execute (max %d concurrent)\n\n", len(manifest.Tasks), cfg.Work.MaxConcurrent)
	opts := worker.Options{
		MaxConcurrent: cfg.Work.MaxConcurrent,
		TimeoutSec:    cfg.Work.AgentTimeoutSeconds,
		FallbackAgent: cfg.Judge.Agent,
	}
	hooks := worker.Hooks{
		Log: func(line string) { fmt.Println(line) },
		TaskStart: func(t *state.Task, agentName string) {
			fmt.Printf("[%s] starting → %s (%s)\n", t.ID, t.Title, agentName)
		},
		TaskDone: func(t *state.Task, agentName string) {
			fmt.Printf("[%s] done ✓\n", t.ID)
		},
		TaskBlocked: func(t *state.Task, agentName, reason string) {
			fmt.Printf("[%s] blocked ✗ — %s\n", t.ID, reason)
		},
	}

	res, err := worker.Run(ctx, manifest, arosDir, reg, mem, opts, hooks)
	if err != nil {
		if errors.Is(err, worker.ErrTasksBlocked) {
			// Phase stays "work" so `aros work` retries the blocked tasks.
			fmt.Printf("\n%d of %d task(s) done, %d blocked. Fix the blockers (see `aros status`) and run `aros work` again.\n", res.Done, res.Total, res.Blocked)
			return nil
		}
		return err
	}

	s.Phase = state.PhaseDone
	if err := state.SaveState(arosDir, s); err != nil {
		return fmt.Errorf("saving final state: %w", err)
	}

	fmt.Printf("\n=== All tasks complete ===\nProject %q complete!\n", s.ProjectName)
	return nil
}
