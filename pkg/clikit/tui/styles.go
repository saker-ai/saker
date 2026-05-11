package tui

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

// Icons used throughout the TUI — aligned with Claude Code's figures.
const (
	IconCheck    = "✓"
	IconPending  = "●" // BLACK_CIRCLE — tool in-progress
	IconCircle   = "●" // message dot indicator
	IconError    = "✗"
	IconDot      = "·"
	IconBorder   = "│"
	IconCursor   = "█"
	IconResponse = "⎿" // Claude Code response prefix
)

// Theme holds the color palette — matches Claude Code's dark theme.
type Theme struct {
	Primary      color.Color // brand color (claude orange)
	Secondary    color.Color // suggestion/highlight
	Accent       color.Color // same as primary
	Muted        color.Color // subtle gray
	Error        color.Color
	Success      color.Color
	Warning      color.Color
	Fg           color.Color // text white
	FgDim        color.Color // inactive gray
	Bg           color.Color // terminal black
	PromptBorder color.Color // input border gray
	UserMsgBg    color.Color // user message background
}

// DefaultTheme returns a dark theme matching Claude Code's darkTheme.
func DefaultTheme() Theme {
	return Theme{
		Primary:      lipgloss.Color("#D77757"), // claude orange rgb(215,119,87)
		Secondary:    lipgloss.Color("#B1B9F9"), // suggestion blue-purple
		Accent:       lipgloss.Color("#D77757"), // same as primary
		Muted:        lipgloss.Color("#505050"), // subtle rgb(80,80,80)
		Error:        lipgloss.Color("#FF6B80"), // soft red
		Success:      lipgloss.Color("#4EBA65"), // success green
		Warning:      lipgloss.Color("#FFC107"), // amber
		Fg:           lipgloss.Color("#FFFFFF"), // white
		FgDim:        lipgloss.Color("#999999"), // inactive grey
		Bg:           lipgloss.Color("#000000"), // black
		PromptBorder: lipgloss.Color("#888888"), // mid grey
		UserMsgBg:    lipgloss.Color("#373737"), // dark grey
	}
}

// Styles holds pre-built lipgloss styles for the TUI.
type Styles struct {
	Theme Theme

	// Header
	HeaderBold lipgloss.Style
	HeaderDim  lipgloss.Style
	LogoColor  lipgloss.Style // Clawd mascot color

	// Chat - User messages
	UserMsgBlock lipgloss.Style // background block for user messages

	// Chat - Assistant messages
	AssistantText  lipgloss.Style
	ResponsePrefix lipgloss.Style // dim ⎿ prefix
	AssistantDot   lipgloss.Style // ● message indicator

	// Chat - Tool calls
	ToolPending lipgloss.Style
	ToolSuccess lipgloss.Style
	ToolError   lipgloss.Style
	ToolName    lipgloss.Style
	ToolParam   lipgloss.Style

	// Chat - Errors / System
	ErrorText  lipgloss.Style
	SystemText lipgloss.Style

	// Status bar
	StatusBar  lipgloss.Style
	StatusText lipgloss.Style
	StatusKey  lipgloss.Style

	// Input
	InputBorder lipgloss.Style
}

// NewStyles creates Styles from a Theme.
func NewStyles(t Theme) Styles {
	return Styles{
		Theme: t,

		HeaderBold: lipgloss.NewStyle().Bold(true).Foreground(t.Fg),
		HeaderDim:  lipgloss.NewStyle().Foreground(t.FgDim),
		LogoColor:  lipgloss.NewStyle().Foreground(t.Primary),

		UserMsgBlock: lipgloss.NewStyle().Background(t.UserMsgBg),

		AssistantText:  lipgloss.NewStyle().Foreground(t.Fg),
		ResponsePrefix: lipgloss.NewStyle().Foreground(t.FgDim),
		AssistantDot:   lipgloss.NewStyle().Foreground(t.Fg),

		ToolPending: lipgloss.NewStyle().Foreground(t.FgDim),
		ToolSuccess: lipgloss.NewStyle().Foreground(t.Success),
		ToolError:   lipgloss.NewStyle().Foreground(t.Error),
		ToolName:    lipgloss.NewStyle().Bold(true).Foreground(t.Fg),
		ToolParam:   lipgloss.NewStyle().Foreground(t.FgDim),

		ErrorText:  lipgloss.NewStyle().Foreground(t.Error),
		SystemText: lipgloss.NewStyle().Foreground(t.FgDim).Italic(true),

		StatusBar:  lipgloss.NewStyle().Foreground(t.FgDim).Background(t.UserMsgBg),
		StatusText: lipgloss.NewStyle().Foreground(t.FgDim),
		StatusKey:  lipgloss.NewStyle().Foreground(t.Muted),

		InputBorder: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder(), true, false, false, false). // top only
			BorderForeground(t.PromptBorder),
	}
}
