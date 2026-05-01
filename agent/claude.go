package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// ClaudeAdapter calls the `claude` CLI in non-interactive mode.
type ClaudeAdapter struct {
	Model                string
	DangerouslySkipPerms bool
	WorkDir              string
}

type claudeResponse struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
	Result  string `json:"result"`
	IsError bool   `json:"is_error"`
}

func NewClaudeAdapter(model string, skipPerms bool, workDir string) (*ClaudeAdapter, error) {
	if _, err := exec.LookPath("claude"); err != nil {
		return nil, ErrAgentNotAvailable
	}
	if model == "" {
		model = "claude-sonnet-4-5"
	}
	return &ClaudeAdapter{Model: model, DangerouslySkipPerms: skipPerms, WorkDir: workDir}, nil
}

func (a *ClaudeAdapter) Name() string { return "claude" }

func (a *ClaudeAdapter) Run(ctx context.Context, prompt string) (AgentResult, error) {
	args := []string{"-p", prompt, "--output-format", "json", "--model", a.Model}
	if a.DangerouslySkipPerms {
		args = append(args, "--dangerously-skip-permissions")
	}

	stdout, stderr, code, err := runCommand(ctx, "claude", args, a.WorkDir)
	if err != nil {
		return AgentResult{AgentName: "claude", Stderr: stderr, ExitCode: code}, err
	}

	text, parseErr := parseClaudeJSON(stdout)
	if parseErr != nil {
		// Fallback: return raw stdout if JSON parse fails
		return AgentResult{AgentName: "claude", Output: strings.TrimSpace(stdout), Stderr: stderr, ExitCode: code}, nil
	}
	return AgentResult{AgentName: "claude", Output: text, Stderr: stderr, ExitCode: code}, nil
}

func parseClaudeJSON(raw string) (string, error) {
	// claude --output-format json may emit multiple JSON objects (one per turn).
	// We want the last one with type=="result".
	lines := strings.Split(strings.TrimSpace(raw), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var resp claudeResponse
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			continue
		}
		if resp.Type == "result" {
			if resp.IsError {
				return "", fmt.Errorf("claude returned error result")
			}
			return resp.Result, nil
		}
	}
	return "", fmt.Errorf("no result object found in claude output")
}
