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
// Called on every window resize, mode change, or approval card toggle.
func (m *Model) recalcLayout() {
	if m.width == 0 || m.height == 0 {
		return
	}

	approvalH := 0
	if m.mode == modeApproval {
		approvalH = 3 // ╭ + content + ╰
	}

	// header(1) + columns(colH) + approvalH + inputBox(3) + shortcuts(1) = 5 + approvalH
	m.colH = m.height - 5 - approvalH
	if m.colH < 3 {
		m.colH = 3
	}

	// Two-column only on wide terminals; fall back to single column below 100 chars
	if m.width >= 100 {
		m.leftW = (m.width * 66) / 100
		m.rightW = m.width - m.leftW - 1 // -1 for │ separator
	} else {
		m.leftW = m.width
		m.rightW = 0
	}

	m.viewport.Width = m.leftW
	m.viewport.Height = m.colH
}

// ── Top-level View ─────────────────────────────────────────────────────────────

func (m *Model) View() string {
	if m.quitting {
		return "Goodbye.\n"
	}
	var out strings.Builder
	out.WriteString(m.renderHeader())
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

	// Brand mark
	brand := lipgloss.NewStyle().
		Background(colorBrand).
		Foreground(lipgloss.Color("#FFFFFF")).
		Bold(true).
		Padding(0, 1).
		Render(" ◆ AROS ")

	// Project name
	projName := lipgloss.NewStyle().
		Background(colorSurface).
		Foreground(colorSubtle).
		Render("  no project")
	if m.project != nil {
		projName = lipgloss.NewStyle().
			Background(colorSurface).
			Foreground(colorText).
			Bold(true).
			Render("  " + m.project.ProjectName)
	}

	// Phase badge — right-aligned
	phase := state.PhaseInit
	if m.project != nil {
		phase = m.project.Phase
	}
	badge := "  " + phaseBadge(phase) + " "

	// Pad middle to fill width
	brandLen := lipgloss.Width(brand)
	projLen := lipgloss.Width(projName)
	badgeLen := lipgloss.Width(badge)
	padLen := w - brandLen - projLen - badgeLen
	if padLen < 0 {
		padLen = 0
	}
	pad := lipgloss.NewStyle().Background(colorSurface).Render(strings.Repeat(" ", padLen))

	return brand + projName + pad + badge + "\n"
}

// ── Two-column layout ──────────────────────────────────────────────────────────

func (m *Model) renderColumns() string {
	if m.rightW == 0 {
		// Single column — narrow terminal
		return m.viewport.View() + "\n"
	}

	// Left panel: chat viewport, constrained to leftW×colH
	leftContent := lipgloss.NewStyle().
		Width(m.leftW).
		Height(m.colH).
		Render(m.viewport.View())

	// Separator: colH lines of │
	var sepLines []string
	for i := 0; i < m.colH; i++ {
		sepLines = append(sepLines, styleVertSep.Render("│"))
	}
	sep := strings.Join(sepLines, "\n")

	// Right panel: agent activity + config, constrained to rightW×colH
	rightContent := lipgloss.NewStyle().
		Width(m.rightW).
		Height(m.colH).
		Background(colorSurface).
		Render(m.renderRightPanel())

	return lipgloss.JoinHorizontal(lipgloss.Top, leftContent, sep, rightContent) + "\n"
}

// ── Right panel ────────────────────────────────────────────────────────────────

func (m *Model) renderRightPanel() string {
	w := m.rightW
	if w < 8 {
		return ""
	}

	var sb strings.Builder

	// ── Activity section (when busy) ──────────────────────────────────────────
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

			nameStr := lipgloss.NewStyle().
				Foreground(agentColor(name)).
				Bold(true).
				Render(name)

			modelStr := ""
			if a.model != "" {
				modelStr = styleActivityModel.Render(" (" + a.model + ")")
			}

			row := fmt.Sprintf(" %s %s%s", icon, nameStr, modelStr)
			sb.WriteString(rpLine(row, w))

			if a.line != "" {
				maxL := w - 4
				line := a.line
				if len(line) > maxL {
					line = line[:maxL-1] + "…"
				}
				sb.WriteString(rpLine(styleActivityLine.Render("   "+line), w))
			}
		}
		sb.WriteString(rpBlank(w))
	}

	// ── Agents section ────────────────────────────────────────────────────────
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
			nameStr := lipgloss.NewStyle().
				Foreground(agentColor(name)).
				Bold(true).
				Width(9).
				Render(name)

			model := ac.Model
			maxM := w - 14
			if len(model) > maxM && maxM > 3 {
				model = model[:maxM-1] + "…"
			}
			modelStr := lipgloss.NewStyle().Foreground(colorSubtle).Render(model)

			judgeTag := ""
			if m.cfg.Judge.Agent == name {
				judgeTag = lipgloss.NewStyle().Foreground(colorJudge).Render("★ ")
			}

			row := fmt.Sprintf(" %s %s%s%s", dot, nameStr, judgeTag, modelStr)
			sb.WriteString(rpLine(row, w))
		}
	} else {
		sb.WriteString(rpLine(styleActivityLine.Render(" loading…"), w))
	}
	sb.WriteString(rpBlank(w))

	// ── Project section ───────────────────────────────────────────────────────
	sb.WriteString(rpSectionTitle("Project", w))
	if m.project != nil {
		phaseRow := fmt.Sprintf(" Phase  %s", phaseBadge(m.project.Phase))
		sb.WriteString(rpLine(phaseRow, w))

		if m.project.Task != "" {
			task := m.project.Task
			maxT := w - 8
			if len(task) > maxT && maxT > 3 {
				task = task[:maxT-1] + "…"
			}
			sb.WriteString(rpLine(styleActivityLine.Render(" Task   "+task), w))
		}
	} else {
		sb.WriteString(rpLine(styleActivityLine.Render(" No project"), w))
		sb.WriteString(rpLine(styleActivityLine.Render(" Enter a name to init"), w))
	}

	return sb.String()
}

// rpSectionTitle renders "─── Title ───────────────────\n" in the right panel.
func rpSectionTitle(title string, w int) string {
	label := " " + title + " "
	rest := w - len(label)
	if rest < 0 {
		rest = 0
	}
	return lipgloss.NewStyle().Background(colorSurface).Foreground(colorMuted).
		Render(strings.Repeat("─", 1)+label+strings.Repeat("─", rest)) + "\n"
}

// rpLine pads a line to fill rightW and adds background color.
func rpLine(content string, w int) string {
	// We can't easily measure ANSI-escaped content width, so we let lipgloss Width handle it
	return lipgloss.NewStyle().Background(colorSurface).Width(w).Render(content) + "\n"
}

// rpBlank returns an empty padded line for spacing.
func rpBlank(w int) string {
	return lipgloss.NewStyle().Background(colorSurface).Width(w).Render("") + "\n"
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
	// Truncate if too long
	maxQ := inner - 8
	if len(q) > maxQ && maxQ > 3 {
		q = q[:maxQ-1] + "…"
	}

	bc := styleApprovalBorder

	topLabel := "─ ? " + q + " "
	topPad := inner - len(topLabel) - 1
	if topPad < 0 {
		topPad = 0
	}
	top := bc.Render("╭"+topLabel+strings.Repeat("─", topPad)+"╮")

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
	w := m.width
	if w < 20 {
		w = 80
	}
	inner := w - 2

	var bc lipgloss.Style
	if m.busy {
		bc = styleInputBorderBusy
	} else {
		bc = styleInputBorderActive
	}

	hBar := strings.Repeat("─", inner)
	top := bc.Render("╭" + hBar + "╮")

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
	// Clamp to inner width so long input never overflows the right border.
	mid := bc.Render("│") + lipgloss.NewStyle().Width(inner).MaxWidth(inner).Render(inputContent) + bc.Render("│")

	bot := bc.Render("╰" + hBar + "╯")

	return top + "\n" + mid + "\n" + bot + "\n"
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

	sep := lipgloss.NewStyle().Background(colorSurface).Foreground(colorDim).Render("  ·  ")
	var parts []string
	for _, s := range shortcuts {
		k := styleShortcutKey.Render(s.key)
		d := lipgloss.NewStyle().Background(colorSurface).Foreground(colorSubtle).Render(" " + s.desc)
		parts = append(parts, k+d)
	}

	content := "  " + strings.Join(parts, sep)
	return styleShortcutsBar.Width(w).Render(content)
}

// ── Phase badge ────────────────────────────────────────────────────────────────

func phaseBadge(phase state.Phase) string {
	colors := map[state.Phase]string{
		state.PhaseInit:   "#6B7280",
		state.PhasePlan:   "#3B82F6",
		state.PhaseDivide: "#F59E0B",
		state.PhaseWork:   "#8B5CF6",
		state.PhaseDone:   "#22C55E",
	}
	c := "#6B7280"
	if col, ok := colors[phase]; ok {
		c = col
	}
	return lipgloss.NewStyle().
		Background(lipgloss.Color(c)).
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

// renderStatusBar kept for any legacy call sites.
func (m *Model) renderStatusBar() string {
	return m.renderShortcutsBar()
}
