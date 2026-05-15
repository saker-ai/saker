package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/saker-ai/saker/pkg/model"
)

// StatusBar renders the bottom status line below the input box.
// Shows model name, token usage, estimated cost, and context usage.
type StatusBar struct {
	styles Styles
	width  int
	text   string

	// Model info.
	modelName     string
	contextWindow int // model context window in tokens

	// Token usage counters (cumulative across turns).
	inputTokens  int
	outputTokens int
}

// NewStatusBar creates a StatusBar component.
func NewStatusBar(s Styles) *StatusBar {
	return &StatusBar{
		styles: s,
		text:   "Ready",
	}
}

// SetWidth updates the status bar width.
func (s *StatusBar) SetWidth(w int) { s.width = w }

// SetText updates the status message.
func (s *StatusBar) SetText(text string) { s.text = text }

// SetModel updates the model name and resolves context window size.
func (s *StatusBar) SetModel(name string) {
	s.modelName = name
	s.contextWindow = model.LookupContextWindow(name)
}

// AddTokens accumulates token usage from a single turn.
func (s *StatusBar) AddTokens(input, output int) {
	s.inputTokens += input
	s.outputTokens += output
}

// ResetTokens clears the token counters (e.g., on /new).
func (s *StatusBar) ResetTokens() {
	s.inputTokens = 0
	s.outputTokens = 0
}

// View renders the status bar.
//
// Layout:
//
//	Left:  model | tokens (↑ ↓) | $cost | ctx%   — or activity text when busy
//	Right: keyboard shortcuts
func (s *StatusBar) View() string {
	var left string
	if s.text == "Ready" {
		left = s.buildInfoLine()
	} else {
		left = s.styles.StatusText.Render(s.text)
	}

	// Right side: shortcuts.
	sep := s.styles.StatusKey.Render("  ")
	keys := []string{
		shortcut(s.styles, "ctrl+c", "interrupt"),
		shortcut(s.styles, "ctrl+d", "quit"),
	}
	right := strings.Join(keys, sep)

	leftW := lipgloss.Width(left)
	rightW := lipgloss.Width(right)
	gap := s.width - leftW - rightW - 4
	if gap < 1 {
		gap = 1
	}

	line := fmt.Sprintf(" %s%s%s ", left, strings.Repeat(" ", gap), right)
	return s.styles.StatusBar.Width(s.width).Render(line)
}

// buildInfoLine constructs the left-side info: model | tokens | cost | ctx%.
func (s *StatusBar) buildInfoLine() string {
	var parts []string

	// Model name (abbreviated if too long).
	if s.modelName != "" {
		name := abbreviateModel(s.modelName)
		parts = append(parts, s.styles.StatusText.Render(name))
	}

	// Token usage.
	if s.inputTokens > 0 || s.outputTokens > 0 {
		total := s.inputTokens + s.outputTokens
		tokenStr := fmt.Sprintf("%s tokens (%s↑ %s↓)",
			formatTokenCount(total),
			formatTokenCount(s.inputTokens),
			formatTokenCount(s.outputTokens),
		)
		parts = append(parts, s.styles.StatusText.Render(tokenStr))

		// Estimated cost.
		cost := model.EstimateCost(s.modelName, model.Usage{
			InputTokens:  s.inputTokens,
			OutputTokens: s.outputTokens,
		})
		if cost.TotalCost > 0 {
			parts = append(parts, s.styles.StatusText.Render(formatCost(cost.TotalCost, cost.Currency)))
		}

		// Context window usage percentage.
		if s.contextWindow > 0 && s.inputTokens > 0 {
			pct := float64(s.inputTokens) * 100 / float64(s.contextWindow)
			parts = append(parts, s.styles.StatusText.Render(fmt.Sprintf("ctx %.0f%%", pct)))
		}
	}

	if len(parts) == 0 {
		return s.styles.StatusText.Render("Ready")
	}

	sep := s.styles.StatusKey.Render(" | ")
	return strings.Join(parts, sep)
}

// shortcut renders a key binding with styled key and description.
func shortcut(s Styles, key, desc string) string {
	k := s.StatusKey.Render(key)
	d := s.StatusText.Render(desc)
	return fmt.Sprintf("%s %s", k, d)
}

// formatTokenCount formats a token count for compact display.
func formatTokenCount(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// formatCost formats a cost for compact display with the appropriate
// currency symbol ("$" for USD, "¥" for CNY).
func formatCost(cost float64, currency string) string {
	sym := "$"
	if currency == "CNY" {
		sym = "¥"
	}
	switch {
	case cost >= 1.0:
		return fmt.Sprintf("%s%.2f", sym, cost)
	case cost >= 0.01:
		return fmt.Sprintf("%s%.3f", sym, cost)
	default:
		return fmt.Sprintf("%s%.4f", sym, cost)
	}
}

// abbreviateModel shortens long model names for the status bar.
func abbreviateModel(name string) string {
	if len(name) <= 24 {
		return name
	}
	return name[:21] + "…"
}
