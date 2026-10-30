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
	gitrepo "github.com/Rinil-Parmar/aros/git"
	"github.com/Rinil-Parmar/aros/memory"
	"github.com/Rinil-Parmar/aros/state"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
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

	// replaying is true while a saved conversation is being pushed back into
	// the view, so the replay is not written to the log again. replayAt is the
	// original arrival time of the entry currently being replayed.
	replaying bool
	replayAt  time.Time
	// chatLogBroken records that persistence failed, so the warning is shown
	// once instead of on every message.
	chatLogBroken bool

	// mouseOff is true when mouse reporting has been turned off so the
	// terminal's own click-drag selection works and chat text can be copied
	// with the mouse. Scroll-wheel scrolling is unavailable while it is off.
	mouseOff bool

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

		// Selecting text with the mouse is impossible while a TUI has mouse
		// reporting on — the terminal hands us the drag instead of selecting.
		// Ctrl+O hands it back so the chat can be selected and copied normally.
		switch key {
		case "ctrl+o":
			m.mouseOff = !m.mouseOff
			if m.mouseOff {
				m.addSystem("Mouse reporting off — drag to select and copy with your terminal. Ctrl+O to re-enable wheel scrolling.")
				return m, tea.DisableMouse
			}
			m.addSystem("Mouse reporting on — wheel scrolling restored.")
			return m, tea.EnableMouseAllMotion
		case "ctrl+y":
			m.copyTranscript("")
			return m, nil
		}

		// Ctrl shortcuts
		switch key {
		case "ctrl+h":
			m.showHelp()
			return m, nil
		case "ctrl+k":
			m.messages = nil
			_ = state.ClearChat(m.arosDir)
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
		// Record which vendor sessions our agent calls create, so removing a
		// session can clean up the files they leave outside the project.
		installSessionRecorder(msg.arosDir)
		m.cfg = msg.cfg
		m.reg = msg.reg
		m.mem = msg.mem
		m.project = msg.project
		m.sessionID = msg.sessionID
		m.sessionName = msg.sessionName
		m.reloadManifest()
		// Order matters: brand at the top, then the replayed conversation,
		// then where the work stands — oldest to newest, reading downwards.
		m.showBanner()
		m.replayChat()
		m.showSessionState()
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

	case modelListMsg:
		m.busy = false
		m.phaseCancel = nil
		if !m.live(msg.gen) {
			break
		}
		m.showModelList(msg)

	case modelSetMsg:
		m.busy = false
		m.phaseCancel = nil
		if !m.live(msg.gen) {
			break
		}
		if msg.err != nil {
			m.addError(msg.err.Error())
			break
		}
		m.applyModel(msg.agent, msg.model, msg.warning, msg.scope)

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
	pr := phaseRun{gen: m.phaseGen, ctx: ctx, cfg: m.cfg, reg: m.reg, mem: m.mem, arosDir: m.arosDir, cwd: m.cwd}
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
	had := m.busy || m.chatInProgress || m.mode == modeApproval || m.onFreeText != nil || m.initializing
	m.initializing = false // otherwise Esc mid-creation wedges every later input
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
		if err := state.ClearChat(m.arosDir); err != nil {
			m.addError("could not clear the saved chat: " + err.Error())
		}
		m.refreshViewport()

	case "/commit":
		if m.rejectIfBusy() {
			return nil
		}
		if m.project == nil {
			m.addError("no project loaded")
			return nil
		}
		repo, ok := gitrepo.Open(m.cwd)
		if !ok {
			m.addError(m.cwd + " is not a git repository")
			return nil
		}
		pr := m.newPhaseRun()
		tasks := []state.Task{}
		if m.manifest != nil {
			tasks = m.manifest.Tasks
		}
		mode := "ask"
		if hasForceFlag(parts) {
			mode = "auto"
		}
		safeGo(pr.gen, func() {
			commitChanges(pr, repo, mode, tasks, func() {
				send(phaseResultMsg{gen: pr.gen})
			})
		})

	case "/copy":
		arg := ""
		if len(parts) > 1 {
			arg = strings.ToLower(parts[1])
		}
		m.copyTranscript(arg)

	case "/export":
		path := "aros-transcript.md"
		if len(parts) > 1 {
			path = strings.Join(parts[1:], " ")
		}
		if err := m.exportTranscript(path); err != nil {
			m.addError(err.Error())
		} else {
			m.addSuccess("Transcript written to " + path)
		}

	case "/model":
		if m.cfg == nil {
			m.addError("configuration not loaded")
			return nil
		}
		return m.handleModelCmd(parts)

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
		dScope, dArgs := parseScope(parts[2:])
		if len(dArgs) == 0 {
			m.addSystem("Usage: /dense <agent> <level>")
			return nil
		}
		agentName, level := strings.ToLower(parts[1]), strings.ToLower(dArgs[0])
		if err := m.setAgentDense(agentName, level); err != nil {
			m.addError(err.Error())
			return nil
		}
		m.addSuccess(fmt.Sprintf("%s dense → %s", agentName, level))
		stored := level
		if level == "off" || level == "none" {
			stored = ""
		}
		m.persistSetting(dScope, func(scope config.Scope) (string, error) {
			return config.SetAgentDense(m.cwd, agentName, stored, scope)
		})

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
		jScope, _ := parseScope(parts[2:])
		name := strings.ToLower(parts[1])
		if err := m.setJudge(name); err != nil {
			m.addError(err.Error())
			return nil
		}
		m.addSuccess("judge → " + name)
		m.persistSetting(jScope, func(scope config.Scope) (string, error) {
			return config.SetJudge(m.cwd, name, scope)
		})

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
		m.addChrome(m.nextStepHint())
		m.clearActivity()
	case "divide_done":
		m.addSuccess("Tasks assigned.")
		m.addChrome(m.nextStepHint())
		m.clearActivity()
	case "work_done":
		m.addSuccess("All tasks complete.")
		m.addChrome(m.nextStepHint())
		m.clearActivity()
	default:
		m.clearActivity()
	}
	m.setMode(modeText, "")
}

// showBanner emits the brand block. It must run *before* a replayed
// conversation: the banner marks the start of the session, so pushing it in
// after the history put it below months of chat, which read as broken.
func (m *Model) showBanner() {
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

	// On a short terminal the six-line banner costs a quarter of the view, so
	// show a one-line brand instead.
	if m.height >= 28 || m.height == 0 {
		// kindBanner so refreshViewport re-centres it on resize.
		m.messages = append(m.messages, ChatMessage{Kind: kindBanner, Body: bannerText})
		m.addChrome("Multi-agent AI orchestrator  ·  v0.1.0")
	} else {
		m.addChrome("◆ AROS  ·  multi-agent orchestrator  ·  v0.1.0")
	}
	m.addChrome(strings.Repeat("─", max(10, w-6)))

}

// showSessionState emits where the project stands and the one command that
// makes sense next. It runs *after* any replayed conversation, so the thing
// you need is immediately above the input box where your eye already is.
func (m *Model) showSessionState() {
	if m.project == nil {
		m.addChrome("No project here yet. Type a name to start one:")
		m.setMode(modeText, "project name")
		return
	}
	// Where we are and what to do next — not the manual. Sixteen lines of
	// help on every start buries the actual state.
	m.addChrome(fmt.Sprintf("%s · phase %s", m.project.ProjectName, m.project.Phase))
	if m.manifest != nil && len(m.manifest.Tasks) > 0 {
		if p := m.taskProgress(); p != "" {
			m.addChrome(p)
		}
	} else if m.project.Task != "" {
		m.addChrome("task: " + truncate(m.project.Task, 72))
	}
	m.addChrome("")
	m.addChrome(m.nextStepHint())
	m.addChrome("/help for all commands")
	m.setMode(modeText, "")
}

// nextStepHint names the one command that makes sense in the current phase.
func (m *Model) nextStepHint() string {
	if m.project == nil {
		return "type a project name to begin"
	}
	switch m.project.Phase {
	case state.PhaseInit:
		return "next:  plan <what you want to build>"
	case state.PhasePlan:
		return "next:  divide    (break the approved plan into tasks)"
	case state.PhaseDivide:
		return "next:  work      (run the tasks)"
	case state.PhaseWork:
		return "next:  work      (resume — retries anything blocked)"
	case state.PhaseDone:
		return "done. plan <task> for the next piece, or /session new <name>"
	}
	return "type /help for commands"
}

func (m *Model) showHelp() {
	m.addChrome("Commands:")
	for _, l := range []string{
		"  plan <task>      generate multi-agent plan",
		"  divide           break plan into tasks",
		"  work             execute tasks (re-run to retry blocked tasks)",
		"  status           current state and tasks",
		"",
		"Slash commands:",
		"  /model [agent] [text]      list or set an agent's model (text filters)",
		"  /dense <agent> <level>     set dense output (off|lite|full|ultra)",
		"  settings are saved to .aros/config.toml · --global or --session to change that",
		"  /judge <agent>             change which agent is the judge",
		"  /agents                    show all configured agents",
		"  /session <new|list|use|rm>  manage sessions",
		"  /phase <phase> [--force]    set phase (init|plan|divide|work|done); --force aborts a running phase",
		"  /commit [--force]          commit the agents' changes (asks first; never pushes)",
		"  /copy [last|all]           copy chat to clipboard (last agent reply, or all)",
		"  /export [file]             write the chat to a markdown file",
		"  /clear                     clear the chat, here and in the saved log",
		"  /help, /quit",
		"",
		"Esc or Ctrl+G        cancel whatever is running and return to idle",
		"Ctrl+Y               copy the whole chat to the clipboard",
		"Ctrl+O               toggle mouse reporting — turn it off to select text with the mouse",
		"",
		"Phase shortcut: Shift+Tab (cycle init → plan → divide → work → done)",
		"Scrolling: pgup/pgdn  •  shift+up/down  •  mouse wheel",
		"Multi-line input: Shift+Enter",
		"",
		"Any other text chats with the judge agent.",
	} {
		m.addChrome(l)
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
	m.addChrome(fmt.Sprintf("Judge: %s", m.cfg.Judge.Agent))
	m.addChrome("Agents:")
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
		m.addChrome(fmt.Sprintf("  %-12s [%s]  model: %s  dense: %s", name, status, ac.Model, dense))
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
		m.addChrome("No sessions found.")
		return
	}
	m.addChrome("Sessions:")
	for _, s := range sessions {
		prefix := " "
		if s.ID == activeID {
			prefix = "*"
		}
		m.addChrome(fmt.Sprintf("  %s %s  %s", prefix, s.ID, s.Name))
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
	m.messages = nil // a fresh session starts with a clean conversation
	m.chatLogBroken = false
	m.addSuccess(fmt.Sprintf("Session created → %s (%s)", meta.Name, meta.ID))
	m.addChrome(m.nextStepHint())
	return nil
}

func (m *Model) useSession(id string) error {
	// Load first: switching the active-session pointer and *then* failing to
	// read the state left the pointer on the new session while m.project still
	// held the old one, so the next save wrote the old project into it.
	s, err := state.LoadSessionState(m.arosDir, id)
	if err != nil {
		return fmt.Errorf("session %q is not readable: %w", id, err)
	}
	if err := state.SetActiveSession(m.arosDir, id); err != nil {
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
	// Each session owns its conversation, so swap the view over to it.
	m.messages = nil
	m.replayChat()
	m.addSuccess(fmt.Sprintf("Active session → %s", id))
	m.addChrome(fmt.Sprintf("%s · phase %s", s.ProjectName, s.Phase))
	m.addChrome(m.nextStepHint())
	return nil
}

func (m *Model) removeSession(id string, force bool) error {
	activeID, _ := state.ActiveSessionID(m.arosDir)
	if id == activeID && !force {
		return fmt.Errorf("cannot remove active session without --force")
	}
	// Clean up the vendor sessions before our record of them disappears.
	// Deleting another tool's files can block, so it runs off the event loop
	// and reports back as ordinary chat lines.
	if recorded, err := state.LoadAgentSessions(m.arosDir, id); err == nil && len(recorded) > 0 {
		m.addSystem(fmt.Sprintf("cleaning up %d agent session(s)…", len(recorded)))
		arosDir := m.arosDir
		safeGoQuiet(func() {
			summary := cleanupAgentSessionsTUI(arosDir, recorded)
			send(streamLineMsg{agent: "aros", line: summary})
		})
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
		m.addChrome("No project loaded.")
		return
	}
	if m.sessionID != "" {
		m.addChrome(fmt.Sprintf("Project: %s  |  Session: %s  |  Phase: %s", m.project.ProjectName, m.sessionID, m.project.Phase))
	} else {
		m.addChrome(fmt.Sprintf("Project: %s  |  Phase: %s", m.project.ProjectName, m.project.Phase))
	}
	if m.project.Task != "" {
		m.addChrome("Task: " + m.project.Task)
	}
	m.reloadManifest()
	if m.manifest == nil || len(m.manifest.Tasks) == 0 {
		return
	}
	m.addChrome(fmt.Sprintf("%-12s %-28s %-12s %s", "ID", "Title", "Agent", "Status"))
	m.addChrome(strings.Repeat("─", 65))
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
		m.addChrome(fmt.Sprintf("%s %-11s %-28s %-12s %s", icon, t.ID, truncate(t.Title, 27), t.AssignedTo, status))
	}
}

// ── Message helpers ─────────────────────────────────────────────────────────────

func (m *Model) addSystem(text string) {
	m.messages = append(m.messages, ChatMessage{Kind: kindSystem, Body: text, At: m.stamp()})
	m.persist(state.ChatSystem, "", "", text)
	m.refreshViewport()
}

// stamp is the time to put on a new message. During replay it is the time the
// message originally arrived, so a resumed transcript shows real history
// rather than the moment it was reloaded.
func (m *Model) stamp() time.Time {
	if !m.replayAt.IsZero() {
		return m.replayAt
	}
	return time.Now()
}

// modelFor names the model an agent is configured with, for the turn header.
// Empty for pseudo-agents like "judge" and "aros", which have no config entry.
func (m *Model) modelFor(agentName string) string {
	if m.cfg == nil {
		return ""
	}
	return m.cfg.Agents[agentName].Model
}

// parseScope pulls --global/--session out of a command's arguments. A setting
// is written to the project config by default: it belongs to the project you
// are working in, not to every project you own.
func parseScope(args []string) (config.Scope, []string) {
	scope := config.ScopeProject
	var rest []string
	for _, a := range args {
		switch a {
		case "--global", "-g":
			scope = config.ScopeGlobal
		case "--session":
			scope = scopeSession
		default:
			rest = append(rest, a)
		}
	}
	return scope, rest
}

// persistSetting writes a setting and reports where it landed, so a change is
// never silently confined to the running session.
func (m *Model) persistSetting(scope config.Scope, write func(config.Scope) (string, error)) {
	if scope == scopeSession {
		m.addChrome("  this session only — it will revert when you reopen")
		return
	}
	path, err := write(scope)
	if err != nil {
		m.addError("could not save it: " + err.Error())
		m.addChrome("  the change applies to this session only")
		return
	}
	m.addChrome("  saved to " + m.shortPath(path))
}

// addChrome adds a line of UI furniture — banner, help, status tables, the
// resume divider, the next-step hint. It is deliberately *not* written to the
// conversation log: that log holds what you and the agents said, so it stays
// small and does not grow by a greeting on every restart. Chrome is
// regenerated on open and re-runnable with /help, /status and /agents.
func (m *Model) addChrome(text string) {
	m.messages = append(m.messages, ChatMessage{Kind: kindSystem, Body: text, At: m.stamp()})
	m.refreshViewport()
}

// persist appends one entry to the session's conversation log so reopening the
// session shows where the work left off. Best-effort: a logging failure must
// never interrupt the session, so it warns once and then stays quiet.
func (m *Model) persist(kind, agent, model, body string) {
	if m.replaying || m.chatLogBroken || m.arosDir == "" || m.project == nil {
		return
	}
	err := state.AppendChat(m.arosDir, state.ChatEntry{
		Kind: kind, Agent: agent, Model: model, Body: body,
	})
	if err != nil {
		m.chatLogBroken = true
		m.messages = append(m.messages, ChatMessage{
			Kind: kindSystem,
			Body: "warning: chat history will not be saved for this session: " + err.Error(),
		})
	}
}

func (m *Model) addAgent(name, text string) {
	m.messages = append(m.messages, ChatMessage{
		Kind: kindAgent, AgentName: name, ModelName: m.modelFor(name), Body: text, At: m.stamp(),
	})
	m.persist(state.ChatAgent, name, m.modelFor(name), text)
	m.refreshViewport()
}

func (m *Model) appendStream(name, line string) {
	if len(m.messages) > 0 {
		last := &m.messages[len(m.messages)-1]
		if last.Kind == kindAgent && last.AgentName == name {
			last.Body += "\n" + line // raw append — indent added at render time
			// Each line is logged separately; replay merges them back with
			// this same function, so nothing has to be rewritten in place.
			m.persist(state.ChatAgent, name, "", line)
			m.refreshViewport()
			return
		}
	}
	m.addAgent(name, line)
}

func (m *Model) addHuman(text string) {
	m.messages = append(m.messages, ChatMessage{Kind: kindHuman, Body: text, At: m.stamp()})
	m.persist(state.ChatHuman, "", "", text)
	m.refreshViewport()
}

func (m *Model) addError(text string) {
	m.messages = append(m.messages, ChatMessage{Kind: kindError, Body: text, At: m.stamp()})
	m.persist(state.ChatError, "", "", text)
	m.refreshViewport()
}

func (m *Model) addSuccess(text string) {
	m.messages = append(m.messages, ChatMessage{Kind: kindSuccess, Body: text, At: m.stamp()})
	m.persist(state.ChatSuccess, "", "", text)
	m.refreshViewport()
}

// replayChat pushes a saved conversation back into the view. Entries go through
// the normal add/append helpers so a streamed reply merges into one block and
// renders exactly as it did live.
func (m *Model) replayChat() {
	entries, err := state.LoadChat(m.arosDir, state.MaxChatReplay)
	if err != nil || len(entries) == 0 {
		return
	}
	m.replaying = true
	defer func() { m.replaying = false; m.replayAt = time.Time{} }()

	for _, e := range entries {
		m.replayAt = e.At
		switch e.Kind {
		case state.ChatHuman:
			m.addHuman(e.Body)
		case state.ChatAgent:
			m.appendStream(e.Agent, e.Body)
		case state.ChatError:
			m.addError(e.Body)
		case state.ChatSuccess:
			m.addSuccess(e.Body)
		default:
			m.addSystem(e.Body)
		}
	}
	m.addChrome("")
	m.addChrome(fmt.Sprintf("── %d earlier message(s) above · resumed ──", len(entries)))
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
//
// Each role gets a distinct gutter and header so a long transcript can be
// scanned: your turns, agent replies, and the orchestrator's own notes should
// be tellable apart without reading them.
func (m *Model) renderChatMessage(msg ChatMessage, w int) string {
	const indent = 4
	bodyW := w - indent - 2 // gutter plus a right margin
	if bodyW < 20 {
		bodyW = 20
	}

	switch msg.Kind {

	case kindBanner:
		return lipgloss.NewStyle().Width(w).Align(lipgloss.Center).
			Render(lipgloss.NewStyle().Bold(true).Foreground(colorBrand).Render(msg.Body))

	case kindHuman:
		header := styleYouMark.Render("▌") + " " + styleYouName.Render("you") + m.timeSuffix(msg, w, 5)
		body := lipgloss.NewStyle().PaddingLeft(indent).
			Render(lipgloss.NewStyle().Foreground(colorText).Width(bodyW).Render(msg.Body))
		return header + "\n" + body + "\n"

	case kindAgent:
		c := agentColor(msg.AgentName)
		header := lipgloss.NewStyle().Foreground(c).Render("▌") + " " +
			lipgloss.NewStyle().Foreground(c).Bold(true).Render(msg.AgentName)
		meta := msg.ModelName
		if meta != "" {
			header += " " + styleMuted.Render(meta)
		}
		header += m.timeSuffix(msg, w, lipgloss.Width(header))
		// Agent output is markdown; render the subset agents actually emit.
		body := lipgloss.NewStyle().PaddingLeft(indent).
			Render(renderAgentBody(msg.Body, bodyW))
		return header + "\n" + body + "\n"

	case kindSystem:
		return lipgloss.NewStyle().
			Foreground(colorSubtle).
			PaddingLeft(2).
			Width(w - 2).
			Render(msg.Body)

	case kindSuccess:
		icon := styleTaskDone.Render("✓")
		text := lipgloss.NewStyle().Foreground(colorSuccess).Width(w - 6).Render(msg.Body)
		return "  " + icon + "  " + text

	case kindError:
		icon := styleActivityError.Render("✗")
		text := lipgloss.NewStyle().Foreground(colorError).Width(w - 6).Render(msg.Body)
		return "  " + icon + "  " + text
	}
	return msg.Body
}

// timeSuffix right-aligns a dim HH:MM on the header line. It answers "when did
// this happen" — which only became answerable once the chat persisted, and
// matters most in a replayed transcript.
func (m *Model) timeSuffix(msg ChatMessage, w, used int) string {
	if msg.At.IsZero() {
		return ""
	}
	ts := msg.At.Format("15:04")
	pad := w - used - lipgloss.Width(ts) - 2
	if pad < 1 {
		return ""
	}
	return strings.Repeat(" ", pad) + styleTurnTime.Render(ts)
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

// ── Copying the chat out ────────────────────────────────────────────────────────

// plainTranscript renders the chat as plain text with no ANSI styling, so it is
// useful when pasted into an editor, an issue or a message.
// scope "last" returns only the most recent agent reply.
func (m *Model) plainTranscript(scope string) string {
	if scope == "last" {
		for i := len(m.messages) - 1; i >= 0; i-- {
			if m.messages[i].Kind == kindAgent {
				return m.messages[i].Body
			}
		}
		return ""
	}

	var b strings.Builder
	for _, msg := range m.messages {
		switch msg.Kind {
		case kindBanner:
			continue // ASCII art is noise outside the terminal
		case kindHuman:
			b.WriteString("\n> " + msg.Body + "\n")
		case kindAgent:
			header := "\n[" + msg.AgentName
			if msg.ModelName != "" {
				header += " · " + msg.ModelName
			}
			b.WriteString(header + "]\n" + msg.Body + "\n")
		case kindError:
			b.WriteString("ERROR: " + msg.Body + "\n")
		case kindSuccess:
			b.WriteString("OK: " + msg.Body + "\n")
		default:
			b.WriteString(msg.Body + "\n")
		}
	}
	return strings.TrimSpace(b.String())
}

// copyTranscript puts the chat on the system clipboard using OSC 52, which the
// terminal emulator handles — so it also works over SSH, where a clipboard
// library running on the remote host would not.
func (m *Model) copyTranscript(scope string) {
	text := m.plainTranscript(scope)
	if text == "" {
		m.addSystem("Nothing to copy.")
		return
	}
	termenv.Copy(text)
	what := fmt.Sprintf("%d lines", strings.Count(text, "\n")+1)
	if scope == "last" {
		what = "last agent reply (" + what + ")"
	} else {
		what = "chat (" + what + ")"
	}
	m.addSuccess("Copied " + what + " to the clipboard.")
	m.addSystem("If your terminal blocks OSC 52, use /export <file> or Ctrl+O then select with the mouse.")
}

// exportTranscript writes the chat to a markdown file.
func (m *Model) exportTranscript(path string) error {
	text := m.plainTranscript("")
	if text == "" {
		return fmt.Errorf("nothing to export")
	}
	header := "# Aros transcript\n"
	if m.project != nil {
		header += fmt.Sprintf("\nProject: %s  \nPhase: %s  \n", m.project.ProjectName, m.project.Phase)
	}
	header += fmt.Sprintf("Exported: %s\n\n```\n", time.Now().Format("2006-01-02 15:04:05"))
	if err := os.WriteFile(path, []byte(header+text+"\n```\n"), 0644); err != nil {
		return fmt.Errorf("writing transcript: %w", err)
	}
	return nil
}
