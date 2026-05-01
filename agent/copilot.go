package agent

import (
	"context"
	"os/exec"
)

// CopilotAdapter is a stub for GitHub Copilot headless mode.
// Enable once `gh copilot` confirms non-interactive support.
type CopilotAdapter struct{}

func NewCopilotAdapter() (*CopilotAdapter, error) {
	if _, err := exec.LookPath("gh"); err != nil {
		return nil, ErrAgentNotAvailable
	}
	// gh copilot headless mode is not confirmed; return unavailable for now.
	return nil, ErrAgentNotAvailable
}

func (a *CopilotAdapter) Name() string { return "copilot" }

func (a *CopilotAdapter) Run(_ context.Context, _ string) (AgentResult, error) {
	return AgentResult{AgentName: "copilot"}, ErrAgentNotAvailable
}
