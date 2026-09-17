package agent

import (
	"errors"
	"fmt"
	"sort"

	"github.com/Rinil-Parmar/aros/config"
)

// Registry maps agent name → Agent implementation.
type Registry map[string]Agent

// BuildRegistry creates agents for all enabled entries in cfg.Agents.
// workDir is the project directory passed to each adapter.
// Agents whose CLI binary is missing are skipped and reported in warnings
// (never printed — the TUI owns the screen).
func BuildRegistry(cfg *config.Config, workDir string) (reg Registry, warnings []string, err error) {
	reg = make(Registry)

	names := make([]string, 0, len(cfg.Agents))
	for name := range cfg.Agents {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		ac := cfg.Agents[name]
		if !ac.Enabled {
			continue
		}
		a, err := buildAgent(name, ac, workDir)
		if errors.Is(err, ErrAgentNotAvailable) {
			warnings = append(warnings, fmt.Sprintf("agent %q not available (binary not found), skipping", name))
			continue
		}
		if err != nil {
			return nil, warnings, fmt.Errorf("initializing agent %q: %w", name, err)
		}
		reg[name] = a
	}

	if len(reg) == 0 {
		return nil, warnings, fmt.Errorf("no agents available — install claude, opencode or copilot and ensure they are on PATH")
	}
	if _, ok := reg[cfg.Judge.Agent]; !ok {
		warnings = append(warnings, fmt.Sprintf("judge agent %q is not available — set judge.agent to one of: %v", cfg.Judge.Agent, reg.Names()))
	}
	return reg, warnings, nil
}

func buildAgent(name string, ac config.AgentConfig, workDir string) (Agent, error) {
	switch name {
	case "claude":
		return NewClaudeAdapter(ac.Model, ac.DangerouslySkipPerms, workDir)
	case "copilot":
		return NewCopilotAdapter(ac.Model, workDir)
	default:
		// "opencode" and any other name (e.g. opencode-gemini) run through the opencode CLI.
		return NewOpenCodeAdapter(name, ac.Model, ac.APIKey, workDir)
	}
}

// Judge returns the configured judge agent from the registry.
func (r Registry) Judge(judgeName string) (Agent, error) {
	if a, ok := r[judgeName]; ok {
		return a, nil
	}
	return nil, fmt.Errorf("judge agent %q not available — check judge.agent in config (available: %v)", judgeName, r.Names())
}

// Names returns the registry keys in sorted order.
func (r Registry) Names() []string {
	names := make([]string, 0, len(r))
	for name := range r {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Enabled returns all agents except the named judge (for parallel plan calls),
// in deterministic (sorted) order.
func (r Registry) Enabled(judgeName ...string) []Agent {
	exclude := ""
	if len(judgeName) > 0 {
		exclude = judgeName[0]
	}
	agents := make([]Agent, 0, len(r))
	for _, name := range r.Names() {
		if name != exclude {
			agents = append(agents, r[name])
		}
	}
	return agents
}
