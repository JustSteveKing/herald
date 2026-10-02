// Package termquiet stops herald asking the terminal its background colour.
//
// Bubble Tea v1 asks in its package init (tea_init.go), so the question goes
// out when any herald command starts, not only the TUI. A terminal that never
// answers (some tmux setups, serial consoles, some remote shells) then costs
// a five-second timeout on every command: `herald --version` measured 5.01s.
// herald's palette does not depend on the answer, so it is never needed.
//
// Declaring the background before Bubble Tea's init runs makes lipgloss skip
// the question. That relies on Go's package initialisation order, which since
// Go 1.21 is specified: a package's imports first, otherwise by import path.
// This package imports only lipgloss, so lipgloss is ready, and its path,
// github.com/JustSteveKing/..., sorts before github.com/charmbracelet/...
// (upper case before lower), so it runs before bubbletea's init. The main
// package imports it for that reason alone. TestNoBackgroundQuery guards it.
//
// The same fix as mavis's and taskgo's.
package termquiet

import "github.com/charmbracelet/lipgloss"

func init() {
	lipgloss.SetHasDarkBackground(true)
}
