package planner

import (
	"context"
	"fmt"
	"strings"

	"github.com/Rinil-Parmar/aros/agent"
	"github.com/Rinil-Parmar/aros/human"
	"github.com/Rinil-Parmar/aros/memory"
	"golang.org/x/sync/errgroup"
)

const maxApprovalLoops = 3

// Run executes the planning phase:
// 1. Each enabled agent generates a plan in parallel.
// 2. The judge synthesizes all plans.
// 3. Human approves or requests changes (up to maxApprovalLoops).
// Returns the approved plan text.
func Run(ctx context.Context, task string, reg agent.Registry, judgeName string, mem *memory.SecondMem) (string, error) {
	agents := reg.Enabled(judgeName)
	if len(agents) == 0 {
		return "", fmt.Errorf("no agents available for planning")
	}

	judge, err := reg.Judge(judgeName)
	if err != nil {
		return "", err
	}

	// Fetch relevant secondmem context (non-blocking — empty string if unavailable)
	memCtx := mem.Ask(ctx, task)

	fmt.Printf("\n=== PLAN PHASE ===\nTask: %s\n\n", task)
	fmt.Printf("Querying %d agent(s) for plans...\n", len(agents))

	// Collect plans in parallel
	results := make([]agent.AgentResult, len(agents))
	g, gctx := errgroup.WithContext(ctx)
	for i, a := range agents {
		i, a := i, a
		g.Go(func() error {
			fmt.Printf("  [%s] generating plan...\n", a.Name())
			prompt := buildPlanPrompt(task, memCtx)
			r, err := a.Run(gctx, prompt)
			if err != nil {
				fmt.Printf("  [%s] warning: %v\n", a.Name(), err)
				r = agent.AgentResult{AgentName: a.Name(), Output: fmt.Sprintf("(error: %v)", err)}
			}
			results[i] = r
			fmt.Printf("  [%s] done\n", a.Name())
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return "", err
	}

	// Synthesis + approval loop
	feedback := ""
	for attempt := 1; attempt <= maxApprovalLoops; attempt++ {
		fmt.Printf("\n[Judge: %s] synthesizing plans (attempt %d/%d)...\n", judge.Name(), attempt, maxApprovalLoops)

		judgePrompt := buildJudgePrompt(task, results, feedback)
		judgeResult, err := judge.Run(ctx, judgePrompt)
		if err != nil {
			return "", fmt.Errorf("judge failed: %w", err)
		}

		fmt.Printf("\n--- Synthesized Plan ---\n")
		human.Paginate(judgeResult.Output)

		ok, err := human.Confirm("Approve this plan and proceed to task division?")
		if err != nil {
			return "", err
		}
		if ok {
			return judgeResult.Output, nil
		}

		if attempt == maxApprovalLoops {
			break
		}
		feedback, err = human.AskInput("What should change? (Your feedback will be sent to the judge)")
		if err != nil {
			return "", err
		}
	}

	return "", fmt.Errorf("plan not approved after %d attempts — re-run `aros plan` to try again", maxApprovalLoops)
}

func buildPlanPrompt(task, memCtx string) string {
	var sb strings.Builder
	sb.WriteString("You are a software architect. Plan the implementation of the following task.\n\n")
	sb.WriteString("TASK: ")
	sb.WriteString(task)
	sb.WriteString("\n\n")
	if memCtx != "" {
		sb.WriteString("RELEVANT CONTEXT FROM MEMORY:\n")
		sb.WriteString(memCtx)
		sb.WriteString("\n\n")
	}
	sb.WriteString("Provide a concrete, specific plan covering:\n")
	sb.WriteString("- Key modules or components needed\n")
	sb.WriteString("- Interfaces and data structures\n")
	sb.WriteString("- Sequencing of work (what depends on what)\n")
	sb.WriteString("- Potential risks or unknowns\n")
	sb.WriteString("Be precise. Avoid generic advice.\n")
	return sb.String()
}

func buildJudgePrompt(task string, results []agent.AgentResult, feedback string) string {
	var sb strings.Builder
	sb.WriteString("You are evaluating multiple AI-generated plans for the following task and synthesizing the best approach.\n\n")
	sb.WriteString("TASK: ")
	sb.WriteString(task)
	sb.WriteString("\n\n")

	for _, r := range results {
		if r.Output == "" {
			continue
		}
		sb.WriteString(fmt.Sprintf("=== Plan from %s ===\n", r.AgentName))
		sb.WriteString(r.Output)
		sb.WriteString("\n\n")
	}

	if feedback != "" {
		sb.WriteString("=== Human Feedback on Previous Synthesis ===\n")
		sb.WriteString(feedback)
		sb.WriteString("\n\n")
	}

	sb.WriteString("Synthesize the BEST implementation plan. Take the strongest elements from each plan. ")
	sb.WriteString("Output a clear, actionable plan that a developer can follow directly. ")
	sb.WriteString("Include: architecture decisions, module breakdown, sequencing, and key risks.")
	return sb.String()
}
