package tui

import (
	"github.com/Rinil-Parmar/aros/agent"
	"github.com/Rinil-Parmar/aros/config"
	"github.com/Rinil-Parmar/aros/memory"
	"github.com/Rinil-Parmar/aros/state"
)

// msgKind classifies a chat message for display.
type msgKind int

const (
	kindSystem  msgKind = iota // aros orchestrator messages
	kindAgent                  // output from an AI agent
	kindHuman                  // user input echoed back
	kindSuccess                // phase completion
	kindError                  // error messages
	kindBanner                 // welcome banner — re-rendered on resize
)

// ChatMessage is one entry in the conversation log.
// Body stores raw text (no ANSI); rendered fresh in refreshViewport() with current width.
type ChatMessage struct {
	Kind      msgKind
	AgentName string // set when Kind == kindAgent
	ModelName string // set when Kind == kindAgent (shown in header)
	Body      string // raw text — no pre-rendered ANSI
}

// --- bubbletea msg types (internal events) ---
//
// Rule: background goroutines communicate with the model ONLY through these
// messages (via send()). They never touch Model fields. Conversely, code that
// already runs inside Update must never call send() — bubbletea's message
// channel is unbuffered and Update runs on the receiving goroutine, so that
// deadlocks the UI. Update-side code mutates the model directly instead.

// bootstrapMsg carries everything loaded at startup.
type bootstrapMsg struct {
	cfg         *config.Config
	reg         agent.Registry
	warnings    []string
	mem         *memory.SecondMem
	project     *state.ProjectState
	sessionID   string
	sessionName string
	arosDir     string
	cwd         string
	err         error
}

// streamLineMsg is sent by goroutines streaming agent output line by line.
type streamLineMsg struct {
	gen   int
	agent string
	line  string
}

// Generation gating: every phase run and every chat is tagged with a
// generation drawn from one monotonic sequence, so phase and chat generations
// can never collide. The model drops any message whose generation is no longer
// live. Without this, late messages from an aborted or superseded run flip
// busy/mode and repopulate the activity panel under the run that replaced it —
// which is how the UI ended up wedged between modes.
// gen == 0 means "not owned by a run" and is always accepted.

// phaseResultMsg carries the final result of a phase operation.
// project, when non-nil, replaces the model's project (goroutines never mutate it directly).
type phaseResultMsg struct {
	gen     int
	phase   string
	err     error
	project *state.ProjectState
}

// approvalMsg requests a y/n response from the user.
type approvalMsg struct {
	gen      int
	question string
	onYes    func() // run in a goroutine when user types y
	onNo     func() // run in a goroutine when user types n
}

// freeInputMsg requests freeform text from the user.
type freeInputMsg struct {
	gen      int
	prompt   string
	callback func(text string) // run in a goroutine
}

// chatDoneMsg signals that a judge chat finished.
type chatDoneMsg struct {
	gen int
	err error
}

// manifestChangedMsg tells the model to reload its cached task manifest.
type manifestChangedMsg struct{}

// agentActivityMsg updates the live activity panel for a single agent.
// status: "running" | "done" | "error" | "clear" (clear resets all activity)
type agentActivityMsg struct {
	gen    int
	agent  string
	model  string // empty = keep existing model
	status string
	line   string // latest output line
}
