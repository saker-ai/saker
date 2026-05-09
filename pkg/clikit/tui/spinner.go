package tui

import (
	"charm.land/bubbles/v2/spinner"
	"charm.land/lipgloss/v2"
)

// NewSpinner creates a styled spinner matching Claude Code's primary color.
func NewSpinner(t Theme) spinner.Model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(t.Primary)
	return s
}
