package tui

import (
	"fmt"
	"os"
	"strings"

	"charm.land/lipgloss/v2"
)

// mascotRows is a static Sichuan opera face mask (川剧脸谱),
// symbolising the soul/persona behind Saker.
var mascotRows = [3]string{
	" ▄███▄  ",
	" ◉▀▼▀◉  ",
	"  ╰─╯   ",
}

// Header renders the top section of the TUI with mascot and project info.
type Header struct {
	styles       Styles
	width        int
	modelName    string
	sessionID    string
	skillCount   int
	cwd          string
	version      string
	updateNotice string
}

// NewHeader creates a Header component.
func NewHeader(s Styles) *Header {
	cwd, _ := os.Getwd()
	home, _ := os.UserHomeDir()
	if home != "" && strings.HasPrefix(cwd, home) {
		cwd = "~" + cwd[len(home):]
	}
	return &Header{styles: s, cwd: cwd, version: "0.1.0"}
}

// SetWidth updates the header width.
func (h *Header) SetWidth(w int) { h.width = w }

// SetModel updates the displayed model name.
func (h *Header) SetModel(name string) { h.modelName = name }

// SetSession updates the displayed session ID.
func (h *Header) SetSession(id string) {
	if len(id) > 8 {
		id = id[:8]
	}
	h.sessionID = id
}

// SetSkillCount updates the displayed skill count.
func (h *Header) SetSkillCount(n int) { h.skillCount = n }

// SetUpdateNotice sets the version update notification text.
func (h *Header) SetUpdateNotice(notice string) { h.updateNotice = notice }

// View renders the header as a CondensedLogo with mascot (Claude Code style).
// Layout:
//
//	▐▛███▜▌  Saker v0.1.0
//	▝▜█████▛▘ model-name
//	  ▘▘ ▝▝   ~/path/to/cwd
func (h *Header) View() string {
	mascotColor := h.styles.LogoColor

	// Build info lines for the right side
	titleLine := fmt.Sprintf("%s %s",
		lipgloss.NewStyle().Bold(true).Foreground(h.styles.Theme.Fg).Render("Saker"),
		h.styles.HeaderDim.Render("v"+h.version),
	)

	var modelLine string
	if h.modelName != "" {
		modelLine = h.styles.HeaderDim.Render(h.modelName)
	}

	cwdLine := h.styles.HeaderDim.Render(h.cwd)

	// Compose 3 rows: mascot left, info right
	infoLines := [3]string{titleLine, modelLine, cwdLine}
	var b strings.Builder
	for i := 0; i < 3; i++ {
		mascot := mascotColor.Render(mascotRows[i])
		info := infoLines[i]
		b.WriteString(fmt.Sprintf(" %s %s\n", mascot, info))
	}

	if h.updateNotice != "" {
		updateStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#FFAA00"))
		b.WriteString(" " + updateStyle.Render(h.updateNotice) + "\n")
	}

	return b.String()
}
