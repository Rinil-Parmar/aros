package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Rinil-Parmar/aros/state"
	"github.com/charmbracelet/lipgloss"
)

// recalcLayout recomputes viewport dimensions based on current window size and
// which dynamic panels (activity, approval card) are visible.
func (m *Model) recalcLayout() {
	if m.width == 0 || m.height == 0 {
		return
	}

	actH := 0
	if m.busy && len(m.activity) > 0 {
		actH = 1 + len(m.activity) // 1 separator + 1 row per agent
	}
	approvalH := 0
	if m.mode == modeApproval {
		approvalH = 3 // ╭ + content + ╰
	}

	// header(1) + gap(1) + actH + approvalH + inputBox(3) + statusBar(1) = 6 + actH + approvalH
	m.viewport.Width = m.width
	m.viewport.Height = m.height - 6 - actH - approvalH
	if m.viewport.Height < 3 {
		m.viewport.Height = 3
	}
}

// View composes the full TUI from its zones.
func (m *Model) View() string {
	if m.quitting {
		return "Goodbye.\n"
	}
	var out strings.Builder
	out.WriteString(m.renderHeader())
	out.WriteString(m.viewport.View())
	out.WriteString("\n") // gap between viewport and activity/input
	if m.busy && len(m.activity) > 0 {
		out.WriteString(m.renderActivity())
	}
	if m.mode == modeApproval {
		out.WriteString(m.renderApprovalCard())
	}
	out.WriteString(m.renderInputBox())
	out.WriteString(m.renderStatusBar())
	return out.String()
}

// renderHeader returns a single full-width header bar.
//
//	◆ AROS  ·  my-project  ·  [PLAN]
func (m *Model) renderHeader() string {
	w := m.width
	if w < 20 {
		w = 80
	}

	brand := "◆ AROS"
	projectPart := styleSystemMsg.Render("no project")
	if m.project != nil {
		projectPart = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFFFF")).Render(m.project.ProjectName)
	}

	phase := state.PhaseInit
	if m.project != nil {
		phase = m.project.Phase
	}
	badge := phaseBadge(phase)

	// Compose: brand · project · badge, padded to full width
	label := fmt.Sprintf(" %s  ·  %s  ·  %s ", brand, projectPart, badge)
	return stylePhaseBar.Width(w).Render(label) + "\n"
}

// renderActivity renders the live per-agent activity panel.
func (m *Model) renderActivity() string {
	w := m.width
	if w < 20 {
		w = 80
	}

	// Separator line with "Activity" label
	label := " Activity "
	sideLen := (w - len(label) - 2) / 2
	if sideLen < 1 {
		sideLen = 1
	}
	sep := styleActivitySep.Render(strings.Repeat("─", sideLen) + label + strings.Repeat("─", w-sideLen-len(label)))
	var sb strings.Builder
	sb.WriteString(sep + "\n")

	// Sort agents for stable order
	names := make([]string, 0, len(m.activity))
	for name := range m.activity {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		a := m.activity[name]

		// Status icon
		var icon string
		switch a.status {
		case "done":
			icon = styleActivityDone.Render("✓")
		case "error":
			icon = styleActivityError.Render("✗")
		default:
			icon = m.spinner.View()
		}

		// Agent name (colored, fixed width)
		agentCol := lipgloss.NewStyle().
			Foreground(agentColor(name)).
			Bold(true).
			Width(10).
			Render(name)

		// Model (subtle, in parens, fixed width)
		model := a.model
		if model == "" {
			model = "—"
		}
		modelCol := styleActivityModel.Width(20).Render("(" + model + ")")

		// Latest line (truncated, subtle italic)
		line := a.line
		lineCol := styleActivityLine.Render(line)

		sb.WriteString(fmt.Sprintf("  %s  %s %s  %s\n", icon, agentCol, modelCol, lineCol))
	}

	return sb.String()
}

// renderApprovalCard renders a 3-line card for y/n approval prompts.
func (m *Model) renderApprovalCard() string {
	w := m.width
	if w < 20 {
		w = 80
	}
	inner := w - 4 // 2 border chars + 2 padding chars

	q := m.approvalQuestion
	if q == "" {
		q = "Approve?"
	}

	borderColor := colorWarning
	hBar := strings.Repeat("─", inner)

	top := lipgloss.NewStyle().Foreground(borderColor).Render("╭─") +
		lipgloss.NewStyle().Foreground(colorWarning).Bold(true).Render(" ? "+q+" ") +
		lipgloss.NewStyle().Foreground(borderColor).Render(strings.Repeat("─", max(0, inner-len(q)-5))+"╮")

	content := lipgloss.NewStyle().Foreground(borderColor).Render("│") +
		"  " +
		styleApprovalYes.Render("[y]") + " Yes     " +
		styleApprovalNo.Render("[n]") + " No" +
		strings.Repeat(" ", max(0, inner-16)) +
		lipgloss.NewStyle().Foreground(borderColor).Render("│")

	_ = hBar
	bottom := lipgloss.NewStyle().Foreground(borderColor).Render("╰" + strings.Repeat("─", inner+2) + "╯")

	return top + "\n" + content + "\n" + bottom + "\n"
}

// renderInputBox renders a 3-line bordered input box.
func (m *Model) renderInputBox() string {
	w := m.width
	if w < 20 {
		w = 80
	}
	inner := w - 2 // width inside the border

	borderColor := colorBrand
	if m.busy {
		borderColor = colorMuted
	}

	hBar := strings.Repeat("─", inner-2)
	top := lipgloss.NewStyle().Foreground(borderColor).Render("╭" + hBar + "╮")

	// Build input content
	prefix := styleInputPrefix.Render("❯")
	if m.busy {
		prefix = m.spinner.View()
	}
	hint := ""
	if m.mode == modeApproval {
		hint = styleSystemMsg.Render("(y/n)  ")
	} else if m.prompt != "" && m.prompt != "y/n" {
		hint = styleSystemMsg.Render("(" + m.prompt + ")  ")
	}
	inputContent := fmt.Sprintf(" %s  %s%s", prefix, hint, m.input.View())

	mid := lipgloss.NewStyle().Foreground(borderColor).Render("│") +
		inputContent +
		lipgloss.NewStyle().Foreground(borderColor).Render("│")

	bottom := lipgloss.NewStyle().Foreground(borderColor).Render("╰" + hBar + "╯")

	return top + "\n" + mid + "\n" + bottom + "\n"
}

// renderStatusBar renders the bottom 1-line status bar.
func (m *Model) renderStatusBar() string {
	w := m.width
	if w < 20 {
		w = 80
	}

	if m.cfg == nil {
		return styleStatusBar.Width(w).Render("  loading...")
	}

	// Sorted agent list
	agentNames := make([]string, 0, len(m.cfg.Agents))
	for name := range m.cfg.Agents {
		agentNames = append(agentNames, name)
	}
	sort.Strings(agentNames)

	sep := styleStatusSep.String()
	var parts []string
	for _, name := range agentNames {
		ac := m.cfg.Agents[name]
		if !ac.Enabled {
			continue
		}
		nameStyled := lipgloss.NewStyle().
			Background(colorSurface).
			Foreground(agentColor(name)).
			Bold(true).
			Render(name)
		modelStyled := lipgloss.NewStyle().
			Background(colorSurface).
			Foreground(colorSubtle).
			Render(":" + ac.Model)
		parts = append(parts, nameStyled+modelStyled)
	}

	phase := state.PhaseInit
	if m.project != nil {
		phase = m.project.Phase
	}
	badge := phaseBadge(phase)

	content := "  " + strings.Join(parts, sep) + "  " + badge
	return styleStatusBar.Width(w).Render(content)
}

// phaseBadge returns a colored pill label for the given phase.
func phaseBadge(phase state.Phase) string {
	phaseColors := map[state.Phase]string{
		state.PhaseInit:   "#6B7280",
		state.PhasePlan:   "#3B82F6",
		state.PhaseDivide: "#F59E0B",
		state.PhaseWork:   "#8B5CF6",
		state.PhaseDone:   "#22C55E",
	}
	c := "#6B7280"
	if col, ok := phaseColors[phase]; ok {
		c = col
	}
	return lipgloss.NewStyle().
		Background(lipgloss.Color(c)).
		Foreground(lipgloss.Color("#FFFFFF")).
		Bold(true).
		Padding(0, 1).
		Render(strings.ToUpper(string(phase)))
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
