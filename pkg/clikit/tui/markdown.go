package tui

import (
	"strings"

	"charm.land/glamour/v2"
)

// renderMarkdown renders content as markdown using glamour.
// Falls back to raw text if rendering fails.
func renderMarkdown(content string, width int) string {
	if width < 20 {
		width = 20
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return content
	}
	result, err := r.Render(content)
	if err != nil {
		return content
	}
	// glamour adds trailing newlines; trim them.
	return strings.TrimRight(result, "\n")
}
