package tui

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

// streamLineMsg is sent by goroutines streaming agent output line by line.
type streamLineMsg struct {
	agent string
	line  string
	done  bool // true = agent finished
}

// phaseResultMsg carries the final result of a phase operation.
type phaseResultMsg struct {
	phase  string
	result string
	err    error
}

// approvalMsg requests a y/n response from the user.
type approvalMsg struct {
	question string
	onYes    func() // called when user types y
	onNo     func() // called when user types n
}

// freeInputMsg requests freeform text from the user.
type freeInputMsg struct {
	prompt   string
	callback func(text string)
}

// agentActivityMsg updates the live activity panel for a single agent.
// status: "running" | "done" | "error" | "clear" (clear resets all activity)
type agentActivityMsg struct {
	agent  string
	model  string // empty = keep existing model
	status string
	line   string // latest output line
}
