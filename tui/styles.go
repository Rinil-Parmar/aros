package tui

import "github.com/charmbracelet/lipgloss"

var (
	colorPrimary  = lipgloss.Color("#7C3AED") // purple — Aros brand
	colorClaude   = lipgloss.Color("#D97706") // amber
	colorOpencode = lipgloss.Color("#2563EB") // blue
	colorJudge    = lipgloss.Color("#059669") // green
	colorSystem   = lipgloss.Color("#6B7280") // gray
	colorError    = lipgloss.Color("#DC2626") // red
	colorHuman    = lipgloss.Color("#EC4899") // pink
	colorSuccess  = lipgloss.Color("#10B981") // emerald

	styleHeader = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorPrimary).
			Padding(0, 1)

	styleBanner = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorPrimary)

	styleAgentLabel = func(color lipgloss.Color) lipgloss.Style {
		return lipgloss.NewStyle().
			Bold(true).
			Foreground(color).
			Width(12)
	}

	styleSystemMsg = lipgloss.NewStyle().
			Foreground(colorSystem).
			Italic(true)

	styleErrorMsg = lipgloss.NewStyle().
			Foreground(colorError).
			Bold(true)

	styleSuccessMsg = lipgloss.NewStyle().
			Foreground(colorSuccess).
			Bold(true)

	styleHumanMsg = lipgloss.NewStyle().
			Foreground(colorHuman).
			Bold(true)

	styleInputPrefix = lipgloss.NewStyle().
				Foreground(colorPrimary).
				Bold(true)

	stylePhaseBar = lipgloss.NewStyle().
			Background(colorPrimary).
			Foreground(lipgloss.Color("#FFFFFF")).
			Bold(true).
			Padding(0, 2)

	styleDivider = lipgloss.NewStyle().
			Foreground(colorSystem)

	styleTaskID = lipgloss.NewStyle().
			Foreground(colorPrimary).
			Bold(true)

	styleTaskDone = lipgloss.NewStyle().
			Foreground(colorSuccess)

	styleTaskPending = lipgloss.NewStyle().
				Foreground(colorSystem)

	styleStatusBar = lipgloss.NewStyle().
			Background(lipgloss.Color("#1F2937")).
			Foreground(lipgloss.Color("#9CA3AF")).
			Padding(0, 2)

	styleStatusKey = lipgloss.NewStyle().
			Background(lipgloss.Color("#1F2937")).
			Foreground(colorPrimary).
			Bold(true)

	styleStatusSep = lipgloss.NewStyle().
			Background(lipgloss.Color("#1F2937")).
			Foreground(lipgloss.Color("#374151")).
			SetString("  │  ")
)

func agentColor(name string) lipgloss.Color {
	switch name {
	case "claude":
		return colorClaude
	case "opencode":
		return colorOpencode
	case "judge":
		return colorJudge
	default:
		return colorSystem
	}
}

func agentLabel(name string) string {
	color := agentColor(name)
	label := "[" + name + "]"
	return styleAgentLabel(color).Render(label)
}
