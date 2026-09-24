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
if [[ -n "${AROS_TEST_SLEEP:-}" ]]; then sleep "$AROS_TEST_SLEEP"; fi
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

const testConfigTmpl = `[judge]
agent = "claude"
[agents.claude]
enabled = true
model = "%s"
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

func newHarness(t *testing.T) *harness { return newHarnessWith(t, true) }

// newHarnessWith(t, false) uses the real `claude` on PATH instead of the mock.
func newHarnessWith(t *testing.T, mock bool) *harness {
	t.Helper()
	dir := t.TempDir()
	if mock {
		bin := filepath.Join(dir, "bin")
		if err := os.MkdirAll(bin, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(mockClaude), 0755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
		// Isolate the global ~/.aros. Only safe with the mock: the real CLIs
		// read their credentials from the real HOME and hang without them.
		t.Setenv("HOME", dir)
	}

	proj := filepath.Join(dir, "project")
	if err := os.MkdirAll(filepath.Join(proj, ".aros"), 0755); err != nil {
		t.Fatal(err)
	}
	model := "mock"
	if !mock {
		model = "haiku" // real CLI: use a cheap, real model
	}
	cfg := fmt.Sprintf(testConfigTmpl, model)
	if err := os.WriteFile(filepath.Join(proj, ".aros", "config.toml"), []byte(cfg), 0644); err != nil {
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

// ── "stuck between modes" regression tests ──────────────────────────────────
//
// These drive the model into every mode that can wait on something (approval,
// free-text question, running phase, in-flight chat), abort it, and assert the
// UI comes back to a clean state that accepts the next command.

// assertIdle checks the model is in a fully operable idle state.
func (h *harness) assertIdle(what string) {
	h.t.Helper()
	h.inspect(func(m *Model) {
		if m.busy || m.chatInProgress {
			h.t.Errorf("%s: still busy (busy=%v chat=%v)", what, m.busy, m.chatInProgress)
		}
		if m.mode != modeText {
			h.t.Errorf("%s: mode=%d, want modeText", what, m.mode)
		}
		if m.onYes != nil || m.onNo != nil || m.onFreeText != nil {
			h.t.Errorf("%s: pending callbacks not cleared", what)
		}
		if m.approvalQuestion != "" {
			h.t.Errorf("%s: approval question still set", what)
		}
		if len(m.activity) != 0 {
			h.t.Errorf("%s: activity panel not cleared", what)
		}
	})
}

func (h *harness) esc() { h.p.Send(tea.KeyMsg{Type: tea.KeyEsc}) }

func (h *harness) startProject() {
	h.t.Helper()
	h.waitFor("bootstrap", 10*time.Second, func(m tea.Msg) bool { _, ok := m.(bootstrapMsg); return ok })
	h.typeLine("demo")
	h.waitFor("session created", 10*time.Second, func(m tea.Msg) bool { _, ok := m.(sessionLoadedMsg); return ok })
}

// Esc must unstick every waiting mode, and a new phase must start right after.
func TestEscCancelsEveryMode(t *testing.T) {
	h := newHarness(t)
	defer h.quit()
	h.startProject()

	// 1. Esc while an approval is pending.
	h.typeLine("plan a thing")
	h.waitFor("approval", 30*time.Second, isApproval)
	h.esc()
	h.assertIdle("after Esc on approval")

	// 2. Esc while a free-text question is pending.
	h.typeLine("plan")
	h.waitFor("task question", 10*time.Second, func(m tea.Msg) bool {
		k, ok := m.(tea.KeyMsg)
		return ok && k.Type == tea.KeyEnter
	})
	h.inspect(func(m *Model) {
		if m.onFreeText == nil {
			t.Error("expected a pending free-text question")
		}
	})
	h.esc()
	h.assertIdle("after Esc on free-text question")

	// 3. Esc while a phase is actually running (slow agent).
	t.Setenv("AROS_TEST_SLEEP", "20")
	h.typeLine("plan something slow")
	h.waitFor("plan started", 10*time.Second, func(m tea.Msg) bool {
		sl, ok := m.(streamLineMsg)
		return ok && strings.Contains(sl.line, "Querying")
	})
	h.esc()
	h.assertIdle("after Esc on running phase")

	// 4. Esc while a chat is in flight.
	h.typeLine("how does this work?")
	h.inspect(func(m *Model) {
		if !m.chatInProgress {
			t.Error("expected chat to be in progress")
		}
	})
	h.esc()
	h.assertIdle("after Esc on chat")

	// 5. After all that, a normal phase still runs to completion.
	t.Setenv("AROS_TEST_SLEEP", "")
	h.typeLine("plan a fast thing")
	h.waitFor("approval after cancels", 30*time.Second, isApproval)
	h.typeLine("y")
	h.waitFor("plan_done", 30*time.Second, phaseDone("plan_done"))
	h.assertIdle("after completed plan")
}

// A late message from an aborted run must not disturb the run that replaced it.
func TestStaleMessagesFromAbortedRunAreIgnored(t *testing.T) {
	h := newHarness(t)
	defer h.quit()
	h.startProject()

	h.typeLine("plan first")
	h.waitFor("first approval", 30*time.Second, isApproval)

	// Capture the retired generation, then abort and start a new run.
	var oldGen int
	h.inspect(func(m *Model) { oldGen = m.phaseGen })
	h.esc()
	h.typeLine("plan second")
	h.waitFor("second approval", 30*time.Second, isApproval)

	// Replay the aborted run's messages: they must all be dropped.
	h.p.Send(phaseResultMsg{gen: oldGen, phase: "plan_done"})
	h.p.Send(freeInputMsg{gen: oldGen, prompt: "stale question", callback: func(string) {}})
	h.p.Send(approvalMsg{gen: oldGen, question: "stale approval"})
	h.inspect(func(m *Model) {
		if !m.busy {
			t.Error("stale phase result cleared busy on the live run")
		}
		if m.onFreeText != nil {
			t.Error("stale free-text prompt was installed")
		}
		if m.approvalQuestion == "stale approval" {
			t.Error("stale approval replaced the live one")
		}
	})

	// The live run still completes normally.
	h.typeLine("y")
	h.waitFor("plan_done", 30*time.Second, phaseDone("plan_done"))
	h.inspect(func(m *Model) {
		if m.project.Task != "second" {
			t.Errorf("wrong task persisted: %q", m.project.Task)
		}
	})
}

// Starting a phase while one is already running must not leave two live runs.
func TestSupersededRunDoesNotWedge(t *testing.T) {
	h := newHarness(t)
	defer h.quit()
	h.startProject()

	h.typeLine("plan one")
	h.waitFor("approval", 30*time.Second, isApproval)

	// 'divide' while an approval is pending is rejected, not silently queued.
	h.typeLine("divide")
	h.inspect(func(m *Model) {
		if m.mode != modeApproval {
			t.Errorf("approval was dropped by a rejected command (mode=%d)", m.mode)
		}
	})

	// --force supersedes: old generation retired, model immediately usable.
	h.typeLine("/phase plan --force")
	h.assertIdle("after --force during approval")
	h.typeLine("status")
	h.inspect(func(m *Model) {
		if m.busy {
			t.Error("status left the model busy")
		}
	})
}

// A panic inside a phase goroutine must surface as an error, not kill the app.
func TestPanicInPhaseGoroutineIsRecovered(t *testing.T) {
	h := newHarness(t)
	defer h.quit()
	h.startProject()

	h.inspect(func(m *Model) { m.busy = true; m.phaseGen++ })
	var gen int
	h.inspect(func(m *Model) { gen = m.phaseGen })
	safeGo(gen, func() { panic("boom") })
	h.waitFor("panic reported as phase error", 10*time.Second, func(m tea.Msg) bool {
		pr, ok := m.(phaseResultMsg)
		return ok && pr.err != nil && strings.Contains(pr.err.Error(), "internal error")
	})
	h.assertIdle("after recovered panic")
}

// Two fast lines of input must not create two projects.
func TestDoubleInitCreatesOneSession(t *testing.T) {
	h := newHarness(t)
	defer h.quit()
	h.waitFor("bootstrap", 10*time.Second, func(m tea.Msg) bool { _, ok := m.(bootstrapMsg); return ok })

	h.typeLine("projA")
	h.typeLine("projB")
	h.waitFor("session created", 10*time.Second, func(m tea.Msg) bool { _, ok := m.(sessionLoadedMsg); return ok })

	sessions, err := state.ListSessions(filepath.Join(mustCwd(t), ".aros"))
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions, want 1: %+v", len(sessions), sessions)
	}
}

// TestTUIWithRealClaude drives the TUI against the real `claude` CLI.
// Opt-in (it spends tokens):  AROS_TEST_REAL=1 go test -run RealClaude ./tui/
func TestTUIWithRealClaude(t *testing.T) {
	if os.Getenv("AROS_TEST_REAL") == "" {
		t.Skip("set AROS_TEST_REAL=1 to run against the real claude CLI")
	}
	h := newHarnessWith(t, false)
	defer h.quit()
	h.startProject()

	// Chat must answer without running tools.
	h.typeLine("Reply with exactly: pong")
	h.waitFor("real chat reply", 120*time.Second, func(m tea.Msg) bool {
		cd, ok := m.(chatDoneMsg)
		if ok && cd.err != nil {
			t.Fatalf("real chat failed: %v", cd.err)
		}
		return ok
	})
	h.inspect(func(m *Model) {
		var got string
		for _, msg := range m.messages {
			if msg.Kind == kindAgent {
				got = msg.Body
			}
		}
		if !strings.Contains(strings.ToLower(got), "pong") {
			t.Errorf("chat reply did not contain pong: %q", got)
		}
	})
	h.assertIdle("after real chat")

	// Plan must produce a real plan and reach the approval card.
	h.typeLine("plan add a --version flag to a tiny Go CLI")
	h.waitFor("real plan approval", 180*time.Second, isApproval)
	h.typeLine("y")
	h.waitFor("plan_done", 60*time.Second, phaseDone("plan_done"))
	h.inspect(func(m *Model) {
		if len(m.project.ApprovedPlan) < 80 {
			t.Errorf("approved plan looks empty: %q", m.project.ApprovedPlan)
		}
	})
	h.assertIdle("after real plan")
}
