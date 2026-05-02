package tui

import "github.com/charmbracelet/lipgloss"

// ── Color tokens ───────────────────────────────────────────────────────────────

var (
	// Brand
	colorBrand    = lipgloss.Color("#7C3AED") // purple
	colorBrandDim = lipgloss.Color("#5B21B6") // darker purple

	// Agent identity
	colorClaude   = lipgloss.Color("#F59E0B") // amber
	colorOpencode = lipgloss.Color("#3B82F6") // blue
	colorCopilot  = lipgloss.Color("#06B6D4") // cyan
	colorJudge    = lipgloss.Color("#10B981") // emerald

	// Semantic
	colorSuccess = lipgloss.Color("#22C55E")
	colorError   = lipgloss.Color("#EF4444")
	colorWarning = lipgloss.Color("#F59E0B")

	// Dark-theme neutrals
	colorSurface = lipgloss.Color("#1E1E2E") // panel backgrounds
	colorBorder  = lipgloss.Color("#2D2D3F") // panel borders / dividers
	colorMuted   = lipgloss.Color("#4B5563") // secondary borders
	colorSubtle  = lipgloss.Color("#6B7280") // deemphasised text
	colorDim     = lipgloss.Color("#374151") // very subtle
	colorText    = lipgloss.Color("#E5E7EB") // primary text
)

// ── Layout styles ──────────────────────────────────────────────────────────────

var (
	// Header bar — full-width top bar
	styleHeader = lipgloss.NewStyle().
			Background(colorSurface).
			Foreground(colorText).
			Bold(true).
			Padding(0, 2)

	// Vertical column separator │
	styleVertSep = lipgloss.NewStyle().Foreground(colorBorder)

	// Horizontal thin rule
	styleHRule = lipgloss.NewStyle().Foreground(colorMuted)

	// Right panel section title
	styleSectionTitle = lipgloss.NewStyle().
				Foreground(colorSubtle).
				Bold(true)

	// Status / shortcuts bar at very bottom
	styleShortcutsBar = lipgloss.NewStyle().
				Background(colorSurface).
				Foreground(colorSubtle).
				Padding(0, 1)

	styleShortcutKey = lipgloss.NewStyle().
				Background(colorDim).
				Foreground(colorBrand).
				Bold(true).
				Padding(0, 1)

	// Input box border
	styleInputBorderActive = lipgloss.NewStyle().Foreground(colorBrand)
	styleInputBorderBusy   = lipgloss.NewStyle().Foreground(colorMuted)

	// Approval card border
	styleApprovalBorder = lipgloss.NewStyle().Foreground(colorWarning)

	// ── Chat message types ────────────────────────────────────────────────────

	styleSystemMsg = lipgloss.NewStyle().
			Foreground(colorSubtle).
			Italic(true)

	styleErrorMsg = lipgloss.NewStyle().
			Foreground(colorError).
			Bold(true)

	styleSuccessMsg = lipgloss.NewStyle().
			Foreground(colorSuccess).
			Bold(true)

	styleHumanMsg = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#EC4899")).
			Bold(true)

	styleInputPrefix = lipgloss.NewStyle().
				Foreground(colorBrand).
				Bold(true)

	// ── Activity / agent label styles ─────────────────────────────────────────

	styleAgentLabel = func(color lipgloss.Color) lipgloss.Style {
		return lipgloss.NewStyle().
			Bold(true).
			Foreground(color).
			Width(12)
	}

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

	// ── Misc ──────────────────────────────────────────────────────────────────

	styleDivider     = lipgloss.NewStyle().Foreground(colorMuted)
	styleTaskDone    = lipgloss.NewStyle().Foreground(colorSuccess)
	styleTaskPending = lipgloss.NewStyle().Foreground(colorSubtle)
	// styleStatusBar kept for any legacy references
	styleStatusBar = lipgloss.NewStyle().
			Background(colorSurface).
			Foreground(colorSubtle).
			Padding(0, 1)
	styleStatusSep = lipgloss.NewStyle().
			Background(colorSurface).
			Foreground(colorMuted).
			SetString("  │  ")
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

func agentLabel(name string) string {
	color := agentColor(name)
	label := "[" + name + "]"
	return styleAgentLabel(color).Render(label)
}
