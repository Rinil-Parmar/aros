package tui

import (
	"context"

	"github.com/Rinil-Parmar/aros/agent"
	"github.com/Rinil-Parmar/aros/config"
	"github.com/Rinil-Parmar/aros/memory"
	"github.com/Rinil-Parmar/aros/state"
)

// phaseRun is an immutable snapshot handed to a phase goroutine so it never
// reads Model fields while Update may be writing them.
type phaseRun struct {
	gen     int // generation this run belongs to; stale results are dropped
	ctx     context.Context
	cfg     *config.Config
	reg     agent.Registry
	mem     *memory.SecondMem
	arosDir string
	project state.ProjectState // copy
}

// truncate clips s to maxRunes runes, appending "…" if clipped. Unicode-safe.
func truncate(s string, maxRunes int) string {
	return state.Truncate(s, maxRunes)
}
