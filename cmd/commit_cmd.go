package cmd

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/Rinil-Parmar/aros/config"
	gitrepo "github.com/Rinil-Parmar/aros/git"
	"github.com/Rinil-Parmar/aros/state"
	"github.com/spf13/cobra"
)

var commitYes bool

var commitCmd = &cobra.Command{
	Use:   "commit",
	Short: "Commit the current agent changes (never pushes)",
	Long: `Record whatever is uncommitted in the working tree as one commit,
with the completed tasks listed in the message.

Useful after declining the gate at the end of ` + "`aros work`" + `, or to record
changes from a run that was interrupted. Aros never pushes — run git push
yourself when you are ready.`,
	Args: cobra.NoArgs,
	RunE: runCommit,
}

func init() {
	commitCmd.Flags().BoolVarP(&commitYes, "yes", "y", false, "commit without asking")
	rootCmd.AddCommand(commitCmd)
}

func runCommit(_ *cobra.Command, _ []string) error {
	arosDir, err := state.ArosDir()
	if err != nil {
		return err
	}
	cwd := filepath.Dir(arosDir)

	repo, ok := gitrepo.Open(cwd)
	if !ok {
		return fmt.Errorf("%s is not a git repository", cwd)
	}

	projectName := "aros"
	var tasks []state.Task
	if s, err := state.LoadState(arosDir); err == nil {
		projectName = s.ProjectName
		if m, err := state.LoadManifest(arosDir); err == nil {
			tasks = m.Tasks
		}
	}

	cfg, err := config.Load(cfgFile)
	if err != nil {
		return err
	}
	mode := "ask"
	if commitYes || cfg.CommitMode() == "auto" {
		mode = "auto"
	}

	if err := commitRun(repo, mode, projectName, tasks); err != nil {
		if errors.Is(err, gitrepo.ErrNothingToCommit) {
			fmt.Println("Nothing to commit.")
			return nil
		}
		return err
	}
	return nil
}
