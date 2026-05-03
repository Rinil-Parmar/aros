package agent

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
)

// OpenCodeAdapter calls the `opencode` CLI in headless mode.
type OpenCodeAdapter struct {
	Model   string
	WorkDir string
}

// opencodeEvent represents one line of opencode's NDJSON event stream.
// The exact schema may evolve; we extract any assistant text content.
type opencodeEvent struct {
	Type    string          `json:"type"`
	Content json.RawMessage `json:"content"`
	Text    string          `json:"text"`
	// assistant message variant
	Message struct {
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"message"`
}

func NewOpenCodeAdapter(model string, workDir string) (*OpenCodeAdapter, error) {
	if _, err := exec.LookPath("opencode"); err != nil {
		return nil, ErrAgentNotAvailable
	}
	if model == "" {
		model = "anthropic/claude-sonnet-4-5"
	}
	return &OpenCodeAdapter{Model: model, WorkDir: workDir}, nil
}

func (a *OpenCodeAdapter) Name() string { return "opencode" }

func (a *OpenCodeAdapter) Run(ctx context.Context, prompt string) (AgentResult, error) {
	args := []string{"run", prompt, "--format", "json", "-m", a.Model}

	stdout, stderr, code, err := runCommand(ctx, "opencode", args, a.WorkDir)
	if err != nil {
		return AgentResult{AgentName: "opencode", Stderr: stderr, ExitCode: code}, err
	}

	text := parseOpenCodeEvents(stdout)
	return AgentResult{AgentName: "opencode", Output: text, Stderr: stderr, ExitCode: code}, nil
}

// parseOpenCodeEvents reads NDJSON event stream and extracts assistant text.
func parseOpenCodeEvents(raw string) string {
	var parts []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		var ev opencodeEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		// Collect assistant message content
		if ev.Message.Role == "assistant" {
			for _, c := range ev.Message.Content {
				if c.Type == "text" && c.Text != "" {
					parts = append(parts, c.Text)
				}
			}
		}
		// Fallback: plain text field
		if ev.Type == "assistant" && ev.Text != "" {
			parts = append(parts, ev.Text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}
