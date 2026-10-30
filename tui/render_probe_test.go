package tui

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/Rinil-Parmar/aros/config"
	"github.com/Rinil-Parmar/aros/state"
	tea "github.com/charmbracelet/bubbletea"
)

var ansi = regexp.MustCompile("\x1b\\[[0-9;]*m")

// TestRenderProbe prints the real View() so the layout can be inspected.
// Run with: go test -run RenderProbe ./tui/ -v
func TestRenderProbe(t *testing.T) {
	if os.Getenv("AROS_RENDER_PROBE") == "" {
		t.Skip("set AROS_RENDER_PROBE=1")
	}
	m := New("")
	m.cfg = &config.Config{
		Judge: config.JudgeConfig{Agent: "claude"},
		Agents: map[string]config.AgentConfig{
			"claude":   {Enabled: true, Model: "haiku", Dense: "full"},
			"opencode": {Enabled: true, Model: "openai/gpt-5.4-mini"},
			"copilot":  {Enabled: true, Model: "auto"},
		},
		Work: config.WorkConfig{MaxConcurrent: 2, AgentTimeoutSeconds: 300},
	}
	m.project = &state.ProjectState{ProjectName: "paramrut-api", Phase: state.PhaseWork, Task: "add a REST endpoint for scripture search with pagination"}
	m.sessionID = "sess-1793341423693357249-0f70c1ac"
	m.sessionName = "paramrut-api"
	m.manifest = &state.TaskManifest{Tasks: []state.Task{
		{ID: "task-001", Title: "Define the search request and response types", AssignedTo: "claude", Status: state.TaskDone},
		{ID: "task-002", Title: "Implement the pagination helper", AssignedTo: "opencode", Status: state.TaskInProgress, Dependencies: []string{"task-001"}},
		{ID: "task-003", Title: "Wire the handler into the router and add tests", AssignedTo: "copilot", Status: state.TaskPending, Dependencies: []string{"task-002"}},
		{ID: "task-004", Title: "Update the OpenAPI document", AssignedTo: "claude", Status: state.TaskBlocked},
	}}
	m.activity = map[string]*agentStatus{
		"opencode": {model: "openai/gpt-5.4-mini", status: "running", line: "[task-002] Implement the pagination helper"},
		"claude":   {model: "haiku", status: "done", line: "[task-001] done"},
	}
	m.busy = true

	W, H := 120, 36
	if v := os.Getenv("AROS_PROBE_SIZE"); v != "" {
		fmt.Sscanf(v, "%dx%d", &W, &H)
	}
	m.Update(tea.WindowSizeMsg{Width: W, Height: H})
	m.showBanner()
	m.showSessionState()
	m.addHuman("plan add a REST endpoint for scripture search with pagination")
	m.appendStream("judge", "Synthesised plan: introduce a SearchRequest type with Limit and Offset,")
	m.appendStream("judge", "validate them in the handler, and return a SearchResponse carrying Total.")
	m.appendStream("opencode", "Reading internal/store/search.go to find the existing query path.")
	m.addError("task-004 blocked: agent claude failed: claude error (rate_limit)")
	m.addSuccess("Plan approved.")

	if os.Getenv("AROS_PROBE_APPROVAL") != "" {
		m.approvalQuestion = "Commit 7 changed file(s)?"
		m.setMode(modeApproval, "y/n")
	}
	m.Update(tea.WindowSizeMsg{Width: W, Height: H})
	out := m.View()
	fmt.Println("════════ WITH STYLING (as the terminal shows it) ════════")
	fmt.Println(out)
	fmt.Println("════════ PLAIN (structure only) ════════")
	plain := ansi.ReplaceAllString(out, "")
	for i, line := range strings.Split(plain, "\n") {
		fmt.Printf("%2d|%s|\n", i+1, line)
	}
	fmt.Printf("\nwidth=%d leftW=%d rightW=%d colH=%d  rendered lines=%d\n",
		m.width, m.leftW, m.rightW, m.colH, len(strings.Split(plain, "\n")))
}
