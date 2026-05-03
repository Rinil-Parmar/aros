package tui

import "github.com/charmbracelet/lipgloss"

// ── Color tokens ───────────────────────────────────────────────────────────────

var (
	// Brand purple (matches OpenCodeTUI palette)
	colorBrand    = lipgloss.Color("#B084FF")
	colorBrandDim = lipgloss.Color("#7C3AED")

	// Agent identity
	colorClaude   = lipgloss.Color("#F59E0B") // amber
	colorOpencode = lipgloss.Color("#60A5FA") // blue
	colorCopilot  = lipgloss.Color("#34D399") // emerald
	colorJudge    = lipgloss.Color("#EC7EF6") // pink

	// Semantic
	colorSuccess = lipgloss.Color("#34D399")
	colorError   = lipgloss.Color("#FB7185")
	colorWarning = lipgloss.Color("#F59E0B")

	// Dark-theme neutrals (deep purple-tinted dark)
	colorSurface = lipgloss.Color("#13111C") // panel background
	colorBorder  = lipgloss.Color("#4B3470") // panel borders
	colorMuted   = lipgloss.Color("#6B5C8A") // secondary borders
	colorSubtle  = lipgloss.Color("#9D8CC4") // deemphasised text
	colorDim     = lipgloss.Color("#2D2448") // very subtle
	colorText    = lipgloss.Color("#ECE7FA") // primary text
)

// ── Layout styles ──────────────────────────────────────────────────────────────

var (
	// Header — border-bottom line (OpenCodeTUI style)
	styleHeader = lipgloss.NewStyle().
			BorderBottom(true).
			BorderForeground(colorBorder).
			Foreground(colorText).
			Padding(0, 1)

	// Panel boxes — rounded border (OpenCodeTUI style)
	stylePanelBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorBorder)

	// Vertical column separator │
	styleVertSep = lipgloss.NewStyle().Foreground(colorBorder)

	// Horizontal thin rule
	styleHRule = lipgloss.NewStyle().Foreground(colorMuted)

	// Status / shortcuts bar at very bottom
	styleShortcutsBar = lipgloss.NewStyle().
				Foreground(colorSubtle).
				Padding(0, 1)

	styleShortcutKey = lipgloss.NewStyle().
				Background(colorDim).
				Foreground(colorBrand).
				Bold(true).
				Padding(0, 1)

	// Input box border — active (purple) / busy (dim)
	styleInputBorderActive = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(colorBrand).
				Padding(0, 1)
	styleInputBorderBusy = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(colorBorder).
				Padding(0, 1)

	// Approval card border
	styleApprovalBorder = lipgloss.NewStyle().Foreground(colorWarning)

	// ── Chat message types ────────────────────────────────────────────────────

	styleSystemMsg = lipgloss.NewStyle().
			Foreground(colorSubtle).
			Italic(true)

	styleInputPrefix = lipgloss.NewStyle().
				Foreground(colorBrand).
				Bold(true)

	// ── Activity / agent label styles ─────────────────────────────────────────

	styleActivityModel = lipgloss.NewStyle().
				Foreground(colorSubtle)

	styleActivityLine = lipgloss.NewStyle().
				Foreground(colorSubtle).
				Italic(true)

	styleActivityDone = lipgloss.NewStyle().
				Foreground(colorSuccess).
				Bold(true)

	styleActivityError = lipgloss.NewStyle().
				Foreground(colorError).
				Bold(true)

	// ── Approval card ─────────────────────────────────────────────────────────

	styleApprovalYes = lipgloss.NewStyle().
				Foreground(colorSuccess).
				Bold(true)

	styleApprovalNo = lipgloss.NewStyle().
			Foreground(colorSubtle)

	// ── Task board status badges ──────────────────────────────────────────────

	styleTaskDone    = lipgloss.NewStyle().Foreground(colorSuccess).Bold(true)
	styleTaskPending = lipgloss.NewStyle().Foreground(colorSubtle)
	styleTaskRun     = lipgloss.NewStyle().Foreground(colorBrand).Bold(true)
	styleTaskBlocked = lipgloss.NewStyle().Foreground(colorError).Bold(true)
)

// ── Helpers ────────────────────────────────────────────────────────────────────

func agentColor(name string) lipgloss.Color {
	switch name {
	case "claude":
		return colorClaude
	case "opencode":
		return colorOpencode
	case "copilot":
		return colorCopilot
	case "judge":
		return colorJudge
	default:
		return colorSubtle
	}
}
