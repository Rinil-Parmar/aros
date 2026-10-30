package tui

import (
	"strings"
	"testing"

	"github.com/Rinil-Parmar/aros/config"
	"github.com/Rinil-Parmar/aros/state"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// fixtureModel builds a model with enough content that every panel section
// renders: activity, agents, a task board, and a scrolled chat.
func fixtureModel(_ bool) *Model {
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
	m.project = &state.ProjectState{
		ProjectName: "paramrut-api",
		Phase:       state.PhaseWork,
		Task:        "add a REST endpoint for scripture search with pagination",
	}
	m.sessionID = "sess-1793341423693357249-0f70c1ac"
	m.sessionName = "paramrut-api"
	m.manifest = &state.TaskManifest{Tasks: []state.Task{
		{ID: "task-001", Title: "Define the search request and response types", AssignedTo: "claude", Status: state.TaskDone},
		{ID: "task-002", Title: "Implement the pagination helper", AssignedTo: "opencode", Status: state.TaskInProgress, Dependencies: []string{"task-001"}},
		{ID: "task-003", Title: "Wire the handler into the router and add tests", AssignedTo: "copilot", Status: state.TaskPending, Dependencies: []string{"task-002"}},
		{ID: "task-004", Title: "Update the OpenAPI document", AssignedTo: "claude", Status: state.TaskBlocked, BlockReason: "rate limited"},
	}}
	m.activity = map[string]*agentStatus{
		"opencode": {model: "openai/gpt-5.4-mini", status: "running", line: "[task-002] Implement the pagination helper"},
		"claude":   {model: "haiku", status: "done", line: "[task-001] done"},
	}
	m.busy = true
	return m
}

// setApproval puts the model into approval mode. It must run *after* populate:
// showWelcome calls setMode and would clear it.
func setApproval(m *Model) {
	m.approvalQuestion = "Commit 7 changed file(s)?"
	m.onYes, m.onNo = func() {}, func() {}
	m.setMode(modeApproval, "y/n")
}

func populate(m *Model) {
	m.showWelcome()
	m.addHuman("plan add a REST endpoint for scripture search with pagination")
	m.appendStream("judge", "Synthesised plan: introduce a SearchRequest type with Limit and Offset,")
	m.appendStream("judge", "validate them in the handler, and return a SearchResponse carrying Total.")
	m.addError("task-004 blocked: agent claude failed: claude error (rate_limit_exceeded)")
	m.addSuccess("Plan approved.")
}

// The rendered frame must fit the terminal exactly. One column too wide makes
// the terminal wrap every row; one line too tall makes it scroll. Either turns
// the whole layout into visual noise, which is what "looks unprofessional"
// actually means here.
func TestViewFitsTerminalExactly(t *testing.T) {
	sizes := []struct{ w, h int }{
		{80, 24},  // the classic minimum
		{100, 30}, // two-column threshold
		{120, 36}, // common wide terminal
		{160, 50}, // large
		{90, 28},  // just above the single-column cutoff
		{70, 20},  // single column
		{45, 12},  // cramped
	}
	for _, approval := range []bool{false, true} {
		for _, sz := range sizes {
			m := fixtureModel(approval)
			m.Update(tea.WindowSizeMsg{Width: sz.w, Height: sz.h})
			populate(m)
			if approval {
				setApproval(m)
			}
			// Re-send the size: content changed the textarea/approval height.
			m.Update(tea.WindowSizeMsg{Width: sz.w, Height: sz.h})

			out := m.View()
			if approval && !strings.Contains(stripANSI(out), "[y] yes") {
				t.Fatalf("%dx%d: approval card did not render — the test was not exercising it", sz.w, sz.h)
			}
			lines := strings.Split(out, "\n")
			// A trailing newline yields one empty final element; that is fine.
			if n := len(lines); n > 0 && lines[n-1] == "" {
				lines = lines[:n-1]
			}

			if len(lines) != sz.h {
				t.Errorf("%dx%d approval=%v: rendered %d lines, want %d",
					sz.w, sz.h, approval, len(lines), sz.h)
			}
			for i, line := range lines {
				if got := lipgloss.Width(line); got != sz.w {
					t.Errorf("%dx%d approval=%v: line %d is %d cols, want %d\n  %q",
						sz.w, sz.h, approval, i+1, got, sz.w, stripANSI(line))
					break // one report per size is enough
				}
			}
		}
	}
}

func stripANSI(s string) string { return ansi.ReplaceAllString(s, "") }
