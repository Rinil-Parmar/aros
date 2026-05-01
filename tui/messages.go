package tui

// msgKind classifies a chat message for display.
type msgKind int

const (
	kindSystem  msgKind = iota // aros orchestrator messages
	kindAgent                  // output from an AI agent
	kindHuman                  // user input echoed back
	kindSuccess                // phase completion
	kindError                  // error messages
	kindStream                 // streaming agent line (appended to last agent msg)
)

// ChatMessage is one entry in the conversation log.
type ChatMessage struct {
	Kind      msgKind
	AgentName string // set when Kind == kindAgent or kindStream
	Text      string
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

// inputModeMsg switches the input box label/placeholder.
type inputModeMsg struct {
	placeholder string
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
