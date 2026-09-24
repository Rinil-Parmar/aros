package divider

import (
	"context"
	"fmt"
	"strings"

	"github.com/Rinil-Parmar/aros/agent"
	"github.com/Rinil-Parmar/aros/config"
	"github.com/Rinil-Parmar/aros/human"
	"github.com/Rinil-Parmar/aros/memory"
	"github.com/Rinil-Parmar/aros/state"
)

const maxApprovalLoops = 3
const maxParseRetries = 2

// Run executes the divide phase:
// 1. Judge breaks the approved plan into tasks with agent assignments.
// 2. Validates IDs, dependencies, cycles and agent names.
// 3. Human approves or requests changes.
// Returns the approved task manifest.
func Run(ctx context.Context, s *state.ProjectState, reg agent.Registry, cfg *config.Config, mem *memory.SecondMem) (*state.TaskManifest, error) {
	judge, err := reg.Judge(cfg.Judge.Agent)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(s.ApprovedPlan) == "" {
		return nil, fmt.Errorf("no approved plan in state — run `aros plan` first")
	}

	fmt.Printf("\n=== DIVIDE PHASE ===\nProject: %s\n\n", s.ProjectName)

	feedback := ""
	for attempt := 1; attempt <= maxApprovalLoops; attempt++ {
		fmt.Printf("[Judge: %s] dividing into tasks (attempt %d/%d)...\n", judge.Name(), attempt, maxApprovalLoops)

		dividePrompt := BuildDividePrompt(s, cfg, reg.Names(), feedback)
		result, err := agent.Reason(ctx, judge, dividePrompt)
		if err != nil {
			return nil, fmt.Errorf("judge failed: %w", err)
		}

		tasks, parseErr := parseTasksWithRetry(ctx, judge, result.Output, dividePrompt, maxParseRetries)
		if parseErr != nil {
			fmt.Printf("warning: could not parse task JSON: %v\n", parseErr)
			if attempt == maxApprovalLoops {
				return nil, fmt.Errorf("failed to get valid task JSON after %d attempts", maxApprovalLoops)
			}
			feedback = "Previous response had invalid JSON. Return ONLY a JSON array, no markdown, no prose."
			continue
		}

		state.AssignIDs(tasks)
		if err := state.ValidateTasks(tasks); err != nil {
			fmt.Printf("warning: invalid task graph: %v\n", err)
			if attempt == maxApprovalLoops {
				return nil, fmt.Errorf("judge produced an invalid task graph after %d attempts: %w", maxApprovalLoops, err)
			}
			feedback = fmt.Sprintf("The previous task list was invalid: %v. Fix it.", err)
			continue
		}
		if reassigned := state.NormalizeAssignments(tasks, reg.Names(), cfg.Judge.Agent); len(reassigned) > 0 {
			fmt.Printf("note: %d task(s) referenced an unavailable agent and were reassigned to %s: %s\n",
				len(reassigned), cfg.Judge.Agent, strings.Join(reassigned, ", "))
		}

		printTaskTable(tasks)

		ok, err := human.Confirm("Approve task assignments and start work?")
		if err != nil {
			return nil, err
		}
		if ok {
			manifest := &state.TaskManifest{Tasks: tasks}
			_ = mem.Ingest(ctx, fmt.Sprintf("Task manifest for %s:\n%s", s.ProjectName, formatManifestSummary(tasks)))
			return manifest, nil
		}

		if attempt == maxApprovalLoops {
			break
		}
		feedback, err = human.AskInput("What should change in the task assignments?")
		if err != nil {
			return nil, err
		}
	}

	return nil, fmt.Errorf("task assignments not approved after %d attempts", maxApprovalLoops)
}

// BuildDividePrompt asks the judge for a JSON task array. Only agents that are
// actually available (in the registry) are offered for assignment.
func BuildDividePrompt(s *state.ProjectState, cfg *config.Config, available []string, feedback string) string {
	var sb strings.Builder
	sb.WriteString("Break the following approved plan into concrete, assignable tasks.\n\n")
	sb.WriteString("PROJECT: ")
	sb.WriteString(s.ProjectName)
	sb.WriteString("\n\nAPPROVED PLAN:\n")
	sb.WriteString(s.ApprovedPlan)
	sb.WriteString("\n\nAVAILABLE AGENTS AND THEIR STRENGTHS:\n")

	for _, name := range available {
		ac := cfg.Agents[name]
		sb.WriteString(fmt.Sprintf("- %s: %s\n", name, strings.Join(ac.Strengths, ", ")))
	}

	sb.WriteString("\nReturn ONLY a JSON array (no markdown fences, no prose, no explanation):\n")
	sb.WriteString(`[
  {
    "id": "task-001",
    "title": "short title",
    "description": "detailed description of what to implement",
    "assigned_to": "agent-name",
    "dependencies": []
  }
]`)
	sb.WriteString("\n\nRules:\n")
	sb.WriteString("- assigned_to must be exactly one of the agent names listed above\n")
	sb.WriteString("- ids must be unique; dependencies must reference only existing task IDs in the same array\n")
	sb.WriteString("- No circular dependencies\n")
	sb.WriteString("- Tasks should be concrete and completable by a single agent\n")

	if feedback != "" {
		sb.WriteString("\n\nHUMAN FEEDBACK:\n")
		sb.WriteString(feedback)
	}
	return sb.String()
}

// parseTasksWithRetry attempts to extract JSON from the agent output, retrying on parse failure.
func parseTasksWithRetry(ctx context.Context, judge agent.Agent, raw, originalPrompt string, maxRetries int) ([]state.Task, error) {
	tasks, err := state.ParseTasks(raw)
	if err == nil {
		return tasks, nil
	}
	for i := 0; i < maxRetries; i++ {
		fmt.Printf("  parse failed (%v), asking judge again (%d/%d)...\n", err, i+1, maxRetries)
		retryPrompt := originalPrompt + "\n\nPrevious response could not be parsed as JSON. Return ONLY the JSON array, nothing else."
		result, rerr := agent.Reason(ctx, judge, retryPrompt)
		if rerr != nil {
			err = rerr
			continue
		}
		tasks, err = state.ParseTasks(result.Output)
		if err == nil {
			return tasks, nil
		}
	}
	return nil, err
}

func printTaskTable(tasks []state.Task) {
	fmt.Printf("\n%-12s %-30s %-15s %s\n", "ID", "Title", "Assigned To", "Dependencies")
	fmt.Println(strings.Repeat("-", 80))
	for _, t := range tasks {
		deps := strings.Join(t.Dependencies, ", ")
		if deps == "" {
			deps = "(none)"
		}
		fmt.Printf("%-12s %-30s %-15s %s\n", t.ID, state.Truncate(t.Title, 29), t.AssignedTo, deps)
	}
	fmt.Println()
}

func formatManifestSummary(tasks []state.Task) string {
	var sb strings.Builder
	for _, t := range tasks {
		sb.WriteString(fmt.Sprintf("- [%s] %s → %s\n", t.ID, t.Title, t.AssignedTo))
	}
	return sb.String()
}
