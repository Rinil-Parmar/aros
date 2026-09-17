package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// DefaultClaudeModel is used when no model is configured.
const DefaultClaudeModel = "haiku"

// ClaudeAdapter calls the `claude` CLI in non-interactive mode.
// The prompt is passed on stdin (claude -p reads stdin when no prompt argument
// is given) so large prompts never hit the kernel's per-argument size limit.
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

// errNoResult means stdout held no result object at all (parse failure).
var errNoResult = errors.New("no result object found in claude output")

func NewClaudeAdapter(model string, skipPerms bool, workDir string) (*ClaudeAdapter, error) {
	if _, err := exec.LookPath("claude"); err != nil {
		return nil, ErrAgentNotAvailable
	}
	if model == "" {
		model = DefaultClaudeModel
	}
	return &ClaudeAdapter{Model: model, DangerouslySkipPerms: skipPerms, WorkDir: workDir}, nil
}

func (a *ClaudeAdapter) Name() string { return "claude" }

// Chat runs claude with --tools "" so it responds as a plain LLM without tool use.
// Fast and non-blocking — suitable for conversational queries.
func (a *ClaudeAdapter) Chat(ctx context.Context, prompt string) (AgentResult, error) {
	args := []string{"-p", "--output-format", "json", "--model", a.Model, "--tools", ""}
	return a.exec(ctx, args, prompt)
}

func (a *ClaudeAdapter) Run(ctx context.Context, prompt string) (AgentResult, error) {
	args := []string{"-p", "--output-format", "json", "--model", a.Model}
	if a.DangerouslySkipPerms {
		args = append(args, "--dangerously-skip-permissions")
	}
	return a.exec(ctx, args, prompt)
}

func (a *ClaudeAdapter) exec(ctx context.Context, args []string, prompt string) (AgentResult, error) {
	stdout, stderr, code, err := runCommand(ctx, "claude", args, a.WorkDir, strings.NewReader(prompt))
	if err != nil {
		return AgentResult{AgentName: "claude", Output: strings.TrimSpace(stdout), Stderr: stderr, ExitCode: code}, err
	}
	text, parseErr := parseClaudeJSON(stdout)
	if parseErr != nil {
		if errors.Is(parseErr, errNoResult) {
			// Not JSON at all (older CLI / plain text) — return raw stdout.
			return AgentResult{AgentName: "claude", Output: strings.TrimSpace(stdout), Stderr: stderr, ExitCode: code}, nil
		}
		return AgentResult{AgentName: "claude", Output: text, Stderr: stderr, ExitCode: code}, parseErr
	}
	return AgentResult{AgentName: "claude", Output: text, Stderr: stderr, ExitCode: code}, nil
}

// parseClaudeJSON extracts the result text from `claude --output-format json`.
// It returns errNoResult when no result object is present, and a descriptive
// error (with the result text) when claude reports is_error.
func parseClaudeJSON(raw string) (string, error) {
	// claude may emit multiple JSON objects (one per line); the last "result" wins.
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
				msg := strings.TrimSpace(resp.Result)
				if msg == "" {
					msg = resp.Subtype
				}
				return resp.Result, fmt.Errorf("claude error (%s): %s", resp.Subtype, msg)
			}
			return resp.Result, nil
		}
	}
	return "", errNoResult
}
