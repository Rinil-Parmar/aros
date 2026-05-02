package agent

import (
	"fmt"

	"github.com/Rinil-Parmar/aros/config"
)

// Registry maps agent name → Agent implementation.
type Registry map[string]Agent

// BuildRegistry creates agents for all enabled entries in cfg.Agents.
// workDir is the project directory passed to each adapter.
func BuildRegistry(cfg *config.Config, workDir string) (Registry, error) {
	reg := make(Registry)

	for name, ac := range cfg.Agents {
		if !ac.Enabled {
			continue
		}
		a, err := buildAgent(name, ac, workDir)
		if err == ErrAgentNotAvailable {
			fmt.Printf("warning: agent %q not available (binary not found), skipping\n", name)
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("initializing agent %q: %w", name, err)
		}
		reg[name] = a
	}

	if len(reg) == 0 {
		return nil, fmt.Errorf("no agents available — install claude or opencode and ensure they are on PATH")
	}
	return reg, nil
}

func buildAgent(name string, ac config.AgentConfig, workDir string) (Agent, error) {
	switch name {
	case "claude":
		return NewClaudeAdapter(ac.Model, ac.DangerouslySkipPerms, workDir)
	case "opencode":
		return NewOpenCodeAdapter(ac.Model, workDir)
	case "copilot":
		return NewCopilotAdapter(ac.Model, workDir)
	default:
		// Unknown agent names are treated as opencode variants (e.g., opencode-gemini)
		return NewOpenCodeAdapter(ac.Model, workDir)
	}
}

// Judge returns the configured judge agent from the registry.
func (r Registry) Judge(judgeName string) (Agent, error) {
	if a, ok := r[judgeName]; ok {
		return a, nil
	}
	// Fallback: return first available agent
	for _, a := range r {
		return a, nil
	}
	return nil, fmt.Errorf("judge agent %q not found in registry", judgeName)
}

// Enabled returns all agents in the registry except the judge (for parallel plan calls).
func (r Registry) Enabled() []Agent {
	agents := make([]Agent, 0, len(r))
	for _, a := range r {
		agents = append(agents, a)
	}
	return agents
}
