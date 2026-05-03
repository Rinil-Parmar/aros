package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// CopilotAdapter calls the `copilot` CLI (GitHub Copilot CLI) in non-interactive mode.
// Invocation: copilot -p "<prompt>" --output-format json --no-color [--model <model>] --allow-all
type CopilotAdapter struct {
	Model   string
	WorkDir string
}

func NewCopilotAdapter(model, workDir string) (*CopilotAdapter, error) {
	if _, err := exec.LookPath("copilot"); err != nil {
		return nil, ErrAgentNotAvailable
	}
	if model == "" {
		model = "gpt-4.1"
	}
	return &CopilotAdapter{Model: model, WorkDir: workDir}, nil
}

func (a *CopilotAdapter) Name() string { return "copilot" }

func (a *CopilotAdapter) Run(ctx context.Context, prompt string) (AgentResult, error) {
	args := []string{
		"-p", prompt,
		"--output-format", "json",
		"--no-color",
		"--model", a.Model,
		"--allow-all",
	}

	stdout, stderr, code, err := runCommand(ctx, "copilot", args, a.WorkDir)
	if err != nil {
		return AgentResult{AgentName: "copilot", Stderr: stderr, ExitCode: code}, err
	}

	text, parseErr := parseCopilotJSON(stdout)
	if parseErr != nil {
		return AgentResult{AgentName: "copilot", Output: strings.TrimSpace(stdout), Stderr: stderr, ExitCode: code}, nil
	}
	return AgentResult{AgentName: "copilot", Output: text, Stderr: stderr, ExitCode: code}, nil
}

type copilotEvent struct {
	Type string `json:"type"`
	Data struct {
		Content string `json:"content"`
	} `json:"data"`
}

// parseCopilotJSON collects all assistant.message events and concatenates their content.
func parseCopilotJSON(raw string) (string, error) {
	var parts []string
	for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var ev copilotEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		if ev.Type == "assistant.message" && ev.Data.Content != "" {
			parts = append(parts, ev.Data.Content)
		}
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("no assistant.message found in copilot output")
	}
	return strings.Join(parts, "\n"), nil
}
