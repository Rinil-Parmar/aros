package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

	cfg     *config.Config
	reg     agent.Registry
	mem     *memory.SecondMem
	project *state.ProjectState
	arosDir string
	cwd     string
	busy    bool

	activity         map[string]*agentStatus
	approvalQuestion string

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
		"claude-haiku-4-5-20251001",
		"claude-sonnet-4-6",
		"claude-opus-4-7",
	},
	"opencode": {
		"openai/gpt-4o-mini",
		"openai/gpt-4o",
		"anthropic/claude-sonnet-4-5",
		"anthropic/claude-haiku-4-5",
		"google/gemini-2.0-flash",
		"google/gemini-pro",
		"deepseek/deepseek-chat",
	},
	"copilot": {
		"gpt-4.1",
		"gpt-4.1-mini",
		"gpt-4o",
		"gpt-4o-mini",
		"o3",
		"o4-mini",
		"gemini-2.0-flash",
	},
}

var knownDenseLevels = []string{"", "lite", "full", "ultra"}

var program *tea.Program

func SetProgram(p *tea.Program) { program = p }

func send(msg tea.Msg) {
	if program != nil {
		program.Send(msg)
	}
}

func New() *Model {
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
		spinner:  sp,
		textarea: ta,
		viewport: vp,
		mode:     modeText,
	}
}

func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, textarea.Blink, m.bootstrap())
}

func (m *Model) bootstrap() tea.Cmd {
	return func() tea.Msg {
		cwd, _ := os.Getwd()
		m.cwd = cwd
		m.arosDir = filepath.Join(cwd, ".aros")

		cfg, err := config.Load("")
		if err != nil {
			return phaseResultMsg{err: fmt.Errorf("config: %w", err)}
		}
		m.cfg = cfg
		m.mem = memory.New(cfg.SecondMem.Binary, cfg.SecondMem.Enabled)

		reg, regErr := agent.BuildRegistry(cfg, cwd)
		if regErr != nil {
			m.addSystem("warning: " + regErr.Error())
		} else {
			m.reg = reg
		}

		if s, err := state.LoadState(m.arosDir); err == nil {
			m.project = s
		}

		return phaseResultMsg{phase: "bootstrap"}
	}
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {

	case tea.KeyMsg:
		key := msg.String()

		// Always-active quit
		switch key {
		case "ctrl+c":
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

		// Approval mode accepts immediate single-key responses (no Enter required).
		if m.mode == modeApproval {
			switch strings.ToLower(key) {
			case "y", "n":
				cmds = append(cmds, m.handleInput(strings.ToLower(key)))
				return m, tea.Batch(cmds...)
			}
		}

		// Enter submits; Shift+Enter / Alt+Enter handled by textarea (newline)
		if msg.Type == tea.KeyEnter && !msg.Alt {
			// While busy, block regular commands but keep interactive prompts usable.
			if m.busy && m.mode != modeApproval && m.onFreeText == nil {
				return m, nil
			}
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

	case phaseResultMsg:
		m.busy = false
		if msg.err != nil {
			m.addError(msg.err.Error())
			m.setMode(modeText, "")
		} else {
			cmds = append(cmds, m.handlePhaseResult(msg))
		}

	case streamLineMsg:
		if msg.done {
			m.busy = false
		} else {
			m.appendStream(msg.agent, msg.line)
		}

	case approvalMsg:
		m.onYes = msg.onYes
		m.onNo = msg.onNo
		m.approvalQuestion = msg.question
		m.setMode(modeApproval, "y/n")
		m.recalcLayout()

	case freeInputMsg:
		m.busy = false // allow Enter so user can submit feedback
		m.onFreeText = msg.callback
		m.addSystem(msg.prompt)
		m.setMode(modeText, msg.prompt)

	case agentActivityMsg:
		if m.activity == nil {
			m.activity = make(map[string]*agentStatus)
		}
		if msg.status == "clear" {
			m.activity = make(map[string]*agentStatus)
			m.recalcLayout()
			break
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

func (m *Model) handleInput(text string) tea.Cmd {
	m.addHuman(text)

	switch m.mode {
	case modeApproval:
		cbYes := m.onYes
		cbNo := m.onNo
		m.approvalQuestion = ""
		m.onYes = nil
		m.onNo = nil
		// Unlock input immediately — never hold the UI hostage waiting for a goroutine
		m.busy = false
		m.setMode(modeText, "")
		m.recalcLayout()
		if strings.ToLower(text) == "y" || strings.ToLower(text) == "yes" {
			if cbYes != nil {
				go cbYes()
			}
		} else {
			if cbNo != nil {
				go cbNo()
			}
		}
		return nil

	case modeText, modeIdle:
		if m.onFreeText != nil {
			cb := m.onFreeText
			m.onFreeText = nil
			m.busy = true // set in the Update loop (not in goroutine) — no data race
			m.setMode(modeIdle, "")
			go cb(text)
			return nil
		}
		return m.dispatch(text)
	}
	return nil
}

func (m *Model) dispatch(text string) tea.Cmd {
	if strings.HasPrefix(text, "/") {
		return m.handleSlash(text)
	}

	if m.project == nil {
		return m.initProject(text)
	}

	lower := strings.ToLower(text)
	switch {
	case lower == "help":
		m.showHelp()
	case lower == "status":
		m.showStatus()
	case lower == "divide":
		m.busy = true
		go m.runDivide()
	case lower == "work":
		m.busy = true
		go m.runWork()
	case strings.HasPrefix(lower, "plan"):
		task := strings.TrimSpace(text[len("plan"):])
		if task == "" {
			m.onFreeText = func(t string) {
				m.busy = true
				go m.runPlan(t)
			}
			m.addSystem("What do you want to build?")
			m.setMode(modeText, "task")
		} else {
			m.busy = true
			go m.runPlan(task)
		}
	default:
		m.addSystem("Unknown command. Type help for commands.")
	}
	return nil
}

func (m *Model) handleSlash(text string) tea.Cmd {
	parts := strings.Fields(text)
	cmd := strings.ToLower(parts[0])

	switch cmd {
	case "/help":
		m.showHelp()

	case "/quit", "/exit":
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
		if len(parts) == 1 {
			m.addSystem("Usage: /model <agent> <model>")
			m.addSystem("")
			for _, agentName := range []string{"claude", "opencode", "copilot"} {
				if models, ok := knownModels[agentName]; ok {
					m.addSystem(fmt.Sprintf("  %s:", agentName))
					for _, mdl := range models {
						m.addSystem(fmt.Sprintf("    %s", mdl))
					}
				}
			}
			return nil
		}
		if len(parts) == 2 {
			agentName := parts[1]
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
		agentName, modelName := parts[1], strings.Join(parts[2:], " ")
		if err := m.setAgentModel(agentName, modelName); err != nil {
			m.addError(err.Error())
			return nil
		}
		m.addSuccess(fmt.Sprintf("Set %s model → %s", agentName, modelName))

	case "/dense":
		if len(parts) == 1 {
			m.addSystem("Usage: /dense <agent> [level]")
			m.addSystem("Levels: " + strings.Join(knownDenseLevels, ", "))
			m.addSystem("")
			for _, agentName := range []string{"claude", "opencode", "copilot"} {
				if ac, ok := m.cfg.Agents[agentName]; ok {
					level := ac.Dense
					if level == "" {
						level = "(off)"
					}
					m.addSystem(fmt.Sprintf("  %s: %s", agentName, level))
				}
			}
			return nil
		}
		if len(parts) == 2 {
			m.addSystem(fmt.Sprintf("Usage: /dense %s <level>", parts[1]))
			m.addSystem("Levels: " + strings.Join(knownDenseLevels, ", "))
			return nil
		}
		agentName, level := parts[1], parts[2]
		if err := m.setAgentDense(agentName, level); err != nil {
			m.addError(err.Error())
			return nil
		}
		if level == "" {
			m.addSuccess(fmt.Sprintf("%s dense mode → off", agentName))
		} else {
			m.addSuccess(fmt.Sprintf("%s dense mode → %s", agentName, level))
		}

	case "/judge":
		if len(parts) < 2 {
			m.addError("Usage: /judge <agent>   e.g. /judge claude")
			return nil
		}
		if err := m.setJudge(parts[1]); err != nil {
			m.addError(err.Error())
			return nil
		}
		m.addSuccess("Judge agent → " + parts[1])

	default:
		m.addSystem("Unknown slash command. Try /help")
	}
	return nil
}

func (m *Model) handlePhaseResult(msg phaseResultMsg) tea.Cmd {
	switch msg.phase {
	case "bootstrap":
		return m.showWelcome()
	case "init":
		m.addSuccess("Project initialized: " + m.project.ProjectName)
		m.showHelp()
	case "plan_done":
		m.addSuccess("Plan approved.")
		m.addSystem("  → type: divide    (break plan into agent tasks)")
		send(agentActivityMsg{status: "clear"})
	case "divide_done":
		m.addSuccess("Tasks assigned.")
		m.addSystem("  → type: work    (execute all tasks)")
		send(agentActivityMsg{status: "clear"})
	case "work_done":
		m.addSuccess("All tasks complete!")
		m.addSystem("  → type: status    (review results)")
		send(agentActivityMsg{status: "clear"})
	}
	m.setMode(modeText, "")
	return nil
}

func (m *Model) showWelcome() tea.Cmd {
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
		return nil
	}

	m.addSystem("No project found here. Enter a project name to start:")
	m.setMode(modeText, "project name")
	return nil
}

func (m *Model) showHelp() {
	m.addSystem("Commands:")
	for _, l := range []string{
		"  plan <task>      generate multi-agent plan",
		"  divide           break plan into tasks",
		"  work             execute tasks",
		"  status           current state and tasks",
		"",
		"Slash commands:",
		"  /model <agent> <model>     change model for an agent",
		"  /dense <agent> [level]     set dense output (lite|full|ultra)",
		"  /judge <agent>             change which agent is the judge",
		"  /agents                    show all configured agents",
		"  /clear                     clear chat history",
		"  /help, /quit",
		"",
		"Phase shortcut: Shift+Tab (cycle init → plan → divide → work → done)",
		"Scrolling: pgup/pgdn  •  shift+up/down  •  mouse wheel",
		"Multi-line input: Shift+Enter",
	} {
		m.addSystem(l)
	}
}

func (m *Model) cyclePhase() {
	if m.project == nil {
		m.addSystem("No project loaded.")
		return
	}
	if m.busy {
		m.addSystem("Cannot change phase while a command is running.")
		return
	}

	order := []state.Phase{
		state.PhaseInit,
		state.PhasePlan,
		state.PhaseDivide,
		state.PhaseWork,
		state.PhaseDone,
	}
	next := order[0]
	for i, p := range order {
		if m.project.Phase == p {
			next = order[(i+1)%len(order)]
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

func (m *Model) showAgents() {
	if m.cfg == nil {
		return
	}
	m.addSystem(fmt.Sprintf("Judge: %s", m.cfg.Judge.Agent))
	m.addSystem("Agents:")
	for name, ac := range m.cfg.Agents {
		status := "off"
		if ac.Enabled {
			status = "on"
		}
		m.addSystem(fmt.Sprintf("  %-12s [%s]  model: %s", name, status, ac.Model))
	}
}

func (m *Model) showStatus() {
	if m.project == nil {
		m.addSystem("No project loaded.")
		return
	}
	m.addSystem(fmt.Sprintf("Project: %s  |  Phase: %s", m.project.ProjectName, m.project.Phase))
	if m.project.Task != "" {
		m.addSystem("Task: " + m.project.Task)
	}
	manifest, err := state.LoadManifest(m.arosDir)
	if err != nil || len(manifest.Tasks) == 0 {
		return
	}
	m.addSystem(fmt.Sprintf("%-12s %-28s %-12s %s", "ID", "Title", "Agent", "Status"))
	m.addSystem(strings.Repeat("─", 65))
	for _, t := range manifest.Tasks {
		icon := "○"
		if t.Status == state.TaskDone {
			icon = "✓"
		} else if t.Status == state.TaskInProgress {
			icon = "►"
		}
		title := t.Title
		if len(title) > 27 {
			title = title[:24] + "..."
		}
		m.addSystem(fmt.Sprintf("%s %-11s %-28s %-12s %s", icon, t.ID, title, t.AssignedTo, t.Status))
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

func (m *Model) initProject(name string) tea.Cmd {
	return func() tea.Msg {
		if err := os.MkdirAll(m.arosDir, 0755); err != nil {
			return phaseResultMsg{err: err}
		}
		s := &state.ProjectState{
			ProjectName: name,
			Phase:       state.PhaseInit,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}
		if err := state.SaveState(m.arosDir, s); err != nil {
			return phaseResultMsg{err: err}
		}
		_ = os.WriteFile(filepath.Join(m.arosDir, "config.toml"),
			[]byte("# Aros project config — see ~/.aros/config.toml for global defaults\n"), 0644)
		m.project = s
		return phaseResultMsg{phase: "init"}
	}
}

func (m *Model) setAgentModel(agentName, modelName string) error {
	if m.cfg.Agents == nil {
		return fmt.Errorf("no agents configured")
	}
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
	if m.cfg.Agents == nil {
		return fmt.Errorf("no agents configured")
	}
	ac, ok := m.cfg.Agents[agentName]
	if !ok {
		return fmt.Errorf("unknown agent %q", agentName)
	}
	found := false
	for _, valid := range knownDenseLevels {
		if level == valid {
			found = true
			break
		}
	}
	if !found {
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
	m.cfg.Judge.Agent = agentName
	return nil
}

func (m *Model) rebuildRegistry() error {
	reg, err := agent.BuildRegistry(m.cfg, m.cwd)
	if err != nil {
		return err
	}
	m.reg = reg
	return nil
}
