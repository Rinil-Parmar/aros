package divider

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Rinil-Parmar/aros/agent"
	"github.com/Rinil-Parmar/aros/config"
	"github.com/Rinil-Parmar/aros/human"
	"github.com/Rinil-Parmar/aros/memory"
	"github.com/Rinil-Parmar/aros/state"
	"github.com/google/uuid"
)

const maxApprovalLoops = 3
const maxParseRetries = 2

// Run executes the divide phase:
// 1. Judge breaks the approved plan into tasks with agent assignments.
// 2. Validates for cycles.
// 3. Human approves or requests changes.
// Returns the approved task manifest.
func Run(ctx context.Context, s *state.ProjectState, reg agent.Registry, cfg *config.Config, mem *memory.SecondMem) (*state.TaskManifest, error) {
	judge, err := reg.Judge(cfg.Judge.Agent)
	if err != nil {
		return nil, err
	}

	fmt.Printf("\n=== DIVIDE PHASE ===\nProject: %s\n\n", s.ProjectName)

	feedback := ""
	for attempt := 1; attempt <= maxApprovalLoops; attempt++ {
		fmt.Printf("[Judge: %s] dividing into tasks (attempt %d/%d)...\n", judge.Name(), attempt, maxApprovalLoops)

		dividePrompt := buildDividePrompt(s, cfg, feedback)
		result, err := judge.Run(ctx, dividePrompt)
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

		if err := detectCycles(tasks); err != nil {
			fmt.Printf("warning: dependency cycle detected: %v\n", err)
			if attempt == maxApprovalLoops {
				return nil, fmt.Errorf("judge produced cyclic dependencies after %d attempts: %w", maxApprovalLoops, err)
			}
			feedback = fmt.Sprintf("The previous task list had a dependency cycle: %v. Fix the dependencies.", err)
			continue
		}

		// Assign stable IDs if missing
		assignIDs(tasks)

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

func buildDividePrompt(s *state.ProjectState, cfg *config.Config, feedback string) string {
	var sb strings.Builder
	sb.WriteString("Break the following approved plan into concrete, assignable tasks.\n\n")
	sb.WriteString("PROJECT: ")
	sb.WriteString(s.ProjectName)
	sb.WriteString("\n\nAPPROVED PLAN:\n")
	sb.WriteString(s.ApprovedPlan)
	sb.WriteString("\n\nAVAILABLE AGENTS AND THEIR STRENGTHS:\n")

	for name, ac := range cfg.Agents {
		if !ac.Enabled {
			continue
		}
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
	sb.WriteString("- Assign each task to the most appropriate agent based on their strengths\n")
	sb.WriteString("- dependencies must reference only existing task IDs in the same array\n")
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
	tasks, err := parseTasks(raw)
	if err == nil {
		return tasks, nil
	}
	for i := 0; i < maxRetries; i++ {
		retryPrompt := originalPrompt + "\n\nPrevious response could not be parsed as JSON. Return ONLY the JSON array, nothing else."
		result, rerr := judge.Run(ctx, retryPrompt)
		if rerr != nil {
			continue
		}
		tasks, err = parseTasks(result.Output)
		if err == nil {
			return tasks, nil
		}
	}
	return nil, err
}

func parseTasks(raw string) ([]state.Task, error) {
	// Strip markdown code fences if present
	raw = strings.TrimSpace(raw)
	if idx := strings.Index(raw, "```"); idx != -1 {
		raw = raw[idx:]
		raw = strings.TrimPrefix(raw, "```json")
		raw = strings.TrimPrefix(raw, "```")
		if end := strings.Index(raw, "```"); end != -1 {
			raw = raw[:end]
		}
		raw = strings.TrimSpace(raw)
	}

	// Find JSON array bounds
	start := strings.Index(raw, "[")
	end := strings.LastIndex(raw, "]")
	if start == -1 || end == -1 || end <= start {
		return nil, fmt.Errorf("no JSON array found in output")
	}
	raw = raw[start : end+1]

	var tasks []state.Task
	if err := json.Unmarshal([]byte(raw), &tasks); err != nil {
		return nil, fmt.Errorf("JSON parse error: %w", err)
	}
	if len(tasks) == 0 {
		return nil, fmt.Errorf("empty task list")
	}
	return tasks, nil
}

// detectCycles uses DFS to find cycles in the dependency graph.
func detectCycles(tasks []state.Task) error {
	idSet := make(map[string]bool)
	for _, t := range tasks {
		idSet[t.ID] = true
	}

	// Validate all dependency IDs exist
	for _, t := range tasks {
		for _, dep := range t.Dependencies {
			if !idSet[dep] {
				return fmt.Errorf("task %q depends on unknown task %q", t.ID, dep)
			}
		}
	}

	// Build adjacency list and run DFS
	adj := make(map[string][]string)
	for _, t := range tasks {
		adj[t.ID] = t.Dependencies
	}

	visited := make(map[string]int) // 0=unvisited, 1=in-stack, 2=done
	var dfs func(id string) error
	dfs = func(id string) error {
		if visited[id] == 1 {
			return fmt.Errorf("cycle detected at task %q", id)
		}
		if visited[id] == 2 {
			return nil
		}
		visited[id] = 1
		for _, dep := range adj[id] {
			if err := dfs(dep); err != nil {
				return err
			}
		}
		visited[id] = 2
		return nil
	}

	for _, t := range tasks {
		if err := dfs(t.ID); err != nil {
			return err
		}
	}
	return nil
}

func assignIDs(tasks []state.Task) {
	for i := range tasks {
		if tasks[i].ID == "" {
			tasks[i].ID = fmt.Sprintf("task-%s", uuid.New().String()[:8])
		}
		if tasks[i].Status == "" {
			tasks[i].Status = state.TaskPending
		}
	}
}

func printTaskTable(tasks []state.Task) {
	fmt.Printf("\n%-12s %-30s %-15s %s\n", "ID", "Title", "Assigned To", "Dependencies")
	fmt.Println(strings.Repeat("-", 80))
	for _, t := range tasks {
		deps := strings.Join(t.Dependencies, ", ")
		if deps == "" {
			deps = "(none)"
		}
		title := t.Title
		if len(title) > 29 {
			title = title[:26] + "..."
		}
		fmt.Printf("%-12s %-30s %-15s %s\n", t.ID, title, t.AssignedTo, deps)
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
