package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Rinil-Parmar/aros/agent"
	"github.com/Rinil-Parmar/aros/config"
	"github.com/Rinil-Parmar/aros/memory"
	"github.com/Rinil-Parmar/aros/state"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type inputMode int

const (
	modeIdle inputMode = iota
	modeText
	modeApproval
)

// agentStatus tracks live state for one agent in the activity panel.
type agentStatus struct {
	model  string
	status string // "running" | "done" | "error"
	line   string // latest output line (already truncated)
}

type Model struct {
	width, height int

	viewport viewport.Model
	textarea textarea.Model
	spinner  spinner.Model

	messages []ChatMessage
	mode     inputMode
	prompt   string

	onYes      func()
	onNo       func()
	onFreeText func(string)

	cfgFile string
	cfg     *config.Config
	reg     agent.Registry
	mem     *memory.SecondMem
	project *state.ProjectState
	arosDir string
	cwd     string
	busy    bool

	// phaseCancel aborts the running phase's agent subprocesses (Esc, /phase --force).
	phaseCancel context.CancelFunc
	chatCancel  context.CancelFunc

	// phaseGen/chatGen identify the current run. Both are drawn from genSeq —
	// one monotonic sequence — so a retired phase generation can never collide
	// with a live chat generation. Messages tagged with a generation that is no
	// longer live are dropped.
	genSeq   int
	phaseGen int
	chatGen  int

	// initializing is true while the first session is being created, so a
	// second line of input cannot create a duplicate project.
	initializing bool

	// manifest is a cached copy of the active session's task list for the
	// right panel — reloaded on manifestChangedMsg, never read from disk in View().
	manifest *state.TaskManifest

	activity         map[string]*agentStatus
	approvalQuestion string
	sessionID        string
	sessionName      string
	chatInProgress   bool // true while a judge chat is running

	// layout cache — computed by recalcLayout, used by view.go
	leftW  int
	rightW int
	colH   int

	quitting bool
}

// knownModels lists well-known model strings per agent type, shown as suggestions.
var knownModels = map[string][]string{
	"claude": {
		"haiku",
		"sonnet",
		"opus",
	},
	"opencode": {
		"openai/gpt-5.4-mini",
		"openai/gpt-5.4",
		"anthropic/claude-sonnet-4-5",
		"anthropic/claude-haiku-4-5",
		"google/gemini-2.5-flash",
		"google/gemini-2.5-pro",
		"(run `opencode models` for the full list)",
	},
	"copilot": {
		"auto",
		"gpt-5.4",
		"claude-sonnet-4.5",
		"(run `copilot --help` for the full list)",
	},
}

var knownDenseLevels = []string{"off", "lite", "full", "ultra"}

// program is read by send() from many goroutines and written by SetProgram, so
// it is stored atomically.
var program atomic.Pointer[tea.Program]

func SetProgram(p *tea.Program) { program.Store(p) }

// send delivers a message to the event loop. Only call from goroutines —
// never from inside Update (see messages.go).
func send(msg tea.Msg) {
	if p := program.Load(); p != nil {
		p.Send(msg)
	}
}

// New builds the TUI model. cfgFile overrides the global config path ("" = default).
func New(cfgFile string) *Model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(colorBrand)

	ta := textarea.New()
	ta.Placeholder = "Type a command (try: help)"
	ta.Prompt = ""
	ta.CharLimit = 0
	ta.SetHeight(1)
	ta.SetWidth(80)
	ta.ShowLineNumbers = false
	ta.KeyMap.InsertNewline.SetEnabled(true) // Shift+Enter inserts newline

	// Clear all default gray/white backgrounds — pure transparent input
	noBase := lipgloss.NewStyle().Background(lipgloss.NoColor{})
	ta.FocusedStyle.Base = noBase
	ta.BlurredStyle.Base = noBase
	ta.FocusedStyle.CursorLine = noBase
	ta.BlurredStyle.CursorLine = noBase
	ta.FocusedStyle.Text = lipgloss.NewStyle().Foreground(colorText)
	ta.BlurredStyle.Text = lipgloss.NewStyle().Foreground(colorSubtle)
	ta.FocusedStyle.Placeholder = lipgloss.NewStyle().Foreground(colorMuted).Italic(true)
	ta.BlurredStyle.Placeholder = lipgloss.NewStyle().Foreground(colorMuted).Italic(true)
	ta.FocusedStyle.Prompt = lipgloss.NewStyle().Foreground(colorBrand)
	ta.BlurredStyle.Prompt = lipgloss.NewStyle().Foreground(colorMuted)
	ta.Cursor.Style = lipgloss.NewStyle().Foreground(colorBrand)
	ta.Focus()

	vp := viewport.New(80, 20)
	vp.MouseWheelEnabled = true

	return &Model{
		cfgFile:  cfgFile,
		spinner:  sp,
		textarea: ta,
		viewport: vp,
		mode:     modeText,
		activity: make(map[string]*agentStatus),
	}
}

func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, textarea.Blink, bootstrap(m.cfgFile))
}

// bootstrap loads config, agents and project state off the event loop and
// hands everything back as one message (it never touches the Model).
func bootstrap(cfgFile string) tea.Cmd {
	return func() tea.Msg {
		cwd, _ := os.Getwd()
		out := bootstrapMsg{cwd: cwd, arosDir: filepath.Join(cwd, ".aros")}

		cfg, err := config.Load(cfgFile)
		if err != nil {
			out.err = fmt.Errorf("config: %w", err)
			return out
		}
		out.cfg = cfg
		out.mem = memory.New(cfg.SecondMem.Binary, cfg.SecondMem.Enabled)

		reg, warnings, regErr := agent.BuildRegistry(cfg, cwd)
		out.warnings = warnings
		if regErr != nil {
			out.warnings = append(out.warnings, regErr.Error())
		} else {
			out.reg = reg
		}

		if s, err := state.LoadState(out.arosDir); err == nil {
			out.project = s
			if meta, err := state.ActiveSession(out.arosDir); err == nil {
				out.sessionID = meta.ID
				out.sessionName = meta.Name
			}
		}
		return out
	}
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {

	case tea.KeyMsg:
		key := msg.String()

		// Always-active quit
		if key == "ctrl+c" {
			m.cancelAll()
			m.quitting = true
			return m, tea.Quit
		}

		// Scroll keys: route to viewport
		switch key {
		case "pgup", "pgdown", "ctrl+u", "home", "end":
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
		case "shift+tab", "backtab":
			m.cyclePhase()
			return m, nil
		case "shift+up":
			m.viewport.LineUp(1)
			return m, nil
		case "shift+down":
			m.viewport.LineDown(1)
			return m, nil
		}

		// Universal escape hatch: abort whatever is running / pending and return
		// to a clean idle state. Never leaves the UI wedged between modes.
		switch key {
		case "esc", "ctrl+g":
			if m.cancelAll() {
				m.addSystem("Cancelled.")
			} else if m.textarea.Value() != "" {
				m.textarea.Reset()
			} else {
				m.addSystem("Nothing to cancel.")
			}
			m.recalcLayout()
			return m, nil
		}

		// Ctrl shortcuts
		switch key {
		case "ctrl+h":
			m.showHelp()
			return m, nil
		case "ctrl+k":
			m.messages = nil
			m.refreshViewport()
			return m, nil
		case "ctrl+s":
			m.showStatus()
			return m, nil
		}

		// Approval mode accepts a bare y/n keypress (no Enter) when nothing is typed yet.
		if m.mode == modeApproval && m.textarea.Value() == "" {
			switch strings.ToLower(key) {
			case "y", "n":
				return m, m.handleInput(strings.ToLower(key))
			}
		}

		// Enter submits; Shift+Enter / Alt+Enter handled by textarea (newline)
		if msg.Type == tea.KeyEnter && !msg.Alt {
			text := strings.TrimSpace(m.textarea.Value())
			m.textarea.Reset()
			if text != "" {
				cmds = append(cmds, m.handleInput(text))
			}
			return m, tea.Batch(cmds...)
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.textarea.SetWidth(msg.Width - 8)
		m.recalcLayout()
		m.refreshViewport()

	case tea.MouseMsg:
		if msg.Action == tea.MouseActionPress {
			switch msg.Button {
			case tea.MouseButtonWheelUp:
				m.viewport.LineUp(3)
				return m, nil
			case tea.MouseButtonWheelDown:
				m.viewport.LineDown(3)
				return m, nil
			}
		}

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		cmds = append(cmds, cmd)

	case bootstrapMsg:
		m.cwd = msg.cwd
		m.arosDir = msg.arosDir
		m.cfg = msg.cfg
		m.reg = msg.reg
		m.mem = msg.mem
		m.project = msg.project
		m.sessionID = msg.sessionID
		m.sessionName = msg.sessionName
		m.reloadManifest()
		m.showWelcome()
		if msg.err != nil {
			m.addError(msg.err.Error())
			m.addSystem("Fix the config (aros config show) and restart.")
		}
		for _, w := range msg.warnings {
			m.addSystem("warning: " + w)
		}

	case phaseResultMsg:
		if !m.live(msg.gen) {
			break // stale: from an aborted or superseded run
		}
		if msg.project != nil {
			m.project = msg.project
			m.reloadManifest()
		}
		if msg.phase == "work_started" {
			break // phase still running
		}
		m.busy = false
		m.phaseCancel = nil
		m.initializing = false
		if msg.err != nil {
			m.addError(msg.err.Error())
			m.clearActivity()
			m.setMode(modeText, "")
		} else {
			m.handlePhaseResult(msg)
		}

	case streamLineMsg:
		if !m.live(msg.gen) {
			break
		}
		m.appendStream(msg.agent, msg.line)

	case approvalMsg:
		if !m.live(msg.gen) {
			break
		}
		m.onYes = msg.onYes
		m.onNo = msg.onNo
		m.approvalQuestion = msg.question
		m.setMode(modeApproval, "y/n")
		m.recalcLayout()

	case freeInputMsg:
		if !m.live(msg.gen) {
			break
		}
		m.busy = false // allow Enter so user can submit feedback
		m.onFreeText = msg.callback
		m.addSystem(msg.prompt)
		m.setMode(modeText, msg.prompt)

	case chatDoneMsg:
		if !m.live(msg.gen) {
			break // stale: the chat was cancelled or replaced
		}
		m.chatInProgress = false
		m.chatCancel = nil
		if msg.err != nil {
			m.addError(msg.err.Error())
		}
		m.clearActivity()

	case manifestChangedMsg:
		m.reloadManifest()

	case planRequestMsg:
		m.busy = false // set by the free-text handoff
		m.setMode(modeText, "")
		if m.rejectIfBusy() || !m.ready() {
			break
		}
		pr := m.newPhaseRun()
		safeGo(pr.gen, func() { runPlan(pr, msg.task) })

	case sessionNewMsg:
		m.busy = false
		m.setMode(modeText, "")
		if err := m.createSession(msg.name); err != nil {
			m.addError(err.Error())
		}

	case sessionLoadedMsg:
		m.initializing = false
		m.project = msg.project
		m.sessionID = msg.sessionID
		m.sessionName = msg.sessionName
		m.reloadManifest()
		m.handlePhaseResult(phaseResultMsg{phase: "init"})

	case agentActivityMsg:
		if !m.live(msg.gen) {
			break // stale: an aborted run must not repopulate the activity panel
		}
		m.setActivity(msg)
	}

	// Track textarea height before update to detect growth/shrink
	prevTAH := m.textarea.Height()
	var taCmd tea.Cmd
	m.textarea, taCmd = m.textarea.Update(msg)
	cmds = append(cmds, taCmd)
	if m.textarea.Height() != prevTAH {
		m.recalcLayout()
		m.refreshViewport()
	}

	return m, tea.Batch(cmds...)
}

// ── Activity panel (Update-side; goroutines use send(agentActivityMsg)) ────────

func (m *Model) setActivity(msg agentActivityMsg) {
	if msg.status == "clear" {
		m.clearActivity()
		return
	}
	prev, exists := m.activity[msg.agent]
	if !exists {
		prev = &agentStatus{}
		m.activity[msg.agent] = prev
	}
	if msg.model != "" {
		prev.model = msg.model
	}
	if msg.status != "" {
		prev.status = msg.status
	}
	if msg.line != "" {
		prev.line = truncate(msg.line, 50)
	}
	if !exists {
		m.recalcLayout()
	}
}

func (m *Model) clearActivity() {
	m.activity = make(map[string]*agentStatus)
	m.recalcLayout()
}

// reloadManifest refreshes the cached task list from disk.
func (m *Model) reloadManifest() {
	m.manifest = nil
	if m.project == nil || m.arosDir == "" {
		return
	}
	if mf, err := state.LoadManifest(m.arosDir); err == nil {
		m.manifest = mf
	}
}

// ready reports whether config/agents loaded; otherwise explains why not.
func (m *Model) ready() bool {
	if m.cfg == nil {
		m.addError("configuration failed to load — fix ~/.aros/config.toml and restart")
		return false
	}
	if len(m.reg) == 0 {
		m.addError("no agents available — install claude, opencode or copilot and ensure they are on PATH")
		return false
	}
	return true
}

// nextGen returns a generation number that has never been used before.
func (m *Model) nextGen() int {
	m.genSeq++
	return m.genSeq
}

// live reports whether a message's generation still belongs to a current run.
// gen 0 means the message is not owned by a run (system messages).
func (m *Model) live(gen int) bool {
	return gen == 0 || gen == m.phaseGen || gen == m.chatGen
}

// newPhaseRun snapshots everything a phase goroutine needs, bumps the phase
// generation and marks the model busy.
func (m *Model) newPhaseRun() phaseRun {
	m.stopPhase() // cancel anything still running from a previous generation
	ctx, cancel := context.WithCancel(context.Background())
	m.phaseCancel = cancel
	m.phaseGen = m.nextGen()
	m.busy = true
	pr := phaseRun{gen: m.phaseGen, ctx: ctx, cfg: m.cfg, reg: m.reg, mem: m.mem, arosDir: m.arosDir}
	if m.project != nil {
		pr.project = *m.project
	}
	return pr
}

// stopPhase cancels the running phase (killing agent subprocesses) and retires
// its generation so late messages from it are ignored.
func (m *Model) stopPhase() {
	if m.phaseCancel != nil {
		m.phaseCancel()
		m.phaseCancel = nil
	}
	m.phaseGen = m.nextGen() // retire: nothing in flight carries this number
	m.busy = false
	m.onYes, m.onNo, m.onFreeText = nil, nil, nil
	m.approvalQuestion = ""
}

// stopChat cancels an in-flight chat and retires its generation.
func (m *Model) stopChat() {
	if m.chatCancel != nil {
		m.chatCancel()
		m.chatCancel = nil
	}
	m.chatGen = m.nextGen() // retire
	m.chatInProgress = false
}

// cancelAll aborts everything in flight and returns the UI to a clean, usable
// idle state. This is the universal escape hatch (Esc / Ctrl+G / --force).
// Returns false if there was nothing to cancel.
func (m *Model) cancelAll() bool {
	had := m.busy || m.chatInProgress || m.mode == modeApproval || m.onFreeText != nil
	m.stopPhase()
	m.stopChat()
	m.clearActivity()
	m.setMode(modeText, "")
	return had
}

func (m *Model) isBusy() bool { return m.busy || m.chatInProgress }

func (m *Model) rejectIfBusy() bool {
	if m.isBusy() {
		m.addSystem("Busy — wait for the current operation to finish, or use /phase <phase> --force to abort it.")
		return true
	}
	return false
}

func (m *Model) handleInput(text string) tea.Cmd {
	m.addHuman(text)

	// Slash commands always work — /phase --force must be able to abort a
	// phase that is waiting on an approval or a question.
	if strings.HasPrefix(text, "/") {
		return m.handleSlash(text)
	}

	switch m.mode {
	case modeApproval:
		lower := strings.ToLower(text)
		if lower == "y" || lower == "yes" || lower == "n" || lower == "no" {
			cbYes := m.onYes
			cbNo := m.onNo
			m.approvalQuestion = ""
			m.onYes = nil
			m.onNo = nil
			m.setMode(modeIdle, "")
			m.recalcLayout()
			// Callbacks may run agents or call send(); they must run off the event loop.
			if lower == "y" || lower == "yes" {
				if cbYes != nil {
					safeGo(m.phaseGen, cbYes)
				}
			} else if cbNo != nil {
				safeGo(m.phaseGen, cbNo)
			}
			return nil
		}
		m.addSystem("Please answer y or n.")
		return nil

	case modeText, modeIdle:
		if m.onFreeText != nil {
			cb := m.onFreeText
			m.onFreeText = nil
			m.busy = true
			m.setMode(modeIdle, "")
			safeGo(m.phaseGen, func() { cb(text) })
			return nil
		}
		return m.dispatch(text)
	}
	return nil
}

func (m *Model) dispatch(text string) tea.Cmd {
	if m.project == nil {
		if m.initializing {
			m.addSystem("Creating the project — one moment.")
			return nil
		}
		m.initializing = true
		return m.initProject(text)
	}

	lower := strings.ToLower(text)
	switch {
	case lower == "help":
		m.showHelp()
	case lower == "status":
		m.showStatus()
	case lower == "divide":
		if m.rejectIfBusy() || !m.ready() {
			return nil
		}
		pr := m.newPhaseRun()
		safeGo(pr.gen, func() { runDivide(pr) })
	case lower == "work":
		if m.rejectIfBusy() || !m.ready() {
			return nil
		}
		pr := m.newPhaseRun()
		safeGo(pr.gen, func() { runWork(pr) })
	case lower == "plan" || strings.HasPrefix(lower, "plan "):
		if m.rejectIfBusy() || !m.ready() {
			return nil
		}
		task := strings.TrimSpace(text[len("plan"):])
		if task == "" {
			m.onFreeText = func(t string) {
				// runs in a goroutine; hand off to a fresh phase run via the event loop
				send(planRequestMsg{task: t})
			}
			m.addSystem("What do you want to build?")
			m.setMode(modeText, "task")
		} else {
			pr := m.newPhaseRun()
			safeGo(pr.gen, func() { runPlan(pr, task) })
		}
	default:
		return m.startChat(text)
	}
	return nil
}

// planRequestMsg starts a plan for a task entered via the free-text prompt.
type planRequestMsg struct{ task string }

func (m *Model) handleSlash(text string) tea.Cmd {
	parts := strings.Fields(text)
	cmd := strings.ToLower(parts[0])

	switch cmd {
	case "/help":
		m.showHelp()

	case "/quit", "/exit":
		m.cancelAll()
		m.quitting = true
		return tea.Quit

	case "/agents":
		m.showAgents()

	case "/status":
		m.showStatus()

	case "/clear":
		m.messages = nil
		m.refreshViewport()

	case "/model":
		if m.cfg == nil {
			m.addError("configuration not loaded")
			return nil
		}
		if len(parts) == 1 {
			m.addSystem("Usage: /model <agent> <model>")
			m.addSystem("")
			for _, agentName := range m.agentNames() {
				m.addSystem(fmt.Sprintf("  %s: %s", agentName, m.cfg.Agents[agentName].Model))
				for _, mdl := range knownModels[agentName] {
					m.addSystem("    " + mdl)
				}
			}
			return nil
		}
		agentName := strings.ToLower(parts[1])
		if len(parts) == 2 {
			if models, ok := knownModels[agentName]; ok {
				m.addSystem(fmt.Sprintf("Models for %s:", agentName))
				for _, mdl := range models {
					m.addSystem("  " + mdl)
				}
				m.addSystem(fmt.Sprintf("Usage: /model %s <model>", agentName))
			} else {
				m.addError(fmt.Sprintf("Unknown agent %q. Usage: /model <agent> <model>", agentName))
			}
			return nil
		}
		if m.rejectIfBusy() {
			return nil
		}
		modelName := strings.Join(parts[2:], " ")
		if err := m.setAgentModel(agentName, modelName); err != nil {
			m.addError(err.Error())
			return nil
		}
		m.addSuccess(fmt.Sprintf("Set %s model → %s  (this session only; persist with: aros config set agents.%s.model %s)", agentName, modelName, agentName, modelName))

	case "/dense":
		if m.cfg == nil {
			m.addError("configuration not loaded")
			return nil
		}
		if len(parts) == 1 {
			m.addSystem("Usage: /dense <agent> <level>")
			m.addSystem("Levels: " + strings.Join(knownDenseLevels, ", "))
			m.addSystem("")
			for _, agentName := range m.agentNames() {
				level := m.cfg.Agents[agentName].Dense
				if level == "" {
					level = "off"
				}
				m.addSystem(fmt.Sprintf("  %s: %s", agentName, level))
			}
			return nil
		}
		if len(parts) == 2 {
			m.addSystem(fmt.Sprintf("Usage: /dense %s <level>", parts[1]))
			m.addSystem("Levels: " + strings.Join(knownDenseLevels, ", "))
			return nil
		}
		if m.rejectIfBusy() {
			return nil
		}
		agentName, level := strings.ToLower(parts[1]), strings.ToLower(parts[2])
		if err := m.setAgentDense(agentName, level); err != nil {
			m.addError(err.Error())
			return nil
		}
		m.addSuccess(fmt.Sprintf("%s dense mode → %s", agentName, level))

	case "/judge":
		if m.cfg == nil {
			m.addError("configuration not loaded")
			return nil
		}
		if len(parts) < 2 {
			m.addError("Usage: /judge <agent>   e.g. /judge claude")
			return nil
		}
		if m.rejectIfBusy() {
			return nil
		}
		name := strings.ToLower(parts[1])
		if err := m.setJudge(name); err != nil {
			m.addError(err.Error())
			return nil
		}
		m.addSuccess("Judge agent → " + name)

	case "/session":
		if len(parts) == 1 {
			m.addSystem("Usage: /session <new|list|use|rm> [args]")
			return nil
		}
		sub := strings.ToLower(parts[1])
		if sub == "list" {
			m.showSessions()
			return nil
		}
		force := hasForceFlag(parts)
		if m.isBusy() && !force {
			m.addError("Cannot change session while a command is running. Use --force to abort it.")
			return nil
		}
		if m.isBusy() && force {
			m.addSystem("Aborting the running operation — tasks in flight will be marked blocked.")
			m.cancelAll()
		}
		switch sub {
		case "new":
			name := strings.Join(stripFlags(parts[2:]), " ")
			if name == "" {
				m.onFreeText = func(t string) { send(sessionNewMsg{name: t}) }
				m.addSystem("Session name?")
				m.setMode(modeText, "session name")
				return nil
			}
			if err := m.createSession(name); err != nil {
				m.addError(err.Error())
			}
		case "use":
			id := firstArg(parts[2:])
			if id == "" {
				m.addError("Usage: /session use <id>")
				return nil
			}
			if err := m.useSession(id); err != nil {
				m.addError(err.Error())
			}
		case "rm":
			id := firstArg(parts[2:])
			if id == "" {
				m.addError("Usage: /session rm <id> [--force]")
				return nil
			}
			if err := m.removeSession(id, force); err != nil {
				m.addError(err.Error())
			}
		default:
			m.addError("Usage: /session <new|list|use|rm> [args]")
		}
		return nil

	case "/phase":
		if len(parts) < 2 {
			m.addError("Usage: /phase <init|plan|divide|work|done> [--force]")
			return nil
		}
		force := hasForceFlag(parts)
		if m.isBusy() && !force {
			m.addError("Cannot change phase while a command is running. Use --force to abort it.")
			return nil
		}
		if m.isBusy() && force {
			m.addSystem("Aborting the running operation — tasks in flight will be marked blocked.")
			m.cancelAll()
		}
		if err := m.setPhase(parts[1]); err != nil {
			m.addError(err.Error())
		}
		return nil

	default:
		m.addSystem("Unknown slash command. Try /help")
	}
	return nil
}

// sessionNewMsg creates a session named via the free-text prompt.
type sessionNewMsg struct{ name string }

func (m *Model) handlePhaseResult(msg phaseResultMsg) {
	switch msg.phase {
	case "init":
		m.addSuccess("Project initialized: " + m.project.ProjectName)
		m.showHelp()
	case "plan_done":
		m.addSuccess("Plan approved.")
		m.addSystem("  → type: divide    (break plan into agent tasks)")
		m.clearActivity()
	case "divide_done":
		m.addSuccess("Tasks assigned.")
		m.addSystem("  → type: work    (execute all tasks)")
		m.clearActivity()
	case "work_done":
		m.addSuccess("All tasks complete!")
		m.addSystem("  → status                    review results")
		m.addSystem("  → /session new <name>       start a new project")
		m.addSystem("  → /phase init               reset this session")
		m.clearActivity()
	default:
		m.clearActivity()
	}
	m.setMode(modeText, "")
}

func (m *Model) showWelcome() {
	w := m.leftW
	if w == 0 {
		w = m.width
	}
	if w == 0 {
		w = 80
	}

	bannerText := `   █████╗ ██████╗  ██████╗ ███████╗     ██████╗██╗     ██╗
  ██╔══██╗██╔══██╗██╔═══██╗██╔════╝    ██╔════╝██║     ██║
  ███████║██████╔╝██║   ██║███████╗    ██║     ██║     ██║
  ██╔══██║██╔══██╗██║   ██║╚════██║    ██║     ██║     ██║
  ██║  ██║██║  ██║╚██████╔╝███████║    ╚██████╗███████╗██║
  ╚═╝  ╚═╝╚═╝  ╚═╝ ╚═════╝ ╚══════╝     ╚═════╝╚══════╝╚═╝`

	// Store as kindBanner so refreshViewport re-renders it with current width on resize
	m.messages = append(m.messages, ChatMessage{Kind: kindBanner, Body: bannerText})
	m.addSystem("Multi-agent AI orchestrator  •  v0.1.0")
	m.addSystem(strings.Repeat("─", min(w-2, 65)))

	if m.project != nil {
		m.addSystem(fmt.Sprintf("Resuming: %s  [phase: %s]", m.project.ProjectName, m.project.Phase))
		m.showAgents()
		m.showHelp()
		m.setMode(modeText, "")
		return
	}

	m.addSystem("No project found here. Enter a project name to start:")
	m.setMode(modeText, "project name")
}

func (m *Model) showHelp() {
	m.addSystem("Commands:")
	for _, l := range []string{
		"  plan <task>      generate multi-agent plan",
		"  divide           break plan into tasks",
		"  work             execute tasks (re-run to retry blocked tasks)",
		"  status           current state and tasks",
		"",
		"Slash commands:",
		"  /model <agent> <model>     change model for an agent (this session)",
		"  /dense <agent> <level>     set dense output (off|lite|full|ultra)",
		"  /judge <agent>             change which agent is the judge",
		"  /agents                    show all configured agents",
		"  /session <new|list|use|rm>  manage sessions",
		"  /phase <phase> [--force]    set phase (init|plan|divide|work|done); --force aborts a running phase",
		"  /clear                     clear chat history",
		"  /help, /quit",
		"",
		"Esc or Ctrl+G        cancel whatever is running and return to idle",
		"",
		"Phase shortcut: Shift+Tab (cycle init → plan → divide → work → done)",
		"Scrolling: pgup/pgdn  •  shift+up/down  •  mouse wheel",
		"Multi-line input: Shift+Enter",
		"",
		"Any other text chats with the judge agent.",
	} {
		m.addSystem(l)
	}
}

func (m *Model) cyclePhase() {
	if m.project == nil {
		m.addSystem("No project loaded.")
		return
	}
	if m.isBusy() {
		m.addSystem("Cannot change phase while a command is running.")
		return
	}

	next := state.KnownPhases[0]
	for i, p := range state.KnownPhases {
		if m.project.Phase == p {
			next = state.KnownPhases[(i+1)%len(state.KnownPhases)]
			break
		}
	}

	m.project.Phase = next
	if err := state.SaveState(m.arosDir, m.project); err != nil {
		m.addError("could not save phase: " + err.Error())
		return
	}
	m.addSuccess("Phase changed → " + string(next))
}

// agentNames returns configured agent names in sorted order.
func (m *Model) agentNames() []string {
	if m.cfg == nil {
		return nil
	}
	names := make([]string, 0, len(m.cfg.Agents))
	for n := range m.cfg.Agents {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func (m *Model) showAgents() {
	if m.cfg == nil {
		return
	}
	m.addSystem(fmt.Sprintf("Judge: %s", m.cfg.Judge.Agent))
	m.addSystem("Agents:")
	for _, name := range m.agentNames() {
		ac := m.cfg.Agents[name]
		status := "off"
		if ac.Enabled {
			status = "on"
			if _, ok := m.reg[name]; !ok {
				status = "missing"
			}
		}
		dense := ac.Dense
		if dense == "" {
			dense = "off"
		}
		m.addSystem(fmt.Sprintf("  %-12s [%s]  model: %s  dense: %s", name, status, ac.Model, dense))
	}
}

func (m *Model) showSessions() {
	sessions, err := state.ListSessions(m.arosDir)
	if err != nil {
		m.addError(err.Error())
		return
	}
	activeID, _ := state.ActiveSessionID(m.arosDir)
	if len(sessions) == 0 {
		m.addSystem("No sessions found.")
		return
	}
	m.addSystem("Sessions:")
	for _, s := range sessions {
		prefix := " "
		if s.ID == activeID {
			prefix = "*"
		}
		m.addSystem(fmt.Sprintf("  %s %s  %s", prefix, s.ID, s.Name))
	}
}

// startChat asks the judge a free-form question with project context.
// All work happens inside the returned tea.Cmd (its own goroutine).
func (m *Model) startChat(text string) tea.Cmd {
	if m.rejectIfBusy() || !m.ready() {
		return nil
	}

	judgeName := m.cfg.Judge.Agent
	judge, err := m.reg.Judge(judgeName)
	if err != nil {
		// Chat is best-effort: any available agent will do, but say which.
		names := m.reg.Names()
		judgeName = names[0]
		judge = m.reg[judgeName]
		m.addSystem(fmt.Sprintf("judge %q unavailable — chatting with %s instead", m.cfg.Judge.Agent, judgeName))
	}

	judgeCfg := m.cfg.Agents[judgeName]
	m.stopChat() // retire any previous chat generation
	m.chatInProgress = true
	m.chatGen = m.nextGen()
	gen := m.chatGen
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(m.cfg.Work.AgentTimeoutSeconds)*time.Second)
	m.chatCancel = cancel
	m.setActivity(agentActivityMsg{agent: judgeName, model: judgeCfg.Model, status: "running", line: "thinking..."})

	// Snapshot for the goroutine.
	var project *state.ProjectState
	if m.project != nil {
		p := *m.project
		project = &p
	}
	var manifest *state.TaskManifest
	if m.manifest != nil {
		mf := *m.manifest
		manifest = &mf
	}
	mem := m.mem

	return func() tea.Msg {
		defer cancel()
		// bubbletea recovers panics in commands, but that kills the program —
		// turn one into an ordinary chat error instead.
		defer func() {
			if r := recover(); r != nil {
				send(chatDoneMsg{gen: gen, err: panicErr(r)})
			}
		}()

		memCtx := mem.Ask(ctx, text)
		prompt := withDense(buildChatPrompt(text, project, manifest, memCtx), judgeCfg)

		result, runErr := agent.Reason(ctx, judge, prompt)
		if runErr != nil {
			if ctx.Err() != nil {
				return chatDoneMsg{gen: gen, err: fmt.Errorf("chat cancelled")}
			}
			send(agentActivityMsg{gen: gen, agent: judgeName, status: "error", line: runErr.Error()})
			return chatDoneMsg{gen: gen, err: fmt.Errorf("chat: %w", runErr)}
		}
		if strings.TrimSpace(result.Output) == "" {
			send(streamLineMsg{gen: gen, agent: judgeName, line: "(no response)"})
		} else {
			streamOutput(gen, judgeName, result.Output)
		}
		send(agentActivityMsg{gen: gen, agent: judgeName, status: "done", line: "done"})
		return chatDoneMsg{gen: gen}
	}
}

func (m *Model) createSession(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("session name cannot be empty")
	}
	if err := os.MkdirAll(m.arosDir, 0755); err != nil {
		return err
	}
	meta, s, err := state.CreateSession(m.arosDir, name)
	if err != nil {
		return err
	}
	if err := state.SetActiveSession(m.arosDir, meta.ID); err != nil {
		return err
	}
	m.project = s
	m.sessionID = meta.ID
	m.sessionName = meta.Name
	m.reloadManifest()
	m.addSuccess(fmt.Sprintf("Session created → %s (%s)", meta.Name, meta.ID))
	return nil
}

func (m *Model) useSession(id string) error {
	if err := state.SetActiveSession(m.arosDir, id); err != nil {
		return err
	}
	s, err := state.LoadState(m.arosDir)
	if err != nil {
		return err
	}
	meta, err := state.ActiveSession(m.arosDir)
	if err == nil {
		m.sessionID = meta.ID
		m.sessionName = meta.Name
	} else {
		m.sessionID = id
		m.sessionName = ""
	}
	m.project = s
	m.reloadManifest()
	m.addSuccess(fmt.Sprintf("Active session → %s", id))
	return nil
}

func (m *Model) removeSession(id string, force bool) error {
	activeID, _ := state.ActiveSessionID(m.arosDir)
	if id == activeID && !force {
		return fmt.Errorf("cannot remove active session without --force")
	}
	if err := state.RemoveSession(m.arosDir, id); err != nil {
		return err
	}
	if id == activeID {
		sessions, err := state.ListSessions(m.arosDir)
		if err != nil {
			return err
		}
		if len(sessions) > 0 {
			if err := state.SetActiveSession(m.arosDir, sessions[0].ID); err != nil {
				return err
			}
			if s, err := state.LoadState(m.arosDir); err == nil {
				m.project = s
			}
			m.sessionID = sessions[0].ID
			m.sessionName = sessions[0].Name
		} else {
			if err := state.ClearActiveSession(m.arosDir); err != nil {
				return err
			}
			m.project = nil
			m.sessionID = ""
			m.sessionName = ""
		}
		m.reloadManifest()
	}
	m.addSuccess(fmt.Sprintf("Session removed → %s", id))
	return nil
}

func (m *Model) setPhase(phaseInput string) error {
	if m.project == nil {
		return fmt.Errorf("no project loaded")
	}
	phase, ok := state.ParsePhase(strings.ToLower(phaseInput))
	if !ok {
		return fmt.Errorf("unknown phase %q (valid: init, plan, divide, work, done)", phaseInput)
	}
	if m.project.Phase == phase {
		m.addSystem(fmt.Sprintf("Phase already %q", phase))
		return nil
	}
	m.project.Phase = phase
	if err := state.SaveState(m.arosDir, m.project); err != nil {
		return err
	}
	m.addSuccess("Phase changed → " + string(phase))
	return nil
}

func hasForceFlag(parts []string) bool {
	for _, p := range parts {
		if p == "--force" {
			return true
		}
	}
	return false
}

func stripFlags(parts []string) []string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if strings.HasPrefix(p, "-") {
			continue
		}
		out = append(out, p)
	}
	return out
}

func firstArg(parts []string) string {
	for _, p := range parts {
		if strings.HasPrefix(p, "-") {
			continue
		}
		return p
	}
	return ""
}

func (m *Model) showStatus() {
	if m.project == nil {
		m.addSystem("No project loaded.")
		return
	}
	if m.sessionID != "" {
		m.addSystem(fmt.Sprintf("Project: %s  |  Session: %s  |  Phase: %s", m.project.ProjectName, m.sessionID, m.project.Phase))
	} else {
		m.addSystem(fmt.Sprintf("Project: %s  |  Phase: %s", m.project.ProjectName, m.project.Phase))
	}
	if m.project.Task != "" {
		m.addSystem("Task: " + m.project.Task)
	}
	m.reloadManifest()
	if m.manifest == nil || len(m.manifest.Tasks) == 0 {
		return
	}
	m.addSystem(fmt.Sprintf("%-12s %-28s %-12s %s", "ID", "Title", "Agent", "Status"))
	m.addSystem(strings.Repeat("─", 65))
	for _, t := range m.manifest.Tasks {
		icon := "○"
		switch t.Status {
		case state.TaskDone:
			icon = "✓"
		case state.TaskInProgress:
			icon = "►"
		case state.TaskBlocked:
			icon = "✗"
		}
		status := string(t.Status)
		if t.BlockReason != "" {
			status += "  " + truncate(t.BlockReason, 60)
		}
		m.addSystem(fmt.Sprintf("%s %-11s %-28s %-12s %s", icon, t.ID, truncate(t.Title, 27), t.AssignedTo, status))
	}
}

// ── Message helpers ─────────────────────────────────────────────────────────────

func (m *Model) addSystem(text string) {
	m.messages = append(m.messages, ChatMessage{Kind: kindSystem, Body: text})
	m.refreshViewport()
}

func (m *Model) addAgent(name, text string) {
	m.messages = append(m.messages, ChatMessage{Kind: kindAgent, AgentName: name, Body: text})
	m.refreshViewport()
}

func (m *Model) appendStream(name, line string) {
	if len(m.messages) > 0 {
		last := &m.messages[len(m.messages)-1]
		if last.Kind == kindAgent && last.AgentName == name {
			last.Body += "\n" + line // raw append — indent added at render time
			m.refreshViewport()
			return
		}
	}
	m.addAgent(name, line)
}

func (m *Model) addHuman(text string) {
	m.messages = append(m.messages, ChatMessage{Kind: kindHuman, Body: text})
	m.refreshViewport()
}

func (m *Model) addError(text string) {
	m.messages = append(m.messages, ChatMessage{Kind: kindError, Body: text})
	m.refreshViewport()
}

func (m *Model) addSuccess(text string) {
	m.messages = append(m.messages, ChatMessage{Kind: kindSuccess, Body: text})
	m.refreshViewport()
}

// refreshViewport re-renders all messages with the current panel width and updates the viewport.
func (m *Model) refreshViewport() {
	w := m.leftW
	if w == 0 {
		w = m.viewport.Width
	}
	if w == 0 {
		w = 80
	}

	var parts []string
	for _, msg := range m.messages {
		parts = append(parts, m.renderChatMessage(msg, w))
	}
	m.viewport.SetContent(strings.Join(parts, "\n"))
	m.viewport.GotoBottom()
}

// renderChatMessage renders one ChatMessage block styled for the given width.
func (m *Model) renderChatMessage(msg ChatMessage, w int) string {
	bodyW := w - 6 // space for 4-char left indent + 2 safety
	if bodyW < 20 {
		bodyW = 20
	}

	switch msg.Kind {

	case kindBanner:
		bannerStyle := lipgloss.NewStyle().Bold(true).Foreground(colorBrand)
		return lipgloss.NewStyle().Width(w).Align(lipgloss.Center).
			Render(bannerStyle.Render(msg.Body))

	case kindHuman:
		arrow := lipgloss.NewStyle().Foreground(colorBrand).Bold(true).Render("▸")
		you := lipgloss.NewStyle().Foreground(lipgloss.Color("#EC4899")).Bold(true).Render("you")
		badge := arrow + " " + you
		body := lipgloss.NewStyle().Foreground(colorText).Width(bodyW).Render(msg.Body)
		indented := lipgloss.NewStyle().PaddingLeft(4).Render(body)
		return badge + "\n" + indented + "\n"

	case kindAgent:
		dot := lipgloss.NewStyle().Foreground(agentColor(msg.AgentName)).Render("●")
		name := lipgloss.NewStyle().Foreground(agentColor(msg.AgentName)).Bold(true).Render(msg.AgentName)
		header := dot + " " + name
		if msg.ModelName != "" {
			header += " " + lipgloss.NewStyle().Foreground(colorSubtle).Render("("+msg.ModelName+")")
		}
		var bodyLines []string
		for _, l := range strings.Split(msg.Body, "\n") {
			bodyLines = append(bodyLines,
				lipgloss.NewStyle().Foreground(colorText).Width(bodyW).Render(l))
		}
		body := lipgloss.NewStyle().PaddingLeft(4).Render(strings.Join(bodyLines, "\n"))
		return header + "\n" + body + "\n"

	case kindSystem:
		return lipgloss.NewStyle().
			Foreground(colorSubtle).Italic(true).
			PaddingLeft(2).
			Render(msg.Body)

	case kindSuccess:
		icon := lipgloss.NewStyle().Foreground(colorSuccess).Bold(true).Render("✓")
		text := lipgloss.NewStyle().Foreground(colorSuccess).Width(w - 5).Render(msg.Body)
		return "  " + icon + "  " + text

	case kindError:
		icon := lipgloss.NewStyle().Foreground(colorError).Bold(true).Render("✗")
		text := lipgloss.NewStyle().Foreground(colorError).Bold(true).Width(w - 5).Render(msg.Body)
		return "  " + icon + "  " + text
	}
	return msg.Body
}

func (m *Model) setMode(mode inputMode, prompt string) {
	m.mode = mode
	m.prompt = prompt
}

// initProject creates the first session off the event loop and reports back.
func (m *Model) initProject(name string) tea.Cmd {
	arosDir := m.arosDir
	return func() tea.Msg {
		if err := os.MkdirAll(arosDir, 0755); err != nil {
			return phaseResultMsg{err: err}
		}
		meta, s, err := state.CreateSession(arosDir, name)
		if err != nil {
			return phaseResultMsg{err: err}
		}
		if err := state.SetActiveSession(arosDir, meta.ID); err != nil {
			return phaseResultMsg{err: err}
		}
		cfgPath := filepath.Join(arosDir, "config.toml")
		if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
			_ = os.WriteFile(cfgPath, []byte("# Aros project config — overrides ~/.aros/config.toml\n"), 0644)
		}
		return sessionLoadedMsg{project: s, sessionID: meta.ID, sessionName: meta.Name}
	}
}

// sessionLoadedMsg installs a freshly created session.
type sessionLoadedMsg struct {
	project     *state.ProjectState
	sessionID   string
	sessionName string
}

func (m *Model) setAgentModel(agentName, modelName string) error {
	ac, ok := m.cfg.Agents[agentName]
	if !ok {
		return fmt.Errorf("unknown agent %q", agentName)
	}
	ac.Model = modelName
	ac.Enabled = true
	m.cfg.Agents[agentName] = ac
	return m.rebuildRegistry()
}

func (m *Model) setAgentDense(agentName, level string) error {
	ac, ok := m.cfg.Agents[agentName]
	if !ok {
		return fmt.Errorf("unknown agent %q", agentName)
	}
	switch level {
	case "off", "none", "":
		level = ""
	case "lite", "full", "ultra":
	default:
		return fmt.Errorf("unknown dense level %q; valid: %s", level, strings.Join(knownDenseLevels, ", "))
	}
	ac.Dense = level
	m.cfg.Agents[agentName] = ac
	return nil
}

func (m *Model) setJudge(agentName string) error {
	if _, ok := m.cfg.Agents[agentName]; !ok {
		return fmt.Errorf("unknown agent %q", agentName)
	}
	if _, ok := m.reg[agentName]; !ok {
		return fmt.Errorf("agent %q is not available (disabled or binary missing)", agentName)
	}
	m.cfg.Judge.Agent = agentName
	return nil
}

func (m *Model) rebuildRegistry() error {
	reg, warnings, err := agent.BuildRegistry(m.cfg, m.cwd)
	for _, w := range warnings {
		m.addSystem("warning: " + w)
	}
	if err != nil {
		return err
	}
	m.reg = reg
	return nil
}
