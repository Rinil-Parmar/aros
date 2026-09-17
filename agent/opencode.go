package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// DefaultOpenCodeModel is used when no model is configured.
// Run `opencode models` to list what your installation can reach.
const DefaultOpenCodeModel = "openai/gpt-5.4-mini"

// OpenCodeAdapter calls the `opencode` CLI in headless mode.
type OpenCodeAdapter struct {
	name    string // registry name (e.g. "opencode", "opencode-gemini")
	Model   string
	WorkDir string
	APIKey  string // set if configured; passed as OPENAI_API_KEY / ANTHROPIC_API_KEY etc.
}

// opencodeEvent represents one line of opencode's NDJSON event stream.
// Actual format: {"type":"text","part":{"text":"..."}} or {"type":"error","error":{...}}
type opencodeEvent struct {
	Type string `json:"type"`
	Part struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"part"`
	Error struct {
		Name string `json:"name"`
		Data struct {
			Message string `json:"message"`
		} `json:"data"`
	} `json:"error"`
}

// NewOpenCodeAdapter builds an adapter registered under name (the config key).
func NewOpenCodeAdapter(name, model, apiKey, workDir string) (*OpenCodeAdapter, error) {
	if _, err := exec.LookPath("opencode"); err != nil {
		return nil, ErrAgentNotAvailable
	}
	if name == "" {
		name = "opencode"
	}
	if model == "" {
		model = DefaultOpenCodeModel
	}
	return &OpenCodeAdapter{name: name, Model: model, WorkDir: workDir, APIKey: apiKey}, nil
}

// apiKeyEnv returns the environment variable name for the given model prefix.
func apiKeyEnv(model string) string {
	switch {
	case strings.HasPrefix(model, "openai/"):
		return "OPENAI_API_KEY"
	case strings.HasPrefix(model, "anthropic/"):
		return "ANTHROPIC_API_KEY"
	case strings.HasPrefix(model, "google/"):
		return "GOOGLE_API_KEY"
	case strings.HasPrefix(model, "deepseek/"):
		return "DEEPSEEK_API_KEY"
	default:
		return "OPENAI_API_KEY"
	}
}

func (a *OpenCodeAdapter) Name() string { return a.name }

func (a *OpenCodeAdapter) env() []string {
	if a.APIKey == "" {
		return nil
	}
	return []string{apiKeyEnv(a.Model) + "=" + a.APIKey}
}

// Chat runs with --pure (no external plugins) for faster conversational replies.
func (a *OpenCodeAdapter) Chat(ctx context.Context, prompt string) (AgentResult, error) {
	return a.exec(ctx, []string{"run", prompt, "--format", "json", "-m", a.Model, "--pure"})
}

func (a *OpenCodeAdapter) Run(ctx context.Context, prompt string) (AgentResult, error) {
	return a.exec(ctx, []string{"run", prompt, "--format", "json", "-m", a.Model})
}

func (a *OpenCodeAdapter) exec(ctx context.Context, args []string) (AgentResult, error) {
	stdout, stderr, code, err := runCommand(ctx, "opencode", args, a.WorkDir, nil, a.env()...)
	if err != nil {
		return AgentResult{AgentName: a.name, Output: strings.TrimSpace(stdout), Stderr: stderr, ExitCode: code}, err
	}
	// opencode exits 0 even when the provider fails — the error lives in the event stream.
	text, parseErr := parseOpenCodeEvents(stdout)
	if parseErr != nil {
		return AgentResult{AgentName: a.name, Output: strings.TrimSpace(stdout), Stderr: stderr, ExitCode: code}, parseErr
	}
	return AgentResult{AgentName: a.name, Output: text, Stderr: stderr, ExitCode: code}, nil
}

// parseOpenCodeEvents reads the NDJSON event stream and extracts assistant text.
func parseOpenCodeEvents(raw string) (string, error) {
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
		if ev.Type == "error" {
			msg := ev.Error.Data.Message
			if msg == "" {
				msg = ev.Error.Name
			}
			if msg == "" {
				msg = "unknown API error"
			}
			return "", fmt.Errorf("opencode: %s", msg)
		}
		if ev.Type == "text" && ev.Part.Text != "" {
			parts = append(parts, ev.Part.Text)
		}
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("opencode: no text events in output")
	}
	return strings.TrimSpace(strings.Join(parts, "\n")), nil
}
