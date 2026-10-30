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
	gitrepo "github.com/Rinil-Parmar/aros/git"
	"github.com/Rinil-Parmar/aros/human"
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
	installSessionRecorder(arosDir)
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

	// Git: warn before starting if we cannot tell the user's edits from the
	// agents' — the commit at the end would otherwise sweep both up together.
	repo, hasRepo := gitrepo.Open(cwd)
	commitMode := cfg.CommitMode()
	if hasRepo && commitMode != "off" {
		if clean, err := repo.IsClean(); err == nil && !clean {
			fmt.Println("warning: the working tree already has uncommitted changes.")
			fmt.Println("Aros cannot separate them from the agents' edits; the commit at the end")
			fmt.Println("would include both. Commit or stash them first if you want a clean split.")
			ok, err := human.Confirm("Continue anyway?")
			if err != nil {
				ok = true // non-interactive: proceed, the commit gate still guards the write
			}
			if !ok {
				return fmt.Errorf("aborted — commit or stash your changes, then run `aros work` again")
			}
		}
	} else if !hasRepo && commitMode != "off" {
		fmt.Println("note: not a git repository — agent changes will not be recorded as commits.")
	}

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

	// Commit whatever the agents changed, before deciding how the run ended:
	// a partially blocked run still produced work worth recording.
	if hasRepo && commitMode != "off" {
		if cerr := commitRun(repo, commitMode, s.ProjectName, manifest.Tasks); cerr != nil {
			fmt.Printf("warning: %v\n", cerr)
		}
	}

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

// commitRun shows what changed and, with mode "ask", requires approval before
// committing. Aros never pushes: publishing stays the human's decision.
func commitRun(repo *gitrepo.Repo, mode, projectName string, tasks []state.Task) error {
	changes, err := repo.ChangedFiles()
	if err != nil {
		return fmt.Errorf("reading git status: %w", err)
	}
	if len(changes) == 0 {
		fmt.Println("\nNo file changes to commit — the agents reported success but changed nothing.")
		return nil
	}

	fmt.Println()
	for _, line := range gitrepo.Summarize(changes, 20) {
		fmt.Println(line)
	}

	if mode == "ask" {
		ok, err := human.Confirm("Commit these changes?")
		if err != nil {
			// No one to ask (piped or closed stdin): leaving the work
			// uncommitted is the safe answer, and not an error.
			fmt.Println("\nNot committed — no input available to confirm. Run `aros commit` when ready.")
			return nil
		}
		if !ok {
			fmt.Println("Left uncommitted. Review with `git diff`, then commit yourself or run `aros commit`.")
			return nil
		}
	}

	sha, err := repo.CommitAll(gitrepo.CommitMessage(projectName, tasks))
	if errors.Is(err, gitrepo.ErrNothingToCommit) {
		fmt.Println("Nothing to commit.")
		return nil
	}
	if err != nil {
		return fmt.Errorf("committing: %w", err)
	}
	fmt.Printf("Committed %s on %s. Not pushed — run `git push` when you are ready.\n", sha, repo.Branch())
	return nil
}
