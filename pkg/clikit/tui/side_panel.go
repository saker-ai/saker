package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

const (
	sidePanelScrollLines = 3
	sidePanelMinHeight   = 8
)

// SidePanel renders a floating overlay panel for /btw and /im side questions.
// btw panels are single-shot (no input); im panels support multi-turn with input.
type SidePanel struct {
	styles      Styles
	width       int
	height      int
	title       string // e.g., "/btw what is middleware?"
	panelType   string // "btw" or "im"
	sessionID   string // session ID for multi-turn (im only)
	interactive bool   // true = show input, support follow-up (im)
	content     strings.Builder
	done        bool
	err         error
	scrollY     int

	cachedRendered string
	cachedLen      int
	cachedWidth    int
}

// NewSidePanel creates a single-shot side panel (for /btw).
func NewSidePanel(s Styles, panelType, title string) *SidePanel {
	return &SidePanel{
		styles:    s,
		panelType: panelType,
		title:     title,
	}
}

// NewInteractiveSidePanel creates a multi-turn side panel with input (for /im).
func NewInteractiveSidePanel(s Styles, panelType, title, sessionID string) *SidePanel {
	return &SidePanel{
		styles:      s,
		panelType:   panelType,
		title:       title,
		sessionID:   sessionID,
		interactive: true,
	}
}

// IsInteractive returns whether the panel supports multi-turn input.
func (p *SidePanel) IsInteractive() bool {
	return p.interactive
}

// SessionID returns the panel's session ID (for multi-turn follow-ups).
func (p *SidePanel) SessionID() string {
	return p.sessionID
}

// AddUserMessage appends a styled user message to the panel content
// and resets the done state for a new streaming response.
func (p *SidePanel) AddUserMessage(text string) {
	p.content.WriteString("\n\n> **You:** " + text + "\n\n")
	p.done = false
	p.err = nil
}

// SetSize updates panel dimensions.
func (p *SidePanel) SetSize(w, h int) {
	p.width = w
	p.height = h
}

// AppendText adds streaming text to the panel content.
// It auto-scrolls to the bottom so new content is always visible.
func (p *SidePanel) AppendText(s string) {
	p.content.WriteString(s)
	p.scrollToBottom()
}

// SetDone marks the panel as finished streaming.
func (p *SidePanel) SetDone() {
	p.done = true
}

// SetError marks the panel with an error.
func (p *SidePanel) SetError(err error) {
	p.err = err
	p.done = true
}

// IsDone returns whether the panel is finished.
func (p *SidePanel) IsDone() bool {
	return p.done
}

// ScrollUp scrolls the content up.
func (p *SidePanel) ScrollUp() {
	p.scrollY -= sidePanelScrollLines
	if p.scrollY < 0 {
		p.scrollY = 0
	}
}

// ScrollDown scrolls the content down.
func (p *SidePanel) ScrollDown() {
	lines := p.contentLines()
	maxScroll := len(lines) - p.innerHeight()
	if maxScroll < 0 {
		maxScroll = 0
	}
	p.scrollY += sidePanelScrollLines
	if p.scrollY > maxScroll {
		p.scrollY = maxScroll
	}
}

// View renders the panel content area (without full-screen takeover).
// The panel is displayed above the input area in the main View.
func (p *SidePanel) View() string {
	w := p.width
	if w < 20 {
		w = 80
	}

	innerW := w - 6 // "│ " + content + " │"
	if innerW < 10 {
		innerW = 10
	}

	// Title color
	titleColor := p.styles.Theme.Warning // yellow for btw
	if p.panelType == "im" {
		titleColor = lipgloss.Color("#00BCD4") // cyan for im
	}
	titleStyle := lipgloss.NewStyle().Foreground(titleColor).Bold(true)

	// Truncate title if too long
	displayTitle := p.title
	maxTitleLen := innerW - 2
	if lipgloss.Width(displayTitle) > maxTitleLen && maxTitleLen > 3 {
		// Rough truncation for display
		runes := []rune(displayTitle)
		if len(runes) > maxTitleLen-3 {
			displayTitle = string(runes[:maxTitleLen-3]) + "..."
		}
	}

	// Content area
	contentStr := p.content.String()
	if p.err != nil {
		errStyle := lipgloss.NewStyle().Foreground(p.styles.Theme.Error)
		contentStr = errStyle.Render(p.err.Error())
	}

	// Render markdown
	rendered := renderMarkdown(contentStr, innerW)
	lines := strings.Split(rendered, "\n")

	// Apply scroll — cap visible lines to innerHeight, no padding
	visibleH := p.innerHeight()
	start := p.scrollY
	if start > len(lines) {
		start = len(lines)
	}
	end := start + visibleH
	if end > len(lines) {
		end = len(lines)
	}
	visibleLines := lines[start:end]

	// Hint bar
	hintStyle := lipgloss.NewStyle().Foreground(p.styles.Theme.FgDim)
	var hint string
	if p.done {
		if p.interactive {
			hint = hintStyle.Render("Enter send · ↑/↓ scroll · Esc close")
		} else {
			hint = hintStyle.Render("↑/↓ scroll · any key to dismiss")
		}
	} else {
		hint = hintStyle.Render("Esc cancel · streaming...")
	}

	// Scroll indicator
	scrollInfo := ""
	totalLines := len(lines)
	if totalLines > visibleH {
		pct := 0
		if totalLines-visibleH > 0 {
			pct = p.scrollY * 100 / (totalLines - visibleH)
		}
		scrollInfo = hintStyle.Render(fmt.Sprintf(" [%d%%]", pct))
	}

	// Compose — simple bordered panel
	var b strings.Builder

	// Top border with title
	titleRendered := titleStyle.Render(displayTitle)
	titleW := lipgloss.Width(titleRendered)
	fillW := maxInt(0, innerW-titleW)
	b.WriteString(fmt.Sprintf("─ %s %s", titleRendered, strings.Repeat("─", fillW)))
	b.WriteString("\n")

	// Content lines (no side borders for cleaner look)
	for _, line := range visibleLines {
		b.WriteString(fmt.Sprintf("  %s\n", line))
	}

	// Status line (while streaming)
	if !p.done {
		spinnerStyle := lipgloss.NewStyle().Foreground(titleColor)
		b.WriteString(fmt.Sprintf("  %s\n", spinnerStyle.Render("◌ Answering...")))
	}

	// Bottom separator with hint
	b.WriteString(fmt.Sprintf("─ %s%s", hint, scrollInfo))

	return b.String()
}

// innerHeight returns the number of visible content lines.
// Accounts for panel chrome (5 lines: top border, separator, hint, bottom border, status)
// plus space reserved for the input area and status bar below the panel (~5 lines).
func (p *SidePanel) innerHeight() int {
	h := p.height - 10 // panel chrome (5) + input area + status bar below (5)
	if h < 3 {
		h = 3
	}
	return h
}

// scrollToBottom sets scrollY so that the last line of content is visible.
func (p *SidePanel) scrollToBottom() {
	lines := p.contentLines()
	maxScroll := len(lines) - p.innerHeight()
	if maxScroll < 0 {
		maxScroll = 0
	}
	p.scrollY = maxScroll
}

// contentLines returns the rendered content split into lines.
// Results are cached until content changes or width changes.
func (p *SidePanel) contentLines() []string {
	innerW := p.width - 4
	if innerW < 10 {
		innerW = 76
	}
	cl := p.content.Len()
	if cl == p.cachedLen && innerW == p.cachedWidth && p.cachedRendered != "" {
		return strings.Split(p.cachedRendered, "\n")
	}
	rendered := renderMarkdown(p.content.String(), innerW)
	p.cachedRendered = rendered
	p.cachedLen = cl
	p.cachedWidth = innerW
	return strings.Split(rendered, "\n")
}

// padRight pads s with spaces to reach width w based on visual width.
func padRight(s string, w int) string {
	vw := lipgloss.Width(s)
	if vw >= w {
		return s
	}
	return s + strings.Repeat(" ", w-vw)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
