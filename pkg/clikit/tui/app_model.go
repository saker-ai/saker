// app_model.go: tea.Model lifecycle - Init() command for the App model.
package tui

import (
	tea "charm.land/bubbletea/v2"
)

// Init implements tea.Model.
func (a *App) Init() tea.Cmd {
	return tea.Batch(
		a.input.textarea.Focus(),
		tea.Println(a.header.View()),
	)
}
