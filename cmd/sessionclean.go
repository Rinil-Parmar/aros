package cmd

import (
	"context"
	"fmt"

	"github.com/Rinil-Parmar/aros/agent"
	"github.com/Rinil-Parmar/aros/state"
)

// installSessionRecorder makes every agent call record the vendor session it
// created, so removing an Aros session can clean up after itself.
func installSessionRecorder(arosDir string) {
	agent.SetSessionSink(func(agentName, sessionID, workDir string) {
		_ = state.RecordAgentSession(arosDir, state.AgentSession{
			Agent: agentName, ID: sessionID, WorkDir: workDir,
		})
	})
}

// cleanupAgentSessions removes the vendor sessions recorded for one Aros
// session. Returns a human-readable summary, or "" when there was nothing to do.
func cleanupAgentSessions(ctx context.Context, arosDir, sessionID string) string {
	recorded, err := state.LoadAgentSessions(arosDir, sessionID)
	if err != nil || len(recorded) == 0 {
		return ""
	}
	var total agent.CleanupResult
	for _, rec := range recorded {
		total.Merge(agent.CleanupSession(ctx, agent.VendorSession{Agent: rec.Agent, ID: rec.ID}))
	}
	out := fmt.Sprintf("%s (of %d recorded)", total.Summary(), len(recorded))
	for _, w := range dedupe(total.Warnings) {
		out += "\n  note: " + w
	}
	return out
}

// dedupe collapses repeated warnings — the copilot note would otherwise appear
// once per session removed.
func dedupe(in []string) []string {
	seen := make(map[string]bool, len(in))
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
