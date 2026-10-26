package tui

import "github.com/charmbracelet/lipgloss"

var (
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	hintStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	cursorStyle = lipgloss.NewStyle().Reverse(true)
	errorStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	stderrStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	upStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	downStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
)
