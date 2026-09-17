package worker

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Rinil-Parmar/aros/agent"
	"github.com/Rinil-Parmar/aros/memory"
	"github.com/Rinil-Parmar/aros/state"
)

// mockAgent answers prompts via fn and tracks concurrency.
type mockAgent struct {
	name    string
	fn      func(prompt string) (string, error)
	active  int32
	maxSeen int32
	calls   int32
}

func (m *mockAgent) Name() string { return m.name }
func (m *mockAgent) Run(ctx context.Context, prompt string) (agent.AgentResult, error) {
	atomic.AddInt32(&m.calls, 1)
	n := atomic.AddInt32(&m.active, 1)
	for {
		seen := atomic.LoadInt32(&m.maxSeen)
		if n <= seen || atomic.CompareAndSwapInt32(&m.maxSeen, seen, n) {
			break
		}
	}
	defer atomic.AddInt32(&m.active, -1)
	select {
	case <-time.After(20 * time.Millisecond):
	case <-ctx.Done():
		return agent.AgentResult{}, ctx.Err()
	}
	out, err := m.fn(prompt)
	return agent.AgentResult{AgentName: m.name, Output: out}, err
}

func newReg(a *mockAgent) agent.Registry { return agent.Registry{a.name: a} }

func opts(maxConc int) Options {
	return Options{MaxConcurrent: maxConc, TimeoutSec: 5, PollInterval: 10 * time.Millisecond}
}

var noMem = memory.New("", false)

// Regression: dispatcher used to block on the semaphore while holding the
// mutex → deadlock as soon as ready tasks > max_concurrent.
func TestRunMoreReadyTasksThanSlots(t *testing.T) {
	a := &mockAgent{name: "claude", fn: func(p string) (string, error) { return "ok", nil }}
	m := &state.TaskManifest{}
	for i := 0; i < 6; i++ {
		m.Tasks = append(m.Tasks, state.Task{ID: fmt.Sprintf("t%d", i), Title: "T", AssignedTo: "claude", Status: state.TaskPending})
	}
	done := make(chan struct{})
	var res Result
	var err error
	go func() {
		res, err = Run(context.Background(), m, "", newReg(a), noMem, opts(2), Hooks{})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("scheduler deadlocked")
	}
	if err != nil || res.Done != 6 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if a.maxSeen > 2 {
		t.Fatalf("concurrency limit violated: %d", a.maxSeen)
	}
}

func TestRunRespectsDependencyOrder(t *testing.T) {
	var mu sync.Mutex
	var order []string
	a := &mockAgent{name: "claude", fn: func(p string) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case strings.HasPrefix(p, "You are working on task [a]"):
			order = append(order, "a")
			return "A-output", nil
		case strings.HasPrefix(p, "You are working on task [b]"):
			if !strings.Contains(p, "A-output") {
				return "", errors.New("b ran without a's output")
			}
			order = append(order, "b")
			return "B", nil
		}
		return "", errors.New("unknown task")
	}}
	m := &state.TaskManifest{Tasks: []state.Task{
		{ID: "b", Title: "B", AssignedTo: "claude", Dependencies: []string{"a"}},
		{ID: "a", Title: "A", AssignedTo: "claude"},
	}}
	res, err := Run(context.Background(), m, "", newReg(a), noMem, opts(2), Hooks{})
	if err != nil || res.Done != 2 {
		t.Fatalf("res=%+v err=%v tasks=%+v", res, err, m.Tasks)
	}
	if strings.Join(order, ",") != "a,b" {
		t.Fatalf("order=%v", order)
	}
}

// Regression: a blocked task left its dependents pending forever → infinite loop.
func TestRunBlockedCascadesAndTerminates(t *testing.T) {
	a := &mockAgent{name: "claude", fn: func(p string) (string, error) {
		if strings.Contains(p, "[root]") {
			return "", errors.New("boom")
		}
		return "ok", nil
	}}
	m := &state.TaskManifest{Tasks: []state.Task{
		{ID: "root", Title: "R", AssignedTo: "claude"},
		{ID: "child", Title: "C", AssignedTo: "claude", Dependencies: []string{"root"}},
		{ID: "grand", Title: "G", AssignedTo: "claude", Dependencies: []string{"child"}},
		{ID: "free", Title: "F", AssignedTo: "claude"},
	}}
	var blocked []string
	var bmu sync.Mutex
	hooks := Hooks{TaskBlocked: func(t *state.Task, _ string, _ string) {
		bmu.Lock()
		blocked = append(blocked, t.ID)
		bmu.Unlock()
	}}
	done := make(chan struct{})
	var res Result
	var err error
	go func() {
		res, err = Run(context.Background(), m, "", newReg(a), noMem, opts(2), hooks)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("scheduler never terminated with a blocked dependency chain")
	}
	if !errors.Is(err, ErrTasksBlocked) {
		t.Fatalf("expected ErrTasksBlocked, got %v", err)
	}
	if res.Done != 1 || res.Blocked != 3 {
		t.Fatalf("res=%+v", res)
	}
	if len(blocked) != 3 {
		t.Fatalf("blocked hooks=%v", blocked)
	}
	// Only the failing task ran; cascaded ones never invoked the agent.
	if a.calls != 2 {
		t.Fatalf("agent calls=%d, want 2 (root + free)", a.calls)
	}
}

func TestRunRetriesPreviouslyBlocked(t *testing.T) {
	a := &mockAgent{name: "claude", fn: func(p string) (string, error) { return "fixed", nil }}
	m := &state.TaskManifest{Tasks: []state.Task{
		{ID: "a", Title: "A", AssignedTo: "claude", Status: state.TaskBlocked, BlockReason: "old failure"},
		{ID: "b", Title: "B", AssignedTo: "claude", Status: state.TaskDone, Output: "kept"},
	}}
	res, err := Run(context.Background(), m, "", newReg(a), noMem, opts(1), Hooks{})
	if err != nil || res.Done != 2 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if a.calls != 1 || m.Tasks[1].Output != "kept" {
		t.Fatalf("done task was re-run or lost output: calls=%d tasks=%+v", a.calls, m.Tasks)
	}
}

func TestRunHumanSentinelLoop(t *testing.T) {
	a := &mockAgent{name: "claude", fn: func(p string) (string, error) {
		if strings.Contains(p, "Human answer to your question:\nuse postgres") {
			return "done with postgres", nil
		}
		return "I need to know.\n" + HumanSentinel + "\nWhich database?", nil
	}}
	m := &state.TaskManifest{Tasks: []state.Task{{ID: "a", Title: "A", AssignedTo: "claude"}}}
	var asked string
	hooks := Hooks{AskHuman: func(t *state.Task, q string) (string, error) {
		asked = q
		return "use postgres", nil
	}}
	res, err := Run(context.Background(), m, "", newReg(a), noMem, opts(1), hooks)
	if err != nil || res.Done != 1 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if asked != "Which database?" || m.Tasks[0].Output != "done with postgres" {
		t.Fatalf("asked=%q output=%q", asked, m.Tasks[0].Output)
	}
}

func TestRunUnknownAgentFallsBack(t *testing.T) {
	a := &mockAgent{name: "claude", fn: func(p string) (string, error) { return "ok", nil }}
	m := &state.TaskManifest{Tasks: []state.Task{{ID: "a", Title: "A", AssignedTo: "gemini"}}}
	var logs []string
	res, err := Run(context.Background(), m, "", newReg(a), noMem, opts(1), Hooks{Log: func(l string) { logs = append(logs, l) }})
	if err != nil || res.Done != 1 || a.calls != 1 {
		t.Fatalf("res=%+v err=%v calls=%d", res, err, a.calls)
	}
	if len(logs) == 0 || !strings.Contains(logs[0], "gemini") {
		t.Fatalf("no fallback warning logged: %v", logs)
	}
}

func TestRunContextCancel(t *testing.T) {
	a := &mockAgent{name: "claude", fn: func(p string) (string, error) {
		time.Sleep(200 * time.Millisecond)
		return "ok", nil
	}}
	m := &state.TaskManifest{Tasks: []state.Task{{ID: "a", Title: "A", AssignedTo: "claude"}, {ID: "b", Title: "B", AssignedTo: "claude", Dependencies: []string{"a"}}}}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := Run(ctx, m, "", newReg(a), noMem, opts(1), Hooks{})
	if err == nil {
		t.Fatal("expected error on cancellation")
	}
	for _, task := range m.Tasks {
		if task.Status == state.TaskInProgress {
			t.Fatalf("task left in_progress after cancel: %+v", task)
		}
	}
}
