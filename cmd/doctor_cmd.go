package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Rinil-Parmar/aros/agent"
	"github.com/Rinil-Parmar/aros/config"
	gitrepo "github.com/Rinil-Parmar/aros/git"
	"github.com/Rinil-Parmar/aros/state"
	"github.com/spf13/cobra"
)

var doctorProbe bool

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check that the agents, models, auth and config are actually usable",
	Long: `Check everything a run depends on, before a run depends on it.

Without --probe nothing is spent: it checks the binaries, the configured models
against each agent's own list, and the surrounding setup. --probe additionally
makes one minimal real call per agent, which is the only way to prove the
credentials work. That costs a little — a fraction of a cent for claude, one
premium request for copilot — so it is opt-in.`,
	Args: cobra.NoArgs,
	RunE: runDoctor,
}

func init() {
	doctorCmd.Flags().BoolVar(&doctorProbe, "probe", false,
		"also make one real call per agent to verify credentials (costs a little)")
	rootCmd.AddCommand(doctorCmd)
}

const (
	ok   = "ok"
	bad  = "FAIL"
	warn = "warn"
)

func runDoctor(_ *cobra.Command, _ []string) error {
	cfg, err := config.Load(cfgFile)
	if err != nil {
		fmt.Printf("%-9s config — %v\n", bad, err)
		return nil
	}
	cwd, _ := os.Getwd()

	fmt.Println("agents")
	reg, warnings, regErr := agent.BuildRegistry(cfg, cwd)
	for _, w := range warnings {
		fmt.Printf("  %-6s %s\n", warn, w)
	}
	if regErr != nil {
		fmt.Printf("  %-6s %v\n", bad, regErr)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	problems := 0
	for _, name := range reg.Names() {
		a := reg[name]
		model := cfg.Agents[name].Model
		fmt.Printf("\n  %s\n", name)

		if v := agent.BinaryVersion(ctx, binaryFor(name)); v != "" {
			fmt.Printf("    %-6s binary    %s\n", ok, v)
		} else {
			fmt.Printf("    %-6s binary    on PATH, version unknown\n", warn)
		}

		mOK, why := agent.CheckModel(ctx, a, model)
		status := ok
		if !mOK {
			status, problems = bad, problems+1
		}
		fmt.Printf("    %-6s model     %s — %s\n", status, model, why)

		if doctorProbe {
			pOK, detail := agent.Probe(ctx, a)
			status = ok
			if !pOK {
				status, problems = bad, problems+1
			}
			fmt.Printf("    %-6s call      %s\n", status, detail)
		}
	}
	if !doctorProbe {
		fmt.Println("\n  (add --probe to verify credentials with one real call per agent)")
	}

	// Judge must be an agent that is actually available.
	fmt.Println("\njudge")
	if _, err := reg.Judge(cfg.Judge.Agent); err != nil {
		fmt.Printf("  %-6s %v\n", bad, err)
		problems++
	} else {
		fmt.Printf("  %-6s %s\n", ok, cfg.Judge.Agent)
	}

	fmt.Println("\nproject")
	if arosDir, err := state.ArosDir(); err == nil {
		if s, err := state.LoadState(arosDir); err == nil {
			fmt.Printf("  %-6s session   %s · phase %s\n", ok, s.ProjectName, s.Phase)
		} else {
			fmt.Printf("  %-6s session   none here — run `aros init <name>`\n", warn)
		}
	}
	if _, isRepo := gitrepo.Open(cwd); isRepo {
		mode := cfg.CommitMode()
		fmt.Printf("  %-6s git       repository found, commit mode %q\n", ok, mode)
	} else {
		fmt.Printf("  %-6s git       not a repository — agent changes will not be committed\n", warn)
	}
	if cfg.Work.MaxConcurrent > 1 {
		fmt.Printf("  %-6s work      max_concurrent = %d; parallel agents share one working tree (see docs/parallel-execution.md)\n",
			warn, cfg.Work.MaxConcurrent)
	} else {
		fmt.Printf("  %-6s work      max_concurrent = 1 (serialised, safe)\n", ok)
	}

	fmt.Println("\nmemory")
	if cfg.SecondMem.Enabled {
		if _, err := exec.LookPath(cfg.SecondMem.Binary); err == nil {
			fmt.Printf("  %-6s secondmem %s on PATH\n", ok, cfg.SecondMem.Binary)
		} else {
			fmt.Printf("  %-6s secondmem enabled but %q is not on PATH — treated as disabled\n", warn, cfg.SecondMem.Binary)
		}
	} else {
		fmt.Printf("  %-6s secondmem disabled\n", ok)
	}

	fmt.Println("\nconfig")
	for _, p := range []string{config.GlobalConfigPath(), config.ProjectPath(cwd)} {
		if _, err := os.Stat(p); err == nil {
			fmt.Printf("  %-6s %s\n", ok, shortenPath(p))
		} else {
			fmt.Printf("  %-6s %s (absent — defaults apply)\n", warn, shortenPath(p))
		}
	}

	fmt.Println()
	if problems == 0 {
		fmt.Println("No problems found.")
		return nil
	}
	fmt.Printf("%d problem(s) found. Fix those before running a phase.\n", problems)
	return nil
}

// binaryFor maps an agent name to the CLI it runs. Unknown names go through
// opencode, matching the registry.
func binaryFor(agentName string) string {
	switch agentName {
	case "claude", "copilot":
		return agentName
	default:
		return "opencode"
	}
}

func shortenPath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home+string(filepath.Separator)) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}
