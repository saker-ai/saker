package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// PermissionDecision captures the user's response to a permission prompt.
type PermissionDecision int

const (
	PermissionAllow PermissionDecision = iota
	PermissionDeny
	PermissionAlwaysAllow
)

// PermissionPanelOutcome is delivered when the user finishes.
type PermissionPanelOutcome struct {
	Decision PermissionDecision
	Feedback string
	Cancelled bool
}

// PermissionPanelRequest describes the tool action awaiting approval.
type PermissionPanelRequest struct {
	ToolName string
	Params   string
	Detail   string // full command or diff preview
}

// PermissionPanel is a modal overlay for tool permission confirmation.
type PermissionPanel struct {
	styles  Styles
	request PermissionPanelRequest
	width   int
	height  int

	feedbackMode bool
	feedback     string

	outcome  chan PermissionPanelOutcome
	finished bool
}

// NewPermissionPanel creates a panel for a permission request.
func NewPermissionPanel(s Styles, req PermissionPanelRequest) (*PermissionPanel, <-chan PermissionPanelOutcome) {
	out := make(chan PermissionPanelOutcome, 1)
	return &PermissionPanel{
		styles:  s,
		request: req,
		outcome: out,
	}, out
}

func (p *PermissionPanel) SetSize(w, h int) {
	p.width = w
	p.height = h
}

func (p *PermissionPanel) IsFinished() bool { return p.finished }

func (p *PermissionPanel) Cancel() {
	p.deliver(PermissionPanelOutcome{Cancelled: true})
}

func (p *PermissionPanel) HandleKey(msg tea.KeyPressMsg) tea.Cmd {
	if p.finished {
		return nil
	}

	if p.feedbackMode {
		switch msg.String() {
		case "esc":
			p.feedbackMode = false
			return nil
		case "enter":
			p.deliver(PermissionPanelOutcome{
				Decision: PermissionDeny,
				Feedback: strings.TrimSpace(p.feedback),
			})
			return nil
		case "backspace":
			if r := []rune(p.feedback); len(r) > 0 {
				p.feedback = string(r[:len(r)-1])
			}
			return nil
		default:
			if msg.Mod == 0 && msg.Code >= 0x20 && msg.Code != 0x7f {
				p.feedback += string(msg.Code)
			}
			return nil
		}
	}

	switch msg.String() {
	case "y", "Y":
		p.deliver(PermissionPanelOutcome{Decision: PermissionAllow})
	case "n", "N":
		p.deliver(PermissionPanelOutcome{Decision: PermissionDeny})
	case "a", "A":
		p.deliver(PermissionPanelOutcome{Decision: PermissionAlwaysAllow})
	case "tab":
		p.feedbackMode = true
		p.feedback = ""
	case "esc", "ctrl+c":
		p.Cancel()
	}
	return nil
}

func (p *PermissionPanel) deliver(o PermissionPanelOutcome) {
	if p.finished {
		return
	}
	p.finished = true
	select {
	case p.outcome <- o:
	default:
	}
}

func (p *PermissionPanel) View() string {
	t := p.styles.Theme
	title := lipgloss.NewStyle().Foreground(t.Warning).Bold(true)
	toolStyle := lipgloss.NewStyle().Foreground(t.Fg).Bold(true)
	paramStyle := lipgloss.NewStyle().Foreground(t.FgDim)
	detailStyle := lipgloss.NewStyle().Foreground(t.Fg).
		Border(lipgloss.RoundedBorder(), true).
		BorderForeground(t.Muted).
		Padding(0, 1)
	keyStyle := lipgloss.NewStyle().Foreground(t.Success).Bold(true)
	denyKeyStyle := lipgloss.NewStyle().Foreground(t.Error).Bold(true)
	hintStyle := lipgloss.NewStyle().Foreground(t.FgDim).Italic(true)

	var b strings.Builder
	b.WriteString("\n  " + title.Render("Permission Required") + "\n\n")
	b.WriteString("  " + toolStyle.Render(p.request.ToolName))
	if p.request.Params != "" {
		b.WriteString(" " + paramStyle.Render("("+p.request.Params+")"))
	}
	b.WriteString("\n")

	if p.request.Detail != "" {
		maxW := p.width - 8
		if maxW < 40 {
			maxW = 40
		}
		detail := p.request.Detail
		if len(detail) > 500 {
			detail = detail[:497] + "..."
		}
		b.WriteString("\n" + detailStyle.Width(maxW).Render(detail) + "\n")
	}

	b.WriteString("\n")
	if p.feedbackMode {
		prompt := lipgloss.NewStyle().Foreground(t.Primary).Render("Feedback > ")
		b.WriteString("  " + prompt + p.feedback + IconCursor + "\n")
		b.WriteString("  " + hintStyle.Render("Enter send · Esc back") + "\n")
	} else {
		b.WriteString(fmt.Sprintf("  %s allow  %s deny  %s always allow  %s feedback\n",
			keyStyle.Render("[y]"),
			denyKeyStyle.Render("[n]"),
			keyStyle.Render("[a]"),
			paramStyle.Render("[Tab]"),
		))
		b.WriteString("  " + hintStyle.Render("Esc cancel") + "\n")
	}

	return b.String()
}
