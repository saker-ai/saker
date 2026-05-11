// app_view.go: View() rendering for the App model and its overlays.
package tui

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// View implements tea.Model.
func (a *App) View() tea.View {
	// Side panel overlay.
	if a.sidePanel != nil {
		if a.spinning {
			a.status.SetText(a.spinner.View() + " Thinking...")
		}
		panelView := a.sidePanel.View()
		statusView := a.status.View()
		if a.sidePanel.IsInteractive() {
			// Interactive panel (im): show panel + input + status.
			inputView := a.input.View()
			view := lipgloss.JoinVertical(lipgloss.Left, panelView, inputView, statusView)
			return tea.NewView(view)
		}
		// Non-interactive panel (btw): show panel + status only.
		view := lipgloss.JoinVertical(lipgloss.Left, panelView, statusView)
		return tea.NewView(view)
	}

	// Status text with spinner if active.
	if a.spinning {
		a.status.SetText(a.spinner.View() + " Thinking...")
	}
	statusView := a.status.View()
	inputView := a.input.View()
	chatView := a.chat.View()

	var parts []string
	if chatView != "" {
		parts = append(parts, chatView)
	}
	parts = append(parts, inputView, statusView)

	view := lipgloss.JoinVertical(lipgloss.Left, parts...)
	return tea.NewView(view)
}
