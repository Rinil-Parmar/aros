package cmd

import (
	"fmt"
	"strings"

	"github.com/Rinil-Parmar/aros/state"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show current project phase and task status",
	Args:  cobra.NoArgs,
	RunE:  runStatus,
}

func init() {
	rootCmd.AddCommand(statusCmd)
}

func runStatus(_ *cobra.Command, _ []string) error {
	arosDir, err := state.ArosDir()
	if err != nil {
		return err
	}

	s, err := state.LoadState(arosDir)
	if err != nil {
		return fmt.Errorf("no Aros project found in this directory: %w", err)
	}

	fmt.Printf("Project:  %s\n", s.ProjectName)
	fmt.Printf("Phase:    %s\n", s.Phase)
	if s.Task != "" {
		fmt.Printf("Task:     %s\n", s.Task)
	}
	fmt.Printf("Updated:  %s\n", s.UpdatedAt.Format("2006-01-02 15:04:05"))

	// Show task manifest if available
	manifest, err := state.LoadManifest(arosDir)
	if err != nil {
		return nil // manifest not yet created — that's fine
	}

	if len(manifest.Tasks) == 0 {
		return nil
	}

	fmt.Printf("\n%-12s %-30s %-15s %s\n", "ID", "Title", "Agent", "Status")
	fmt.Println(strings.Repeat("-", 75))
	for _, t := range manifest.Tasks {
		title := t.Title
		if len(title) > 29 {
			title = title[:26] + "..."
		}
		status := string(t.Status)
		if t.Status == state.TaskBlocked {
			status += " (" + t.BlockReason + ")"
		}
		fmt.Printf("%-12s %-30s %-15s %s\n", t.ID, title, t.AssignedTo, status)
	}
	return nil
}
