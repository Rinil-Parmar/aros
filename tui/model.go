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
	modeIdle     inputMode = iota
	modeText
	modeApproval
)

// Model is the root bubbletea model.
type Model struct {
	width, height int

	viewport viewport.Model
	input    textarea.Model
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

// program is set after bubbletea starts so goroutines can Send messages back.
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

	ta := textarea.New()
	ta.Placeholder = "Type a command..."
	ta.ShowLineNumbers = false
	ta.SetHeight(3)
	ta.Focus()

	vp := viewport.New(80, 20)

	return &Model{
		spinner:  sp,
		input:    ta,
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
		switch msg.String() {
		case "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		case "enter":
			if m.busy {
				break
			}
			text := strings.TrimSpace(m.input.Value())
			m.input.Reset()
			if text != "" {
				cmds = append(cmds, m.handleInput(text))
			}
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.viewport.Width = msg.Width
		m.viewport.Height = msg.Height - 9
		m.input.SetWidth(msg.Width - 4)
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

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	cmds = append(cmds, cmd)
	m.viewport, cmd = m.viewport.Update(msg)
	cmds = append(cmds, cmd)
	m.refreshViewport()

	return m, tea.Batch(cmds...)
}

func (m *Model) handleInput(text string) tea.Cmd {
	m.addHuman(text)

	switch m.mode {
	case modeApproval:
		m.setMode(modeIdle, "")
		lower := strings.ToLower(text)
		if lower == "y" || lower == "yes" {
			if m.onYes != nil {
				go m.onYes()
			}
		} else {
			if m.onNo != nil {
				go m.onNo()
			}
		}

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
	lower := strings.ToLower(strings.TrimSpace(text))

	if m.project == nil {
		return m.initProject(text)
	}

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
		task := strings.TrimSpace(strings.TrimPrefix(lower, "plan"))
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
		m.addSystem("Unknown command. Type 'help' for commands.")
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
	banner := styleBanner.Render(`
 █████╗ ██████╗  ██████╗ ███████╗
██╔══██╗██╔══██╗██╔═══██╗██╔════╝
███████║██████╔╝██║   ██║███████╗
██╔══██║██╔══██╗██║   ██║╚════██║
██║  ██║██║  ██║╚██████╔╝███████║
╚═╝  ╚═╝╚═╝  ╚═╝ ╚═════╝ ╚══════╝`)
	m.messages = append(m.messages, ChatMessage{Kind: kindSystem, Text: banner})
	m.addSystem("Multi-agent AI orchestrator  •  v0.1.0")
	m.addSystem(strings.Repeat("─", 50))

	if m.project != nil {
		m.addSystem(fmt.Sprintf("Resuming: %s  [phase: %s]", m.project.ProjectName, m.project.Phase))
		m.showHelp()
		m.setMode(modeText, "")
		return nil
	}

	m.addSystem("No project found here. Enter a project name to start:")
	m.setMode(modeText, "project name")
	return nil
}

func (m *Model) showHelp() {
	lines := []string{
		"  plan <task>   —  generate a multi-agent plan",
		"  divide        —  break plan into tasks and assign agents",
		"  work          —  execute all tasks",
		"  status        —  show current state and task list",
		"  ctrl+c        —  quit",
	}
	m.addSystem("Commands:")
	for _, l := range lines {
		m.addSystem(l)
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

	var sb strings.Builder

	// Phase bar
	phaseLabel := " AROS "
	if m.project != nil {
		phaseLabel = fmt.Sprintf(" AROS  ·  %s  ·  %s ", m.project.ProjectName, strings.ToUpper(string(m.project.Phase)))
	}
	bar := stylePhaseBar.Render(phaseLabel)
	if m.width > 0 {
		bar = stylePhaseBar.Width(m.width).Render(phaseLabel)
	}
	sb.WriteString(bar + "\n")
	sb.WriteString(m.viewport.View() + "\n")
	sb.WriteString(styleDivider.Render(strings.Repeat("─", m.width)) + "\n")

	if m.busy {
		sb.WriteString(m.spinner.View() + " agents working...\n")
	} else {
		label := "you"
		if m.prompt != "" {
			label = m.prompt
		}
		sb.WriteString(styleInputPrefix.Render("["+label+"]") + "\n")
	}
	sb.WriteString(m.input.View())

	return sb.String()
}

// --- message helpers ---

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
		Text: styleHumanMsg.Render("[you]") + "         " + text,
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
