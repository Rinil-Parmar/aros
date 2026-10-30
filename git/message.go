package git

import (
	"fmt"
	"strings"

	"github.com/Rinil-Parmar/aros/state"
)

// CommitMessage builds the message for a completed work run: a one-line
// subject, then the task list with the agent that did each one.
// No trailers are added — the repo's history stays the author's own.
func CommitMessage(projectName string, tasks []state.Task) string {
	var done []state.Task
	for _, t := range tasks {
		if t.Status == state.TaskDone {
			done = append(done, t)
		}
	}

	subject := fmt.Sprintf("aros: %s — %d task(s)", projectName, len(done))
	if len(done) == 1 {
		subject = fmt.Sprintf("aros: %s — %s", projectName, state.Truncate(done[0].Title, 60))
	}

	var b strings.Builder
	b.WriteString(subject + "\n\n")
	for _, t := range done {
		agent := t.AssignedTo
		if agent == "" {
			agent = "unknown"
		}
		b.WriteString(fmt.Sprintf("- [%s] %s (%s)\n", t.ID, t.Title, agent))
	}
	var blocked []string
	for _, t := range tasks {
		if t.Status == state.TaskBlocked {
			blocked = append(blocked, t.ID)
		}
	}
	if len(blocked) > 0 {
		b.WriteString(fmt.Sprintf("\nStill blocked: %s\n", strings.Join(blocked, ", ")))
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}
