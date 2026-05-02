package tui

import "github.com/charmbracelet/lipgloss"

var (
	// Brand
	colorBrand    = lipgloss.Color("#7C3AED")
	colorBrandDim = lipgloss.Color("#5B21B6")

	// Agent identity
	colorClaude    = lipgloss.Color("#F59E0B") // amber
	colorOpencode  = lipgloss.Color("#3B82F6") // blue
	colorCopilot   = lipgloss.Color("#06B6D4") // cyan
	colorJudge     = lipgloss.Color("#10B981") // emerald

	// Semantic
	colorSuccess = lipgloss.Color("#22C55E")
	colorError   = lipgloss.Color("#EF4444")
	colorWarning = lipgloss.Color("#F59E0B")

	// Neutrals (dark theme)
	colorSurface = lipgloss.Color("#1E1E2E")
	colorMuted   = lipgloss.Color("#374151")
	colorSubtle  = lipgloss.Color("#6B7280")
	colorText    = lipgloss.Color("#F9FAFB")

	// Header bar
	stylePhaseBar = lipgloss.NewStyle().
			Background(colorBrand).
			Foreground(lipgloss.Color("#FFFFFF")).
			Bold(true).
			Padding(0, 2)

	// Agent labels in chat history
	styleAgentLabel = func(color lipgloss.Color) lipgloss.Style {
		return lipgloss.NewStyle().
			Bold(true).
			Foreground(color).
			Width(12)
	}

	// Chat message types
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

	// Input
	styleInputPrefix = lipgloss.NewStyle().
				Foreground(colorBrand).
				Bold(true)

	// Status bar (bottom)
	styleStatusBar = lipgloss.NewStyle().
			Background(colorSurface).
			Foreground(colorSubtle).
			Padding(0, 1)

	styleStatusSep = lipgloss.NewStyle().
			Background(colorSurface).
			Foreground(colorMuted).
			SetString("  │  ")

	// Activity panel
	styleActivitySep = lipgloss.NewStyle().
				Foreground(colorMuted)

	styleActivityAgent = lipgloss.NewStyle().
				Bold(true)

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

	// Approval card
	styleApprovalYes = lipgloss.NewStyle().
				Foreground(colorSuccess).
				Bold(true)

	styleApprovalNo = lipgloss.NewStyle().
			Foreground(colorSubtle)

	// Task table
	styleTaskDone    = lipgloss.NewStyle().Foreground(colorSuccess)
	styleTaskPending = lipgloss.NewStyle().Foreground(colorSubtle)

	// Misc
	styleDivider = lipgloss.NewStyle().Foreground(colorMuted)
	styleHeader  = lipgloss.NewStyle().Bold(true).Foreground(colorBrand).Padding(0, 1)
)

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
