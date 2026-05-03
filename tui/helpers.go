package tui

import (
	"encoding/json"
	"fmt"
	"unicode/utf8"
	"strings"

	"github.com/Rinil-Parmar/aros/state"
	"github.com/google/uuid"
)

func parseTasks(raw string) ([]state.Task, error) {
	raw = strings.TrimSpace(raw)
	if idx := strings.Index(raw, "```"); idx != -1 {
		raw = raw[idx:]
		raw = strings.TrimPrefix(raw, "```json")
		raw = strings.TrimPrefix(raw, "```")
		if end := strings.Index(raw, "```"); end != -1 {
			raw = raw[:end]
		}
		raw = strings.TrimSpace(raw)
	}
	start := strings.Index(raw, "[")
	end := strings.LastIndex(raw, "]")
	if start == -1 || end == -1 || end <= start {
		return nil, fmt.Errorf("no JSON array found")
	}
	raw = raw[start : end+1]
	var tasks []state.Task
	if err := json.Unmarshal([]byte(raw), &tasks); err != nil {
		return nil, fmt.Errorf("JSON parse: %w", err)
	}
	if len(tasks) == 0 {
		return nil, fmt.Errorf("empty task list")
	}
	return tasks, nil
}

func detectCycles(tasks []state.Task) error {
	idSet := make(map[string]bool)
	for _, t := range tasks {
		idSet[t.ID] = true
	}
	for _, t := range tasks {
		for _, dep := range t.Dependencies {
			if !idSet[dep] {
				return fmt.Errorf("task %q depends on unknown task %q", t.ID, dep)
			}
		}
	}
	adj := make(map[string][]string)
	for _, t := range tasks {
		adj[t.ID] = t.Dependencies
	}
	visited := make(map[string]int)
	var dfs func(id string) error
	dfs = func(id string) error {
		if visited[id] == 1 {
			return fmt.Errorf("cycle at task %q", id)
		}
		if visited[id] == 2 {
			return nil
		}
		visited[id] = 1
		for _, dep := range adj[id] {
			if err := dfs(dep); err != nil {
				return err
			}
		}
		visited[id] = 2
		return nil
	}
	for _, t := range tasks {
		if err := dfs(t.ID); err != nil {
			return err
		}
	}
	return nil
}

func assignIDs(tasks []state.Task) {
	for i := range tasks {
		if tasks[i].ID == "" {
			tasks[i].ID = "task-" + uuid.New().String()[:8]
		}
		if tasks[i].Status == "" {
			tasks[i].Status = state.TaskPending
		}
	}
}

func depsComplete(t *state.Task, byID map[string]*state.Task) bool {
	for _, dep := range t.Dependencies {
		d, ok := byID[dep]
		if !ok || d.Status != state.TaskDone {
			return false
		}
	}
	return true
}

func collectDepOutputs(task *state.Task, byID map[string]*state.Task) string {
	var parts []string
	for _, dep := range task.Dependencies {
		d, ok := byID[dep]
		if !ok || d.Output == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("=== %s [%s] ===\n%s", d.Title, d.ID, d.Output))
	}
	return strings.Join(parts, "\n\n")
}

// truncate clips s to maxRunes runes, appending "…" if clipped.
// Uses rune count (not byte count) so multi-byte Unicode is safe.
func truncate(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	n := utf8.RuneCountInString(s)
	if n <= maxRunes {
		return s
	}
	runes := []rune(s)
	return string(runes[:maxRunes-1]) + "…"
}
