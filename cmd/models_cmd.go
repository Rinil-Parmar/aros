package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Rinil-Parmar/aros/agent"
	"github.com/Rinil-Parmar/aros/config"
	"github.com/spf13/cobra"
)

var modelsRefresh bool

var modelsCmd = &cobra.Command{
	Use:   "models [agent] [filter]",
	Short: "List the models an agent actually supports",
	Long: `Ask each agent what models it supports, rather than guessing.

Where the CLI can enumerate them (opencode) the list is live and cached for a
day. Where it cannot (claude, copilot) Aros shows the documented names and says
so, instead of presenting a guess as fact.

  aros models                    every available agent
  aros models opencode           all of opencode's models
  aros models opencode gpt-5     only the matching ones
  aros models opencode --refresh re-query, ignoring the cache`,
	Args: cobra.MaximumNArgs(2),
	RunE: runModels,
}

func init() {
	modelsCmd.Flags().BoolVar(&modelsRefresh, "refresh", false, "ignore the cache and re-query")
	rootCmd.AddCommand(modelsCmd)
}

func runModels(_ *cobra.Command, args []string) error {
	cfg, err := config.Load(cfgFile)
	if err != nil {
		return err
	}
	cwd, _ := os.Getwd()
	reg, warnings, err := agent.BuildRegistry(cfg, cwd)
	for _, w := range warnings {
		fmt.Println("warning:", w)
	}
	if err != nil {
		return err
	}

	want, filter := "", ""
	if len(args) > 0 {
		want = strings.ToLower(args[0])
	}
	if len(args) > 1 {
		filter = args[1]
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	names := reg.Names()
	if want != "" {
		if _, ok := reg[want]; !ok {
			return fmt.Errorf("agent %q is not available — have: %s", want, strings.Join(names, ", "))
		}
		names = []string{want}
	}

	for i, name := range names {
		if i > 0 {
			fmt.Println()
		}
		list := agent.Models(ctx, reg[name], modelsRefresh)
		matches := list.Filter(filter)

		fmt.Printf("%s — %s", name, list.Describe())
		if filter != "" {
			fmt.Printf("  (%d match %q)", len(matches), filter)
		}
		fmt.Println()
		if list.Note != "" {
			fmt.Printf("  note: %s\n", list.Note)
		}

		current := cfg.Agents[name].Model
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		for _, mdl := range matches {
			mark := " "
			if mdl == current {
				mark = "→"
			}
			fmt.Fprintf(w, "  %s\t%s\n", mark, mdl)
		}
		w.Flush()
		if len(matches) == 0 && filter != "" {
			fmt.Printf("  nothing matches %q\n", filter)
		}
	}
	return nil
}

// verifyModelChoice checks a model before it is written to the config, where
// the agent can do so without spending tokens. Returns a warning string when
// verification is not possible.
func verifyModelChoice(agentName, model string) (warning string, err error) {
	cfg, cerr := config.Load(cfgFile)
	if cerr != nil {
		return "", nil // not our job to fail a config write over this
	}
	cwd, _ := os.Getwd()
	reg, _, rerr := agent.BuildRegistry(cfg, cwd)
	if rerr != nil {
		return "", nil
	}
	a, ok := reg[agentName]
	if !ok {
		return fmt.Sprintf("agent %q is not available here, so the model was not checked", agentName), nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// Prefer a real list; it is free to check against.
	if list := agent.Models(ctx, a, false); list.Source != agent.SourceKnown {
		if list.Has(model) {
			return "", nil
		}
		near := list.Filter(model)
		msg := fmt.Sprintf("%s does not list model %q", agentName, model)
		if len(near) > 0 {
			shown := near
			if len(shown) > 5 {
				shown = shown[:5]
			}
			msg += "\ndid you mean: " + strings.Join(shown, ", ")
		}
		return "", fmt.Errorf("%s", msg)
	}
	if v, canVerify := a.(agent.ModelVerifier); canVerify {
		if verr := v.VerifyModel(ctx, model); verr != nil {
			return "", verr
		}
		return "", nil
	}
	return fmt.Sprintf("%s cannot verify model names; if it is wrong the next call will fail", agentName), nil
}
