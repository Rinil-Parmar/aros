package tui

import (
	"fmt"
	"strings"

	"github.com/Rinil-Parmar/aros/config"
	"github.com/Rinil-Parmar/aros/state"
)

// denseInstructions returns the dense-mode preamble for the given intensity level.
func denseInstructions(level string) string {
	switch level {
	case "lite":
		return "**Dense output: drop filler & hedging. Keep articles. Full sentences.**\n\n"
	case "full":
		return "**Dense output: drop articles (the/a), allow fragments, short synonyms. No filler (basically/actually/simply).**\n\n"
	case "ultra":
		return "**Dense output: abbreviate (DB/auth/cfg), drop conjunctions, use → for causality. No prose filler.**\n\n"
	default:
		return "" // empty = normal mode
	}
}

// withDense prepends dense instructions to a prompt if the agent config specifies it.
func withDense(prompt string, ac config.AgentConfig) string {
	if ac.Dense == "" {
		return prompt
	}
	return denseInstructions(ac.Dense) + prompt
}

func buildPlanPrompt(task, memCtx string) string {
	var sb strings.Builder
	sb.WriteString("You are a software architect. Plan the implementation of:\n\nTASK: ")
	sb.WriteString(task)
	if memCtx != "" {
		sb.WriteString("\n\nCONTEXT FROM MEMORY:\n")
		sb.WriteString(memCtx)
	}
	sb.WriteString("\n\nProvide a concrete plan covering modules, interfaces, sequencing, and risks.")
	return sb.String()
}

type agentPlan struct {
	name   string
	output string
}

func buildJudgePlanPrompt(task string, plans []agentPlan, feedback string) string {
	var sb strings.Builder
	sb.WriteString("Synthesize the best implementation plan from these agent plans.\n\nTASK: ")
	sb.WriteString(task)
	sb.WriteString("\n\n")
	for _, p := range plans {
		if p.output == "" {
			continue
		}
		sb.WriteString(fmt.Sprintf("=== Plan from %s ===\n%s\n\n", p.name, p.output))
	}
	if feedback != "" {
		sb.WriteString("=== Human Feedback ===\n")
		sb.WriteString(feedback)
		sb.WriteString("\n\n")
	}
	sb.WriteString("Output a single clear, actionable plan with architecture decisions, module breakdown, and sequencing.")
	return sb.String()
}

func buildDividePrompt(s *state.ProjectState, cfg *config.Config) string {
	var sb strings.Builder
	sb.WriteString("Break this plan into concrete tasks. Return ONLY a JSON array, no prose, no markdown fences.\n\n")
	sb.WriteString("PROJECT: ")
	sb.WriteString(s.ProjectName)
	sb.WriteString("\n\nPLAN:\n")
	sb.WriteString(s.ApprovedPlan)
	sb.WriteString("\n\nAVAILABLE AGENTS:\n")
	for name, ac := range cfg.Agents {
		if ac.Enabled {
			sb.WriteString(fmt.Sprintf("- %s: %s\n", name, strings.Join(ac.Strengths, ", ")))
		}
	}
	sb.WriteString("\nFormat:\n")
	sb.WriteString(`[{"id":"task-001","title":"short title","description":"detailed description","assigned_to":"agent-name","dependencies":[]}]`)
	sb.WriteString("\n\nRules: no cycles, dependencies must reference existing IDs only, tasks must be concrete and completable.")
	return sb.String()
}

func buildWorkPrompt(task *state.Task, basePrompt, depOutputs, memCtx string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Task [%s]: %s\n\n", task.ID, task.Title))
	sb.WriteString("DESCRIPTION:\n")
	sb.WriteString(basePrompt)
	if depOutputs != "" {
		sb.WriteString("\n\nPREREQUISITE TASK OUTPUTS:\n")
		sb.WriteString(depOutputs)
	}
	if memCtx != "" {
		sb.WriteString("\n\nMEMORY CONTEXT:\n")
		sb.WriteString(memCtx)
	}
	sb.WriteString("\n\nComplete this task fully. If you need a human decision, output '<<AROS_HUMAN>>' on its own line followed by your question, then stop.")
	return sb.String()
}
