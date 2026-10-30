package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Rinil-Parmar/aros/state"
	"github.com/charmbracelet/lipgloss"
)

// ── Layout contract ────────────────────────────────────────────────────────────
//
// The frame must occupy exactly m.width columns and m.height rows. One column
// too wide makes the terminal wrap every row; one row too tall makes it scroll.
// Either shreds the layout, which is what "the TUI looks broken" means in
// practice. TestViewFitsTerminalExactly pins this at seven terminal sizes.
//
// The arithmetic that is easy to get wrong: for a lipgloss style *with* a
// border, Width(n)/Height(n) set the content box, and the border adds 2 on each
// axis. So a panel that must occupy L columns is rendered with Width(L-2).
//
// Row budget:   header 1 + columns + approval(0|3) + input(taH+2) + status 1
// Column budget (wide): left L + gap 1 + right R = width

const (
	// twoColumnMin is the width at which the right panel earns its space.
	twoColumnMin = 90
	// rightPanelWidth is fixed: the task board needs a predictable column, and
	// a proportional panel gets uselessly wide on a 200-column terminal.
	rightPanelWidth    = 34
	rightPanelWidthMax = 44
	minLeftWidth       = 48
)

// recalcLayout computes the column/row budget and resizes the viewport.
func (m *Model) recalcLayout() {
	if m.width == 0 || m.height == 0 {
		return
	}

	approvalRows := 0
	if m.mode == modeApproval {
		approvalRows = 3
	}
	taH := m.textarea.Height()
	if taH < 1 {
		taH = 1
	}

	// Rows the two columns may occupy, border included.
	m.colH = m.height - 1 - approvalRows - (taH + 2) - 1
	if m.colH < 3 {
		m.colH = 3
	}

	if m.width >= twoColumnMin {
		m.rightW = rightPanelWidth
		// Give the right panel a little more on very wide terminals, but cap it.
		if extra := (m.width - twoColumnMin) / 6; extra > 0 {
			m.rightW += extra
			if m.rightW > rightPanelWidthMax {
				m.rightW = rightPanelWidthMax
			}
		}
		m.leftW = m.width - m.rightW - 1 // 1 column gap
		if m.leftW < minLeftWidth {
			m.leftW = minLeftWidth
			m.rightW = m.width - m.leftW - 1
		}
	} else {
		m.leftW = m.width
		m.rightW = 0
	}

	// Viewport sits inside the left panel's border.
	m.viewport.Width = m.leftW - 2
	m.viewport.Height = m.colH - 2
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
	out.WriteString(m.renderStatusBar())

	// Final clamp. The arithmetic above is meant to be exact; this guarantees
	// the contract even if a renderer misbehaves, so a layout bug degrades to a
	// clipped panel instead of a wrapped, scrolling mess.
	return fitFrame(out.String(), m.width, m.height)
}

// fitFrame forces s to exactly w columns and h rows.
func fitFrame(s string, w, h int) string {
	if w <= 0 || h <= 0 {
		return s
	}
	lines := strings.Split(strings.TrimSuffix(s, "\n"), "\n")
	for len(lines) < h {
		lines = append(lines, "")
	}
	lines = lines[:h]
	for i, line := range lines {
		switch n := lipgloss.Width(line); {
		case n < w:
			lines[i] = line + strings.Repeat(" ", w-n)
		case n > w:
			lines[i] = lipgloss.NewStyle().MaxWidth(w).Render(line)
		}
	}
	return strings.Join(lines, "\n")
}

// ── Header ─────────────────────────────────────────────────────────────────────

func (m *Model) renderHeader() string {
	w := m.width
	brand := styleBrand.Render("◆ AROS")

	right := phaseBadge(state.PhaseInit)
	if m.project != nil {
		right = phaseBadge(m.project.Phase)
	}
	if prog := m.taskProgress(); prog != "" {
		right = styleHeaderMeta.Render(prog+"  ") + right
	}

	middle := styleHeaderDim.Render("no project")
	if m.project != nil {
		label := m.project.ProjectName
		// The session name only adds information when it differs.
		if m.sessionName != "" && m.sessionName != m.project.ProjectName {
			label += styleHeaderDim.Render("  ·  " + m.sessionName)
		}
		middle = styleHeaderName.Render(label)
	}

	// Width(w) on a border-less style yields exactly w columns; padding is inside.
	gap := w - lipgloss.Width(brand) - lipgloss.Width(middle) - lipgloss.Width(right) - 4
	if gap < 1 {
		gap = 1
		middle = truncateStyled(middle, w-lipgloss.Width(brand)-lipgloss.Width(right)-6)
	}
	content := brand + "  " + middle + strings.Repeat(" ", gap) + right
	return styleHeader.Width(w).Render(content)
}

// taskProgress renders "3/4 · 1 blocked" for the header, or "" when there are
// no tasks. The count is what you want at a glance; the list is in the panel.
func (m *Model) taskProgress() string {
	if m.manifest == nil || len(m.manifest.Tasks) == 0 {
		return ""
	}
	done, blocked := 0, 0
	for _, t := range m.manifest.Tasks {
		switch t.Status {
		case state.TaskDone:
			done++
		case state.TaskBlocked:
			blocked++
		}
	}
	s := fmt.Sprintf("%d/%d tasks", done, len(m.manifest.Tasks))
	if blocked > 0 {
		s += fmt.Sprintf("  %d blocked", blocked)
	}
	return s
}

// ── Columns ────────────────────────────────────────────────────────────────────

func (m *Model) renderColumns() string {
	// Height(n) on a bordered style gives n+2 rows, Width(n) gives n+2 columns.
	left := stylePanelBox.
		Width(m.leftW - 2).
		Height(m.colH - 2).
		Render(m.viewport.View())

	if m.rightW <= 0 {
		return left + "\n"
	}

	right := stylePanelBox.
		Width(m.rightW - 2).
		Height(m.colH - 2).
		Render(m.renderRightPanel())

	// A one-column gap reads more cleanly than two adjacent borders.
	gap := strings.Repeat(" \n", m.colH)
	return lipgloss.JoinHorizontal(lipgloss.Top, left, strings.TrimSuffix(gap, "\n"), right) + "\n"
}

// ── Right panel ────────────────────────────────────────────────────────────────

func (m *Model) renderRightPanel() string {
	w := m.rightW - 2 // inside the border
	if w < 10 {
		return ""
	}

	var sb strings.Builder

	// Activity — only while something is running.
	if m.isBusy() && len(m.activity) > 0 {
		sb.WriteString(rpSection("Activity", w))
		names := make([]string, 0, len(m.activity))
		for n := range m.activity {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, name := range names {
			a := m.activity[name]
			icon := m.spinner.View()
			switch a.status {
			case "done":
				icon = styleActivityDone.Render("✓")
			case "error":
				icon = styleActivityError.Render("✗")
			}
			sb.WriteString(" " + strings.TrimRight(icon, " ") + " " + styleAgentName(name).Render(truncate(name, w-4)) + "\n")
			if a.line != "" {
				sb.WriteString(styleActivityLine.Render("   "+truncate(a.line, w-4)) + "\n")
			}
		}
		sb.WriteString("\n")
	}

	// Agents — name, model, and a star on the judge.
	sb.WriteString(rpSection("Agents", w))
	if m.cfg != nil {
		// One name column, sized to the longest enabled name, so the models
		// line up instead of stepping raggedly across the panel.
		nameCol := 0
		for _, name := range m.agentNames() {
			if m.cfg.Agents[name].Enabled && len(name) > nameCol {
				nameCol = len(name)
			}
		}
		if nameCol > 10 {
			nameCol = 10
		}
		for _, name := range m.agentNames() {
			ac := m.cfg.Agents[name]
			if !ac.Enabled {
				continue
			}
			mark := " "
			if m.cfg.Judge.Agent == name {
				mark = styleJudgeMark.Render("★")
			}
			shown := truncate(name, nameCol)
			nameStr := styleAgentName(name).Render(padRight(shown, nameCol))
			budget := w - nameCol - 5
			missing := ""
			if _, live := m.reg[name]; !live && len(m.reg) > 0 {
				missing = styleActivityError.Render(" !")
				budget -= 2
			}
			sb.WriteString(fmt.Sprintf(" %s %s %s%s\n", mark, nameStr,
				styleMuted.Render(truncate(ac.Model, max(3, budget))), missing))
		}
	} else {
		sb.WriteString(styleActivityLine.Render(" loading…") + "\n")
	}
	sb.WriteString("\n")

	// Tasks — the board. The phase and counts live in the header, not here.
	sb.WriteString(rpSection("Tasks", w))
	if m.project == nil {
		sb.WriteString(styleActivityLine.Render(" no project") + "\n")
		return sb.String()
	}
	if m.manifest == nil || len(m.manifest.Tasks) == 0 {
		if m.project.Task != "" {
			sb.WriteString(styleActivityLine.Render(" "+truncate(m.project.Task, w-1)) + "\n")
			sb.WriteString("\n")
		}
		sb.WriteString(styleActivityLine.Render(" none yet — run divide") + "\n")
		return sb.String()
	}

	// Size the owner column once: per-row sizing makes every row start its
	// owner at a different column, which reads as misalignment.
	ownerCol := 0
	for _, t := range m.manifest.Tasks {
		if n := len(t.AssignedTo); n > ownerCol {
			ownerCol = n
		}
	}
	if ownerCol > 9 {
		ownerCol = 9
	}
	titleBudget := max(4, w-ownerCol-5) // badge, two gaps, right margin

	for _, t := range m.manifest.Tasks {
		owner := padRight(truncate(t.AssignedTo, ownerCol), ownerCol)
		sb.WriteString(fmt.Sprintf(" %s %s %s\n",
			taskStatusBadge(t.Status),
			styleTaskTitle.Render(padRight(truncate(t.Title, titleBudget), titleBudget)),
			styleAgentName(t.AssignedTo).Render(owner)))
		if t.Status == state.TaskBlocked && t.BlockReason != "" {
			sb.WriteString(styleActivityError.Render("   "+truncate(t.BlockReason, w-4)) + "\n")
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

// rpSection renders a section rule exactly w columns wide: "── Title ─────".
func rpSection(title string, w int) string {
	label := " " + title + " "
	rest := w - lipgloss.Width(label) - 2
	if rest < 0 {
		rest = 0
	}
	return styleSectionRule.Render("──") + styleSectionTitle.Render(label) +
		styleSectionRule.Render(strings.Repeat("─", rest)) + "\n"
}

// ── Approval card ──────────────────────────────────────────────────────────────

func (m *Model) renderApprovalCard() string {
	w := m.width
	inner := w - 2 // inside the card's own border runes

	q := m.approvalQuestion
	if q == "" {
		q = "Approve?"
	}

	bc := styleApprovalBorder
	label := "─ " + q + " "
	if lipgloss.Width(label) > inner-2 {
		label = "─ " + truncate(q, inner-6) + " "
	}
	topPad := inner - lipgloss.Width(label)
	if topPad < 0 {
		topPad = 0
	}
	top := bc.Render("╭"+label) + bc.Render(strings.Repeat("─", topPad)+"╮")

	body := "  " + styleApprovalYes.Render("[y]") + " yes" +
		"    " + styleApprovalNo.Render("[n]") + " no" +
		styleMuted.Render("    esc cancel")
	bodyPad := inner - lipgloss.Width(body)
	if bodyPad < 0 {
		bodyPad = 0
		body = lipgloss.NewStyle().MaxWidth(inner).Render(body)
	}
	mid := bc.Render("│") + body + strings.Repeat(" ", bodyPad) + bc.Render("│")
	bot := bc.Render("╰" + strings.Repeat("─", inner) + "╯")

	return top + "\n" + mid + "\n" + bot + "\n"
}

// ── Input box ──────────────────────────────────────────────────────────────────

func (m *Model) renderInputBox() string {
	prefix := styleInputPrefix.Render("❯")
	if m.isBusy() {
		prefix = m.spinner.View()
	}

	hint := ""
	if m.mode == modeApproval {
		hint = styleMuted.Render("y/n ")
	} else if m.prompt != "" && m.prompt != "y/n" {
		hint = styleMuted.Render(m.prompt + " ")
	}

	lines := strings.Split(m.textarea.View(), "\n")
	for i := range lines {
		if i == 0 {
			lines[i] = prefix + " " + hint + lines[i]
		} else {
			lines[i] = "  " + lines[i]
		}
	}

	boxStyle := styleInputBorderActive
	if m.isBusy() {
		boxStyle = styleInputBorderBusy
	}
	return boxStyle.Width(m.width-2).Render(strings.Join(lines, "\n")) + "\n"
}

// ── Status bar ─────────────────────────────────────────────────────────────────

func (m *Model) renderStatusBar() string {
	keys := []struct{ key, desc string }{
		{"esc", "cancel"},
		{"^y", "copy"},
		{"^h", "help"},
		{"^k", "clear"},
	}
	if m.mouseOff {
		keys = append(keys, struct{ key, desc string }{"^o", "mouse off"})
	} else {
		keys = append(keys, struct{ key, desc string }{"^o", "select"})
	}

	sep := styleMuted.Render(" · ")
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, styleStatusKey.Render(k.key)+styleMuted.Render(" "+k.desc))
	}
	left := strings.Join(parts, sep)

	// Right side: where we are, for people who scrolled the chat away.
	right := ""
	if m.project != nil && m.sessionID != "" {
		right = styleMuted.Render(m.project.ProjectName)
	}

	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right) - 2
	if gap < 1 {
		gap, right = 1, ""
		left = lipgloss.NewStyle().MaxWidth(m.width - 2).Render(left)
		gap = m.width - lipgloss.Width(left) - 2
		if gap < 0 {
			gap = 0
		}
	}
	return styleStatusBar.Width(m.width).Render(left + strings.Repeat(" ", gap) + right)
}

// ── Phase badge ────────────────────────────────────────────────────────────────

var phaseColors = map[state.Phase]lipgloss.Color{
	state.PhaseInit:   "#6B5C8A",
	state.PhasePlan:   "#60A5FA",
	state.PhaseDivide: "#F59E0B",
	state.PhaseWork:   "#B084FF",
	state.PhaseDone:   "#34D399",
}

func phaseBadge(phase state.Phase) string {
	c := lipgloss.Color("#6B5C8A")
	if col, ok := phaseColors[phase]; ok {
		c = col
	}
	return lipgloss.NewStyle().
		Foreground(c).Bold(true).
		Render(strings.ToLower(string(phase)))
}

// ── Utility ────────────────────────────────────────────────────────────────────
// (max/min are Go builtins since 1.21.)

// padRight pads s with spaces to n display columns.
func padRight(s string, n int) string {
	if d := n - lipgloss.Width(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

// truncateStyled clips already-styled text to n columns without counting ANSI.
func truncateStyled(s string, n int) string {
	if n < 1 {
		n = 1
	}
	if lipgloss.Width(s) <= n {
		return s
	}
	return lipgloss.NewStyle().MaxWidth(n).Render(s)
}
