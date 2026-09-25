package tui

import "github.com/charmbracelet/lipgloss"

var (
	accent = lipgloss.AdaptiveColor{Light: "#8250df", Dark: "#c297ff"}
	muted  = lipgloss.AdaptiveColor{Light: "#57606a", Dark: "#8b949e"}
	good   = lipgloss.AdaptiveColor{Light: "#1a7f37", Dark: "#3fb950"}
	bad    = lipgloss.AdaptiveColor{Light: "#cf222e", Dark: "#f85149"}
	warnc  = lipgloss.AdaptiveColor{Light: "#9a6700", Dark: "#d29922"}

	titleStyle    = lipgloss.NewStyle().Foreground(accent).Bold(true)
	mutedStyle    = lipgloss.NewStyle().Foreground(muted)
	goodStyle     = lipgloss.NewStyle().Foreground(good)
	badStyle      = lipgloss.NewStyle().Foreground(bad)
	warnStyle     = lipgloss.NewStyle().Foreground(warnc)
	selectedStyle = lipgloss.NewStyle().Foreground(accent).Bold(true)

	paneStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(muted).
			Padding(0, 1)

	focusedPaneStyle = paneStyle.BorderForeground(accent)
)
