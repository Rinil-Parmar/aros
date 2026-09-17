package state

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

// ParseTasks extracts a JSON task array from raw agent output.
// It tolerates markdown fences and surrounding prose by trying every "["
// position until one decodes into a non-empty task array.
func ParseTasks(raw string) ([]Task, error) {
	raw = strings.TrimSpace(raw)

	// Prefer a fenced block if present — models usually wrap JSON in one.
	candidates := []string{}
	if fenced := extractFenced(raw); fenced != "" {
		candidates = append(candidates, fenced)
	}
	candidates = append(candidates, raw)

	var lastErr error
	for _, c := range candidates {
		tasks, err := decodeTaskArray(c)
		if err == nil {
			return tasks, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// extractFenced returns the body of the first ``` fenced block, or "".
func extractFenced(raw string) string {
	start := strings.Index(raw, "```")
	if start == -1 {
		return ""
	}
	body := raw[start+3:]
	// drop optional language tag on the opening fence line
	if nl := strings.Index(body, "\n"); nl != -1 {
		first := strings.TrimSpace(body[:nl])
		if first != "" && !strings.HasPrefix(first, "[") {
			body = body[nl+1:]
		}
	}
	if end := strings.Index(body, "```"); end != -1 {
		body = body[:end]
	}
	return strings.TrimSpace(body)
}

// decodeTaskArray tries each "[" in s as the start of the JSON array.
func decodeTaskArray(s string) ([]Task, error) {
	if !strings.Contains(s, "[") {
		return nil, fmt.Errorf("no JSON array found in output")
	}
	var lastErr error = fmt.Errorf("no JSON array found in output")
	for i := 0; i < len(s); i++ {
		if s[i] != '[' {
			continue
		}
		dec := json.NewDecoder(bytes.NewReader([]byte(s[i:])))
		var tasks []Task
		if err := dec.Decode(&tasks); err != nil {
			lastErr = fmt.Errorf("JSON parse error: %w", err)
			continue
		}
		if len(tasks) == 0 {
			lastErr = fmt.Errorf("empty task list")
			continue
		}
		return tasks, nil
	}
	return nil, lastErr
}

// AssignIDs fills in missing IDs and default status.
func AssignIDs(tasks []Task) {
	for i := range tasks {
		tasks[i].ID = strings.TrimSpace(tasks[i].ID)
		if tasks[i].ID == "" {
			tasks[i].ID = "task-" + uuid.New().String()[:8]
		}
		if tasks[i].Status == "" {
			tasks[i].Status = TaskPending
		}
	}
}

// ValidateTasks checks IDs are unique, dependencies exist, and the graph is acyclic.
// Call AssignIDs first so every task has an ID.
func ValidateTasks(tasks []Task) error {
	idSet := make(map[string]bool, len(tasks))
	for _, t := range tasks {
		if t.ID == "" {
			return fmt.Errorf("task %q has no id", t.Title)
		}
		if idSet[t.ID] {
			return fmt.Errorf("duplicate task id %q", t.ID)
		}
		idSet[t.ID] = true
	}
	for _, t := range tasks {
		for _, dep := range t.Dependencies {
			if dep == t.ID {
				return fmt.Errorf("task %q depends on itself", t.ID)
			}
			if !idSet[dep] {
				return fmt.Errorf("task %q depends on unknown task %q", t.ID, dep)
			}
		}
	}
	return DetectCycles(tasks)
}

// DetectCycles runs a DFS over the dependency graph and reports the first cycle.
func DetectCycles(tasks []Task) error {
	adj := make(map[string][]string, len(tasks))
	for _, t := range tasks {
		adj[t.ID] = t.Dependencies
	}
	const (
		unvisited = 0
		inStack   = 1
		done      = 2
	)
	visited := make(map[string]int, len(tasks))
	var dfs func(id string) error
	dfs = func(id string) error {
		switch visited[id] {
		case inStack:
			return fmt.Errorf("cycle detected at task %q", id)
		case done:
			return nil
		}
		visited[id] = inStack
		for _, dep := range adj[id] {
			if err := dfs(dep); err != nil {
				return err
			}
		}
		visited[id] = done
		return nil
	}
	for _, t := range tasks {
		if err := dfs(t.ID); err != nil {
			return err
		}
	}
	return nil
}

// NormalizeAssignments lowercases assigned_to and reassigns tasks whose agent
// is not in known to fallback. Returns the list of reassigned task IDs.
func NormalizeAssignments(tasks []Task, known []string, fallback string) []string {
	knownSet := make(map[string]bool, len(known))
	for _, k := range known {
		knownSet[strings.ToLower(k)] = true
	}
	var reassigned []string
	for i := range tasks {
		name := strings.ToLower(strings.TrimSpace(tasks[i].AssignedTo))
		if knownSet[name] {
			tasks[i].AssignedTo = name
			continue
		}
		tasks[i].AssignedTo = fallback
		reassigned = append(reassigned, tasks[i].ID)
	}
	sort.Strings(reassigned)
	return reassigned
}

// DepsComplete reports whether every dependency of t is done.
func DepsComplete(t *Task, byID map[string]*Task) bool {
	for _, dep := range t.Dependencies {
		d, ok := byID[dep]
		if !ok || d.Status != TaskDone {
			return false
		}
	}
	return true
}

// BlockedDep returns the ID of the first dependency that is blocked, or "".
func BlockedDep(t *Task, byID map[string]*Task) string {
	for _, dep := range t.Dependencies {
		if d, ok := byID[dep]; ok && d.Status == TaskBlocked {
			return dep
		}
	}
	return ""
}

// CollectDepOutputs concatenates the outputs of t's completed dependencies.
func CollectDepOutputs(t *Task, byID map[string]*Task) string {
	var parts []string
	for _, dep := range t.Dependencies {
		d, ok := byID[dep]
		if !ok || d.Output == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("=== %s [%s] ===\n%s", d.Title, d.ID, d.Output))
	}
	return strings.Join(parts, "\n\n")
}

// ResetForRetry moves blocked and in-progress tasks back to pending so a new
// work run retries them. Returns how many tasks were reset.
func ResetForRetry(m *TaskManifest) int {
	n := 0
	for i := range m.Tasks {
		t := &m.Tasks[i]
		if t.Status == TaskBlocked || t.Status == TaskInProgress {
			t.Status = TaskPending
			t.BlockReason = ""
			n++
		}
	}
	return n
}

// Truncate clips s to max runes, appending "…" if clipped. Unicode-safe.
func Truncate(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	if max == 1 {
		return "…"
	}
	return string(r[:max-1]) + "…"
}
