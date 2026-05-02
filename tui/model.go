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
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type inputMode int

const (
	modeIdle     inputMode = iota
	modeText
	modeApproval
)

type Model struct {
	width, height int

	viewport viewport.Model
	input    textinput.Model
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
}

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
	sp.Style = lipgloss.NewStyle().Foreground(colorPrimary)

	ti := textinput.New()
	ti.Placeholder = "Type a command (try: /help)"
	ti.Prompt = ""
	ti.CharLimit = 0
	ti.Focus()

	vp := viewport.New(80, 20)
	vp.MouseWheelEnabled = true

	return &Model{
		spinner:  sp,
		input:    ti,
		viewport: vp,
		mode:     modeText,
	}
}

func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, textinput.Blink, m.bootstrap())
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

		// Always-active keys
		switch key {
		case "ctrl+c", "ctrl+d":
			m.quitting = true
			return m, tea.Quit
		}

		// Scroll keys: route to viewport, NOT input
		switch key {
		case "pgup", "pgdown", "ctrl+u", "ctrl+d", "home", "end":
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
		case "shift+up":
			m.viewport.LineUp(1)
			return m, nil
		case "shift+down":
			m.viewport.LineDown(1)
			return m, nil
		}

		// Enter → submit
		if key == "enter" {
			if m.busy {
				return m, nil
			}
			text := strings.TrimSpace(m.input.Value())
			m.input.SetValue("")
			if text != "" {
				cmds = append(cmds, m.handleInput(text))
			}
			return m, tea.Batch(cmds...)
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.viewport.Width = msg.Width
		m.viewport.Height = msg.Height - 5
		m.input.Width = msg.Width - 8
		m.refreshViewport()

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
		m.addSystem(msg.question + "  [y/n]")
		m.setMode(modeApproval, "y/n")

	case freeInputMsg:
		m.onFreeText = msg.callback
		m.addSystem(msg.prompt)
		m.setMode(modeText, msg.prompt)
	}

	// Pass to input for typing
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}

func (m *Model) handleInput(text string) tea.Cmd {
	m.addHuman(text)

	switch m.mode {
	case modeApproval:
		m.setMode(modeIdle, "")
		lower := strings.ToLower(text)
		if lower == "y" || lower == "yes" {
			cb := m.onYes
			m.onYes = nil
			m.onNo = nil
			if cb != nil {
				go cb()
			}
		} else {
			cb := m.onNo
			m.onYes = nil
			m.onNo = nil
			if cb != nil {
				go cb()
			}
		}
		return nil

	case modeText:
		if m.onFreeText != nil {
			cb := m.onFreeText
			m.onFreeText = nil
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
		m.addSystem("Unknown command. Type /help for commands.")
	}
	return nil
}

// handleSlash routes /commands like /model, /agents, /help, /quit.
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
			for _, agentName := range []string{"claude", "opencode"} {
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
		if len(parts) == 2 && parts[1] == "show" {
			m.showAgents()
			return nil
		}
		agentName, modelName := parts[1], strings.Join(parts[2:], " ")
		if err := m.setAgentModel(agentName, modelName); err != nil {
			m.addError(err.Error())
			return nil
		}
		m.addSuccess(fmt.Sprintf("Set %s model → %s", agentName, modelName))

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
		m.addSuccess("Plan approved! Type 'divide' to assign tasks.")
	case "divide_done":
		m.addSuccess("Tasks assigned! Type 'work' to start execution.")
	case "work_done":
		m.addSuccess("All tasks complete!")
	}
	m.setMode(modeText, "")
	return nil
}

func (m *Model) showWelcome() tea.Cmd {
	w := m.width
	if w == 0 {
		w = 80
	}
	bannerText := `   █████╗ ██████╗  ██████╗ ███████╗     ██████╗██╗     ██╗
  ██╔══██╗██╔══██╗██╔═══██╗██╔════╝    ██╔════╝██║     ██║
  ███████║██████╔╝██║   ██║███████╗    ██║     ██║     ██║
  ██╔══██║██╔══██╗██║   ██║╚════██║    ██║     ██║     ██║
  ██║  ██║██║  ██║╚██████╔╝███████║    ╚██████╗███████╗██║
  ╚═╝  ╚═╝╚═╝  ╚═╝ ╚═════╝ ╚══════╝     ╚═════╝╚══════╝╚═╝`
	banner := lipgloss.NewStyle().Width(w).Align(lipgloss.Center).Render(styleBanner.Render(bannerText))
	m.messages = append(m.messages, ChatMessage{Kind: kindSystem, Text: banner})
	subtitle := lipgloss.NewStyle().Width(w).Align(lipgloss.Center).Render(
		styleSystemMsg.Render("Multi-agent AI orchestrator  •  v0.1.0"),
	)
	m.messages = append(m.messages, ChatMessage{Kind: kindSystem, Text: subtitle})
	m.addSystem(strings.Repeat("─", min(w, 65)))

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
		"  /judge <agent>             change which agent is the judge",
		"  /agents                    show all configured agents",
		"  /clear                     clear chat history",
		"  /help, /quit",
		"",
		"Scrolling: pgup/pgdn  •  shift+up/down  •  mouse wheel",
	} {
		m.addSystem(l)
	}
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

func (m *Model) View() string {
	if m.quitting {
		return "Goodbye.\n"
	}

	w := m.width
	if w < 20 {
		w = 80
	}

	// Top phase bar
	phaseLabel := " AROS CLI "
	if m.project != nil {
		phaseLabel = fmt.Sprintf(" ◆ AROS  ·  %s  ·  %s ", m.project.ProjectName, strings.ToUpper(string(m.project.Phase)))
	}
	topBar := stylePhaseBar.Width(w).Render(phaseLabel)

	// Divider
	divider := styleDivider.Render(strings.Repeat("─", w))

	// Input prefix
	prefix := styleInputPrefix.Render("❯")
	if m.busy {
		prefix = m.spinner.View()
	}
	hint := ""
	if m.prompt != "" && m.prompt != "y/n" {
		hint = styleSystemMsg.Render("(" + m.prompt + ")  ")
	}
	if m.mode == modeApproval {
		hint = styleSystemMsg.Render("(y/n)  ")
	}
	inputRow := fmt.Sprintf(" %s  %s%s", prefix, hint, m.input.View())

	// Bottom status bar
	statusBar := m.renderStatusBar(w)

	return topBar + "\n" + m.viewport.View() + "\n" + divider + "\n" + inputRow + "\n" + statusBar
}

func (m *Model) renderStatusBar(w int) string {
	if m.cfg == nil {
		return styleStatusBar.Width(w).Render("loading...")
	}

	var parts []string
	judge := styleStatusKey.Render("judge") + " " + m.cfg.Judge.Agent

	agentNames := make([]string, 0, len(m.cfg.Agents))
	for name := range m.cfg.Agents {
		agentNames = append(agentNames, name)
	}
	// stable sort
	for i := 0; i < len(agentNames)-1; i++ {
		for j := i + 1; j < len(agentNames); j++ {
			if agentNames[i] > agentNames[j] {
				agentNames[i], agentNames[j] = agentNames[j], agentNames[i]
			}
		}
	}

	for _, name := range agentNames {
		ac := m.cfg.Agents[name]
		if !ac.Enabled {
			continue
		}
		label := styleStatusKey.Render(name) + " " + ac.Model
		parts = append(parts, label)
	}

	sep := styleStatusSep.String()
	content := judge + sep + strings.Join(parts, sep)
	if len(parts) == 0 {
		content = judge
	}
	return styleStatusBar.Width(w).Render(content)
}

func (m *Model) addSystem(text string) {
	m.messages = append(m.messages, ChatMessage{Kind: kindSystem, Text: styleSystemMsg.Render(text)})
	m.refreshViewport()
}

func (m *Model) addAgent(name, text string) {
	m.messages = append(m.messages, ChatMessage{
		Kind:      kindAgent,
		AgentName: name,
		Text:      agentLabel(name) + " " + text,
	})
	m.refreshViewport()
}

func (m *Model) appendStream(name, line string) {
	if len(m.messages) > 0 {
		last := &m.messages[len(m.messages)-1]
		if last.Kind == kindAgent && last.AgentName == name {
			last.Text += "\n" + strings.Repeat(" ", 13) + line
			m.refreshViewport()
			return
		}
	}
	m.addAgent(name, line)
}

func (m *Model) addHuman(text string) {
	m.messages = append(m.messages, ChatMessage{
		Kind: kindHuman,
		Text: styleHumanMsg.Render("[you]") + "        " + text,
	})
	m.refreshViewport()
}

func (m *Model) addError(text string) {
	m.messages = append(m.messages, ChatMessage{Kind: kindError, Text: styleErrorMsg.Render("✗ " + text)})
	m.refreshViewport()
}

func (m *Model) addSuccess(text string) {
	m.messages = append(m.messages, ChatMessage{Kind: kindSuccess, Text: styleSuccessMsg.Render("✓ " + text)})
	m.refreshViewport()
}

func (m *Model) refreshViewport() {
	var lines []string
	for _, msg := range m.messages {
		lines = append(lines, msg.Text)
	}
	m.viewport.SetContent(strings.Join(lines, "\n"))
	m.viewport.GotoBottom()
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

// setAgentModel updates the model for an agent and rebuilds the registry.
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
