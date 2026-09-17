package state

import (
	"strings"
	"testing"
)

func TestParseTasks(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantN   int
		wantErr bool
	}{
		{"plain array", `[{"id":"a","title":"A","assigned_to":"claude","dependencies":[]}]`, 1, false},
		{"fenced json", "Here you go:\n```json\n[{\"id\":\"a\",\"title\":\"A\"}]\n```\nDone.", 1, false},
		{"fenced no lang", "```\n[{\"id\":\"a\"},{\"id\":\"b\"}]\n```", 2, false},
		{"prose with bracket before json", "Plan [v2] follows:\n[{\"id\":\"a\"}]", 1, false},
		{"empty array", `[]`, 0, true},
		{"no array", `just prose`, 0, true},
		{"garbage", `[not json`, 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tasks, err := ParseTasks(c.raw)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %d tasks", len(tasks))
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(tasks) != c.wantN {
				t.Fatalf("got %d tasks, want %d", len(tasks), c.wantN)
			}
		})
	}
}

func TestAssignIDsAndValidate(t *testing.T) {
	tasks := []Task{{Title: "no id"}, {ID: "b", Dependencies: []string{"c"}}, {ID: "c"}}
	AssignIDs(tasks)
	if tasks[0].ID == "" || tasks[0].Status != TaskPending {
		t.Fatalf("AssignIDs did not fill id/status: %+v", tasks[0])
	}
	if err := ValidateTasks(tasks); err != nil {
		t.Fatalf("valid graph rejected: %v", err)
	}

	dup := []Task{{ID: "x"}, {ID: "x"}}
	if err := ValidateTasks(dup); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate ids not detected: %v", err)
	}

	unknown := []Task{{ID: "x", Dependencies: []string{"nope"}}}
	if err := ValidateTasks(unknown); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("unknown dep not detected: %v", err)
	}

	self := []Task{{ID: "x", Dependencies: []string{"x"}}}
	if err := ValidateTasks(self); err == nil {
		t.Fatal("self dependency not detected")
	}

	cyc := []Task{{ID: "a", Dependencies: []string{"b"}}, {ID: "b", Dependencies: []string{"c"}}, {ID: "c", Dependencies: []string{"a"}}}
	if err := ValidateTasks(cyc); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("cycle not detected: %v", err)
	}
}

func TestNormalizeAssignments(t *testing.T) {
	tasks := []Task{{ID: "1", AssignedTo: "Claude"}, {ID: "2", AssignedTo: "gemini"}, {ID: "3", AssignedTo: " opencode "}}
	re := NormalizeAssignments(tasks, []string{"claude", "opencode"}, "claude")
	if tasks[0].AssignedTo != "claude" || tasks[2].AssignedTo != "opencode" {
		t.Fatalf("known agents not normalized: %+v", tasks)
	}
	if tasks[1].AssignedTo != "claude" || len(re) != 1 || re[0] != "2" {
		t.Fatalf("unknown agent not reassigned: %+v re=%v", tasks[1], re)
	}
}

func TestDepsAndBlocked(t *testing.T) {
	a := &Task{ID: "a", Status: TaskDone, Output: "A out"}
	b := &Task{ID: "b", Status: TaskBlocked}
	c := &Task{ID: "c", Dependencies: []string{"a", "b"}}
	byID := map[string]*Task{"a": a, "b": b, "c": c}
	if DepsComplete(c, byID) {
		t.Fatal("deps should not be complete")
	}
	if BlockedDep(c, byID) != "b" {
		t.Fatal("blocked dep not found")
	}
	if !strings.Contains(CollectDepOutputs(c, byID), "A out") {
		t.Fatal("dep output missing")
	}
}

func TestResetForRetry(t *testing.T) {
	m := &TaskManifest{Tasks: []Task{
		{ID: "a", Status: TaskDone},
		{ID: "b", Status: TaskBlocked, BlockReason: "x"},
		{ID: "c", Status: TaskInProgress},
	}}
	if n := ResetForRetry(m); n != 2 {
		t.Fatalf("reset %d, want 2", n)
	}
	if m.Tasks[0].Status != TaskDone || m.Tasks[1].Status != TaskPending || m.Tasks[1].BlockReason != "" || m.Tasks[2].Status != TaskPending {
		t.Fatalf("unexpected statuses: %+v", m.Tasks)
	}
}

func TestTruncate(t *testing.T) {
	if got := Truncate("héllo wörld", 5); got != "héll…" {
		t.Fatalf("got %q", got)
	}
	if got := Truncate("short", 10); got != "short" {
		t.Fatalf("got %q", got)
	}
	if got := Truncate("abc", 0); got != "" {
		t.Fatalf("got %q", got)
	}
}
