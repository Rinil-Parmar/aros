package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Rinil-Parmar/aros/state"
	tea "github.com/charmbracelet/bubbletea"
)

// mockClaude answers every prompt shape the TUI produces. Prompt arrives on stdin.
const mockClaude = `#!/usr/bin/env bash
set -euo pipefail
prompt="$(cat)"
if [[ "$prompt" == *"Break this plan into concrete tasks."* ]]; then
  cat <<'JSON'
{"type":"result","result":"[{\"id\":\"task-001\",\"title\":\"Core\",\"description\":\"Implement core.\",\"assigned_to\":\"claude\",\"dependencies\":[]},{\"id\":\"task-002\",\"title\":\"Tests\",\"description\":\"Add tests.\",\"assigned_to\":\"claude\",\"dependencies\":[\"task-001\"]},{\"id\":\"task-003\",\"title\":\"Docs\",\"description\":\"Write docs.\",\"assigned_to\":\"claude\",\"dependencies\":[]}]","is_error":false}
JSON
elif [[ "$prompt" == *"You are working on task"* ]]; then
  cat <<'JSON'
{"type":"result","result":"Task implemented.","is_error":false}
JSON
elif [[ "$prompt" == *"Synthesize"* ]]; then
  cat <<'JSON'
{"type":"result","result":"Synthesized plan: do the thing.","is_error":false}
JSON
elif [[ "$prompt" == *"QUESTION:"* ]]; then
  cat <<'JSON'
{"type":"result","result":"Chat answer: 42","is_error":false}
JSON
else
  cat <<'JSON'
{"type":"result","result":"Agent plan: build it.","is_error":false}
JSON
fi
`

const testConfig = `[judge]
agent = "claude"
[agents.claude]
enabled = true
model = "mock"
[agents.opencode]
enabled = false
[agents.copilot]
enabled = false
[secondmem]
enabled = false
[work]
max_concurrent = 1
agent_timeout_seconds = 30
`

// harness runs the real bubbletea event loop headless and lets the test wait
// for specific messages to be processed (observed via tea.WithFilter, which
// runs on the event-loop goroutine — so reading the model there is safe).
type harness struct {
	t    *testing.T
	m    *Model
	p    *tea.Program
	mu   sync.Mutex
	seen []tea.Msg
	cur  int // waitFor cursor: only messages after the last match count
	wake chan struct{}
	done chan error
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(mockClaude), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", dir) // isolate ~/.aros

	proj := filepath.Join(dir, "project")
	if err := os.MkdirAll(filepath.Join(proj, ".aros"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, ".aros", "config.toml"), []byte(testConfig), 0644); err != nil {
		t.Fatal(err)
	}
	old, _ := os.Getwd()
	if err := os.Chdir(proj); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })

	h := &harness{t: t, wake: make(chan struct{}, 64), done: make(chan error, 1)}
	h.m = New("")
	h.p = tea.NewProgram(h.m,
		tea.WithInput(nil),
		tea.WithoutRenderer(),
		tea.WithoutSignalHandler(),
		tea.WithFilter(func(_ tea.Model, msg tea.Msg) tea.Msg {
			if pm, ok := msg.(probeMsg); ok {
				pm.fn(h.m) // runs on the event-loop goroutine: safe model access
				close(pm.done)
			}
			h.mu.Lock()
			h.seen = append(h.seen, msg)
			h.mu.Unlock()
			select {
			case h.wake <- struct{}{}:
			default:
			}
			return msg
		}),
	)
	SetProgram(h.p)
	go func() {
		_, err := h.p.Run()
		h.done <- err
	}()
	h.p.Send(tea.WindowSizeMsg{Width: 120, Height: 40})
	return h
}

// probeMsg lets a test read the model synchronously on the event loop.
type probeMsg struct {
	fn   func(*Model)
	done chan struct{}
}

func (h *harness) inspect(fn func(*Model)) {
	h.t.Helper()
	pm := probeMsg{fn: fn, done: make(chan struct{})}
	h.p.Send(pm)
	select {
	case <-pm.done:
	case <-time.After(10 * time.Second):
		h.t.Fatal("event loop did not process probe — deadlocked?")
	}
}

func (h *harness) typeLine(s string) {
	h.p.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)})
	h.p.Send(tea.KeyMsg{Type: tea.KeyEnter})
}

// waitFor blocks until a processed message satisfies pred, or fails the test
// after timeout (which is how a deadlocked event loop shows up).
func (h *harness) waitFor(what string, timeout time.Duration, pred func(tea.Msg) bool) {
	h.t.Helper()
	deadline := time.After(timeout)
	for {
		h.mu.Lock()
		for ; h.cur < len(h.seen); h.cur++ {
			if pred(h.seen[h.cur]) {
				h.cur++
				h.mu.Unlock()
				return
			}
		}
		h.mu.Unlock()
		select {
		case <-h.wake:
		case <-deadline:
			h.t.Fatalf("timed out waiting for %s — event loop deadlocked? last messages: %s", what, h.tail())
		}
	}
}

func (h *harness) tail() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	start := len(h.seen) - 6
	if start < 0 {
		start = 0
	}
	var parts []string
	for _, m := range h.seen[start:] {
		if _, ok := m.(spinnerTick); ok {
			continue
		}
		parts = append(parts, fmt.Sprintf("%T", m))
	}
	return strings.Join(parts, ", ")
}

type spinnerTick interface{ isSpinnerTick() }

func phaseDone(name string) func(tea.Msg) bool {
	return func(m tea.Msg) bool {
		pr, ok := m.(phaseResultMsg)
		return ok && pr.phase == name && pr.err == nil
	}
}

func isApproval(m tea.Msg) bool { _, ok := m.(approvalMsg); return ok }

func (h *harness) quit() {
	h.p.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
	select {
	case err := <-h.done:
		if err != nil {
			h.t.Fatalf("program exited with error: %v", err)
		}
	case <-time.After(10 * time.Second):
		h.t.Fatal("program did not exit")
	}
}

func TestTUIFullFlowNoDeadlock(t *testing.T) {
	h := newHarness(t)
	defer h.quit()

	h.waitFor("bootstrap", 10*time.Second, func(m tea.Msg) bool { _, ok := m.(bootstrapMsg); return ok })

	// No project yet → first line names the project.
	h.typeLine("demo")
	h.waitFor("session created", 10*time.Second, func(m tea.Msg) bool { _, ok := m.(sessionLoadedMsg); return ok })

	// Chat before any phase: exercises startChat, which used to send() from Update.
	h.typeLine("what should I do first?")
	h.waitFor("chat done", 20*time.Second, func(m tea.Msg) bool {
		cd, ok := m.(chatDoneMsg)
		return ok && cd.err == nil
	})

	h.typeLine("plan build a widget")
	h.waitFor("plan approval", 30*time.Second, isApproval)
	h.p.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")}) // bare key, no Enter
	h.waitFor("feedback prompt", 10*time.Second, func(m tea.Msg) bool { _, ok := m.(freeInputMsg); return ok })
	h.typeLine("make it simpler")
	h.waitFor("revised plan approval", 30*time.Second, func(m tea.Msg) bool {
		a, ok := m.(approvalMsg)
		return ok && strings.Contains(a.question, "revised")
	})
	h.p.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	h.waitFor("plan_done", 30*time.Second, phaseDone("plan_done"))

	h.typeLine("divide")
	h.waitFor("divide approval", 30*time.Second, isApproval)
	h.typeLine("yes")
	h.waitFor("divide_done", 30*time.Second, phaseDone("divide_done"))

	// 3 tasks with max_concurrent=1: the old dispatcher deadlocked here.
	h.typeLine("work")
	h.waitFor("work_done", 60*time.Second, phaseDone("work_done"))

	// Chat after everything — also exercises the manifest/project context path.
	h.typeLine("summarize")
	h.waitFor("chat done again", 20*time.Second, func(m tea.Msg) bool {
		cd, ok := m.(chatDoneMsg)
		return ok && cd.err == nil
	})

	// Verify persisted state.
	arosDir := filepath.Join(mustCwd(t), ".aros")
	s, err := state.LoadState(arosDir)
	if err != nil {
		t.Fatal(err)
	}
	if s.Phase != state.PhaseDone {
		t.Fatalf("phase=%s want done", s.Phase)
	}
	mf, err := state.LoadManifest(arosDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(mf.Tasks) != 3 {
		t.Fatalf("tasks=%d", len(mf.Tasks))
	}
	for _, task := range mf.Tasks {
		if task.Status != state.TaskDone || task.Output == "" {
			t.Fatalf("task %s: %+v", task.ID, task)
		}
	}
}

func TestTUIForceAbortsRunningPhase(t *testing.T) {
	h := newHarness(t)
	defer h.quit()
	h.waitFor("bootstrap", 10*time.Second, func(m tea.Msg) bool { _, ok := m.(bootstrapMsg); return ok })
	h.typeLine("demo")
	h.waitFor("session created", 10*time.Second, func(m tea.Msg) bool { _, ok := m.(sessionLoadedMsg); return ok })

	h.typeLine("plan slow thing")
	h.waitFor("plan approval", 30*time.Second, isApproval)
	h.inspect(func(m *Model) {
		if m.mode != modeApproval || m.onYes == nil {
			t.Errorf("expected approval mode, got mode=%d", m.mode)
		}
	})

	// Abort while waiting for approval; input must unlock and approval must be dropped.
	h.typeLine("/phase plan --force")
	h.inspect(func(m *Model) {
		if m.busy || m.chatInProgress {
			t.Error("model still busy after --force")
		}
		if m.mode == modeApproval || m.onYes != nil || m.approvalQuestion != "" {
			t.Error("approval state not cleared after --force")
		}
		if m.project == nil || m.project.Phase != state.PhasePlan {
			t.Errorf("phase not changed: %+v", m.project)
		}
		if len(m.activity) != 0 {
			t.Error("activity panel not cleared")
		}
	})

	// A wrong answer in approval mode must not be treated as a chat.
	h.typeLine("plan again")
	h.waitFor("plan approval 2", 30*time.Second, isApproval)
	h.typeLine("maybe")
	h.inspect(func(m *Model) {
		if m.mode != modeApproval || m.chatInProgress {
			t.Errorf("non y/n input left approval mode (mode=%d chat=%v)", m.mode, m.chatInProgress)
		}
	})
	h.typeLine("n")
	h.waitFor("feedback prompt", 10*time.Second, func(m tea.Msg) bool { _, ok := m.(freeInputMsg); return ok })
	h.typeLine("shorter")
	h.waitFor("revised approval", 30*time.Second, func(m tea.Msg) bool {
		a, ok := m.(approvalMsg)
		return ok && strings.Contains(a.question, "revised")
	})
	h.typeLine("n")
	h.waitFor("feedback prompt 2", 10*time.Second, func(m tea.Msg) bool { _, ok := m.(freeInputMsg); return ok })
	h.typeLine("even shorter")
	h.waitFor("third approval", 30*time.Second, isApproval)
	h.typeLine("n") // third rejection ends the plan phase cleanly
	h.waitFor("plan cancelled", 10*time.Second, func(m tea.Msg) bool {
		pr, ok := m.(phaseResultMsg)
		return ok && pr.phase == "" && pr.err == nil
	})
	h.inspect(func(m *Model) {
		if m.busy || m.mode != modeText {
			t.Errorf("model not idle after cancelled plan: busy=%v mode=%d", m.busy, m.mode)
		}
	})
}

func mustCwd(t *testing.T) string {
	t.Helper()
	d, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return d
}
