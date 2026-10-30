package tui

import (
	"context"
	"fmt"
	"time"

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

// cleanupAgentSessionsTUI deletes the given vendor sessions and returns a
// one-line summary. Runs off the event loop: it shells out to other tools.
func cleanupAgentSessionsTUI(_ string, recorded []state.AgentSession) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	var total agent.CleanupResult
	for _, rec := range recorded {
		total.Merge(agent.CleanupSession(ctx, agent.VendorSession{Agent: rec.Agent, ID: rec.ID}))
	}
	out := fmt.Sprintf("%s (of %d recorded)", total.Summary(), len(recorded))
	seen := map[string]bool{}
	for _, w := range total.Warnings {
		if !seen[w] {
			seen[w] = true
			out += "\n  note: " + w
		}
	}
	return out
}
