package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Rinil-Parmar/aros/state"
	"github.com/charmbracelet/lipgloss"
)

// ── Layout ─────────────────────────────────────────────────────────────────────

// recalcLayout computes leftW/rightW/colH and resizes the viewport.
func (m *Model) recalcLayout() {
	if m.width == 0 || m.height == 0 {
		return
	}

	approvalH := 0
	if m.mode == modeApproval {
		approvalH = 3
	}

	taH := m.textarea.Height()
	if taH < 1 {
		taH = 1
	}

	// header(2: text+border) + colH + approvalH + inputBox(taH+2) + shortcuts(1)
	m.colH = m.height - 5 - taH - approvalH
	if m.colH < 3 {
		m.colH = 3
	}

	if m.width >= 90 {
		m.leftW = (m.width * 68) / 100
		if m.leftW < 50 {
			m.leftW = 50
		}
		m.rightW = m.width - m.leftW - 1
		if m.rightW < 30 {
			m.rightW = 30
			m.leftW = m.width - m.rightW - 1
		}
	} else {
		m.leftW = m.width
		m.rightW = 0
	}

	m.viewport.Width = m.leftW - 2 // subtract rounded border sides
	m.viewport.Height = m.colH - 2 // subtract rounded border top/bottom
}

// ── Top-level View ─────────────────────────────────────────────────────────────

func (m *Model) View() string {
	if m.quitting {
		return "Goodbye.\n"
	}
	var out strings.Builder
	out.WriteString(m.renderHeader())
	out.WriteString("\n")
	out.WriteString(m.renderColumns())
	if m.mode == modeApproval {
		out.WriteString(m.renderApprovalCard())
	}
	out.WriteString(m.renderInputBox())
	out.WriteString(m.renderShortcutsBar())
	return out.String()
}

// ── Header ─────────────────────────────────────────────────────────────────────

func (m *Model) renderHeader() string {
	w := m.width
	if w < 20 {
		w = 80
	}

	brand := lipgloss.NewStyle().Foreground(colorBrand).Bold(true).Render(" ◆ AROS ")

	phase := state.PhaseInit
	if m.project != nil {
		phase = m.project.Phase
	}
	badge := phaseBadge(phase)

	projName := lipgloss.NewStyle().Foreground(colorSubtle).Render("no project")
	if m.project != nil {
		label := m.project.ProjectName
		if m.sessionName != "" && m.sessionName != m.project.ProjectName {
			label = fmt.Sprintf("%s  [%s]", label, m.sessionName)
		} else if m.sessionID != "" {
			// fall back to short ID (last 8 chars)
			short := m.sessionID
			if len(short) > 8 {
				short = short[len(short)-8:]
			}
			label = fmt.Sprintf("%s  [%s]", label, short)
		}
		projName = lipgloss.NewStyle().Foreground(colorText).Bold(true).Render(label)
	}

	brandW := lipgloss.Width(brand)
	projW := lipgloss.Width(projName)
	badgeW := lipgloss.Width(badge)
	padW := w - brandW - projW - badgeW - 4
	if padW < 0 {
		padW = 0
	}
	pad := strings.Repeat(" ", padW)

	content := brand + "  " + projName + pad + badge + "  "
	return styleHeader.Width(w - 2).Render(content)
}

// ── Two-column layout ──────────────────────────────────────────────────────────

func (m *Model) renderColumns() string {
	mainH := m.colH

	// Left panel — rounded box containing the chat viewport
	leftInner := m.viewport.View()
	left := stylePanelBox.
		Width(m.leftW).
		Height(mainH).
		Render(leftInner)

	if m.rightW == 0 {
		return left + "\n"
	}

	// Separator
	sep := styleVertSep.Render(verticalSep(mainH + 2))

	// Right panel — rounded box containing agents + task board
	right := stylePanelBox.
		Width(m.rightW).
		Height(mainH).
		Render(m.renderRightPanel())

	return lipgloss.JoinHorizontal(lipgloss.Top, left, sep, right) + "\n"
}

func verticalSep(lines int) string {
	if lines <= 0 {
		return ""
	}
	var b strings.Builder
	for i := 0; i < lines; i++ {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString("│")
	}
	return b.String()
}

// ── Right panel ────────────────────────────────────────────────────────────────

func (m *Model) renderRightPanel() string {
	w := m.rightW - 2 // subtract box padding
	if w < 8 {
		return ""
	}

	var sb strings.Builder

	// ── Activity (while busy) ─────────────────────────────────────────────────
	if m.busy && len(m.activity) > 0 {
		sb.WriteString(rpSectionTitle("Activity", w))

		names := make([]string, 0, len(m.activity))
		for n := range m.activity {
			names = append(names, n)
		}
		sort.Strings(names)

		for _, name := range names {
			a := m.activity[name]
			icon := m.spinner.View()
			if a.status == "done" {
				icon = styleActivityDone.Render("✓")
			} else if a.status == "error" {
				icon = styleActivityError.Render("✗")
			}
			nameStr := lipgloss.NewStyle().Foreground(agentColor(name)).Bold(true).Render(name)
			modelStr := ""
			if a.model != "" {
				modelStr = styleActivityModel.Render(" (" + truncate(a.model, 16) + ")")
			}
			sb.WriteString(" " + icon + " " + nameStr + modelStr + "\n")
			if a.line != "" {
				sb.WriteString(styleActivityLine.Render("   "+truncate(a.line, w-4)) + "\n")
			}
		}
		sb.WriteString("\n")
	}

	// ── Agents ────────────────────────────────────────────────────────────────
	sb.WriteString(rpSectionTitle("Agents", w))
	if m.cfg != nil {
		names := make([]string, 0, len(m.cfg.Agents))
		for n := range m.cfg.Agents {
			names = append(names, n)
		}
		sort.Strings(names)

		for _, name := range names {
			ac := m.cfg.Agents[name]
			if !ac.Enabled {
				continue
			}
			dot := lipgloss.NewStyle().Foreground(agentColor(name)).Render("●")
			nameStr := lipgloss.NewStyle().Foreground(agentColor(name)).Bold(true).Width(9).Render(name)
			judgeTag := ""
			if m.cfg.Judge.Agent == name {
				judgeTag = lipgloss.NewStyle().Foreground(colorJudge).Render("★ ")
			}
			modelStr := lipgloss.NewStyle().Foreground(colorSubtle).Render(truncate(ac.Model, max(4, w-14)))
			sb.WriteString(" " + dot + " " + nameStr + judgeTag + modelStr + "\n")
		}
	} else {
		sb.WriteString(styleActivityLine.Render(" loading…") + "\n")
	}
	sb.WriteString("\n")

	// ── Session / Task Board ──────────────────────────────────────────────────
	sb.WriteString(rpSectionTitle("Session", w))
	if m.project == nil {
		sb.WriteString(styleActivityLine.Render(" No project") + "\n")
		sb.WriteString(styleActivityLine.Render(" /session new <name>") + "\n")
		return sb.String()
	}
	sessLabel := m.project.ProjectName
	if m.sessionName != "" {
		sessLabel = m.sessionName
	}
	sb.WriteString(" " + lipgloss.NewStyle().Foreground(colorText).Render(truncate(sessLabel, w-2)) + "\n")
	if m.sessionID != "" {
		short := m.sessionID
		if len(short) > 13 {
			short = "…" + short[len(short)-12:]
		}
		sb.WriteString(styleActivityLine.Render(" "+short) + "\n")
	}
	phaseRow := " Phase  " + phaseBadge(m.project.Phase)
	sb.WriteString(phaseRow + "\n\n")

	sb.WriteString(rpSectionTitle("Tasks", w))
	manifest, err := state.LoadManifest(m.arosDir)
	if err != nil || len(manifest.Tasks) == 0 {
		if m.project.Task != "" {
			sb.WriteString(styleActivityLine.Render(" "+truncate(m.project.Task, w-2)) + "\n")
		} else {
			sb.WriteString(styleActivityLine.Render(" No tasks yet — run divide") + "\n")
		}
		return sb.String()
	}

	for _, t := range manifest.Tasks {
		badge := taskStatusBadge(t.Status)
		title := truncate(t.Title, max(4, w-14))
		titleStr := lipgloss.NewStyle().Foreground(colorText).Render(title)
		ownerStr := lipgloss.NewStyle().Foreground(agentColor(t.AssignedTo)).Render(truncate(t.AssignedTo, 9))
		sb.WriteString(fmt.Sprintf(" %s %s  %s\n", badge, titleStr, ownerStr))
		if len(t.Dependencies) > 0 {
			deps := "   deps: " + strings.Join(t.Dependencies, ", ")
			sb.WriteString(styleActivityLine.Render(truncate(deps, w-2)) + "\n")
		}
	}

	return sb.String()
}

func taskStatusBadge(s state.TaskStatus) string {
	switch s {
	case state.TaskDone:
		return styleTaskDone.Render("✓")
	case state.TaskInProgress:
		return styleTaskRun.Render("►")
	case state.TaskBlocked:
		return styleTaskBlocked.Render("✗")
	default:
		return styleTaskPending.Render("○")
	}
}

// rpSectionTitle renders "─── Title ────────────────\n"
func rpSectionTitle(title string, w int) string {
	label := " " + title + " "
	rest := w - lipgloss.Width(label) - 3
	if rest < 0 {
		rest = 0
	}
	return lipgloss.NewStyle().Foreground(colorMuted).
		Render(strings.Repeat("─", 3)+label+strings.Repeat("─", rest)) + "\n"
}

// ── Approval card ──────────────────────────────────────────────────────────────

func (m *Model) renderApprovalCard() string {
	w := m.width
	if w < 20 {
		w = 80
	}
	inner := w - 2

	q := m.approvalQuestion
	if q == "" {
		q = "Approve?"
	}
	q = truncate(q, max(4, inner-8))

	bc := styleApprovalBorder

	topLabel := "─ ? " + q + " "
	topPad := inner - lipgloss.Width(topLabel) - 1
	if topPad < 0 {
		topPad = 0
	}
	top := bc.Render("╭" + topLabel + strings.Repeat("─", topPad) + "╮")

	yesStr := styleApprovalYes.Render("[y]") + " Yes"
	noStr := styleApprovalNo.Render("[n]") + " No"
	contentInner := "  " + yesStr + "      " + noStr
	contentPad := inner - lipgloss.Width(contentInner) - 1
	if contentPad < 0 {
		contentPad = 0
	}
	mid := bc.Render("│") + contentInner + strings.Repeat(" ", contentPad) + bc.Render("│")
	bot := bc.Render("╰" + strings.Repeat("─", inner) + "╯")

	return top + "\n" + mid + "\n" + bot + "\n"
}

// ── Input box ──────────────────────────────────────────────────────────────────

func (m *Model) renderInputBox() string {
	prefix := styleInputPrefix.Render("❯")
	if m.busy {
		prefix = m.spinner.View()
	}

	hint := ""
	if m.mode == modeApproval {
		hint = styleSystemMsg.Render("(y/n) ")
	} else if m.prompt != "" && m.prompt != "y/n" {
		hint = styleSystemMsg.Render("(" + m.prompt + ") ")
	}

	taLines := strings.Split(m.textarea.View(), "\n")
	var lines []string
	for i, line := range taLines {
		if i == 0 {
			lines = append(lines, prefix+"  "+hint+line)
		} else {
			lines = append(lines, "     "+line)
		}
	}
	content := strings.Join(lines, "\n")

	boxStyle := styleInputBorderActive
	if m.busy {
		boxStyle = styleInputBorderBusy
	}

	return boxStyle.Width(m.width-4).Render(content) + "\n"
}

// ── Shortcuts bar ──────────────────────────────────────────────────────────────

func (m *Model) renderShortcutsBar() string {
	w := m.width
	if w < 20 {
		w = 80
	}

	shortcuts := []struct{ key, desc string }{
		{"Ctrl+H", "help"},
		{"Ctrl+K", "clear"},
		{"Ctrl+S", "status"},
		{"pgup/dn", "scroll"},
		{"wheel", "scroll"},
	}

	sep := lipgloss.NewStyle().Foreground(colorDim).Render("  ·  ")
	var parts []string
	for _, s := range shortcuts {
		k := styleShortcutKey.Render(s.key)
		d := lipgloss.NewStyle().Foreground(colorSubtle).Render(" " + s.desc)
		parts = append(parts, k+d)
	}

	content := "  " + strings.Join(parts, sep)
	return styleShortcutsBar.Width(w).Render(content)
}

// ── Phase badge ────────────────────────────────────────────────────────────────

func phaseBadge(phase state.Phase) string {
	colors := map[state.Phase]lipgloss.Color{
		state.PhaseInit:   "#6B5C8A",
		state.PhasePlan:   "#60A5FA",
		state.PhaseDivide: "#F59E0B",
		state.PhaseWork:   "#B084FF",
		state.PhaseDone:   "#34D399",
	}
	c := lipgloss.Color("#6B5C8A")
	if col, ok := colors[phase]; ok {
		c = col
	}
	return lipgloss.NewStyle().
		Background(c).
		Foreground(lipgloss.Color("#FFFFFF")).
		Bold(true).
		Padding(0, 1).
		Render(strings.ToUpper(string(phase)))
}

// ── Utility ────────────────────────────────────────────────────────────────────

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
