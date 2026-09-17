package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// DefaultCopilotModel lets the Copilot CLI pick a model it actually has access to.
// Hardcoded model names go stale quickly and are rejected with "not available".
const DefaultCopilotModel = "auto"

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
		model = DefaultCopilotModel
	}
	return &CopilotAdapter{Model: model, WorkDir: workDir}, nil
}

func (a *CopilotAdapter) Name() string { return "copilot" }

func (a *CopilotAdapter) baseArgs(prompt string) []string {
	args := []string{"-p", prompt, "--output-format", "json", "--no-color"}
	if a.Model != "" && a.Model != "auto" {
		args = append(args, "--model", a.Model)
	}
	return args
}

// Chat runs copilot without --allow-all: in non-interactive mode no tool can be
// approved, so it answers as a plain LLM.
func (a *CopilotAdapter) Chat(ctx context.Context, prompt string) (AgentResult, error) {
	return a.exec(ctx, a.baseArgs(prompt))
}

func (a *CopilotAdapter) Run(ctx context.Context, prompt string) (AgentResult, error) {
	return a.exec(ctx, append(a.baseArgs(prompt), "--allow-all"))
}

func (a *CopilotAdapter) exec(ctx context.Context, args []string) (AgentResult, error) {
	stdout, stderr, code, err := runCommand(ctx, "copilot", args, a.WorkDir, nil)
	if err != nil {
		return AgentResult{AgentName: "copilot", Output: strings.TrimSpace(stdout), Stderr: stderr, ExitCode: code}, err
	}
	text, parseErr := parseCopilotJSON(stdout)
	if parseErr != nil {
		return AgentResult{AgentName: "copilot", Output: strings.TrimSpace(stdout), Stderr: stderr, ExitCode: code}, parseErr
	}
	return AgentResult{AgentName: "copilot", Output: text, Stderr: stderr, ExitCode: code}, nil
}

type copilotEvent struct {
	Type string `json:"type"`
	Data struct {
		Content string `json:"content"`
		Message string `json:"message"`
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
		switch ev.Type {
		case "assistant.message":
			if ev.Data.Content != "" {
				parts = append(parts, ev.Data.Content)
			}
		case "error", "session.error":
			msg := ev.Data.Message
			if msg == "" {
				msg = ev.Data.Content
			}
			if msg == "" {
				msg = "unknown error"
			}
			return "", fmt.Errorf("copilot: %s", msg)
		}
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("copilot: no assistant.message found in output")
	}
	return strings.Join(parts, "\n"), nil
}
