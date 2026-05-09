package tui

import (
	"image/color"
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

const (
	inputMaxHeight = 10
	inputMinHeight = 1
)

// inputKeyword defines a keyword to highlight in the input box.
type inputKeyword struct {
	prefix      string      // text to match (case-insensitive)
	color       color.Color // text highlight color
	borderColor color.Color // border color when this keyword is active
	needsSpace  bool        // require trailing space or end-of-input after prefix
}

// keywords to detect and highlight in input, ordered by priority.
var inputKeywords = []inputKeyword{
	{prefix: "/btw", color: nil, borderColor: nil, needsSpace: true},        // warning (yellow)
	{prefix: "/im", color: nil, borderColor: nil, needsSpace: true},         // IM bridge
	{prefix: "/quit", color: nil, borderColor: nil},                         // error (red)
	{prefix: "/exit", color: nil, borderColor: nil},                         // error (red)
	{prefix: "/q", color: nil, borderColor: nil, needsSpace: true},          // error (red)
	{prefix: "/new", color: nil, borderColor: nil},                          // success (green)
	{prefix: "/help", color: nil, borderColor: nil},                         // secondary (blue)
	{prefix: "/model", color: nil, borderColor: nil},                        // secondary (blue)
	{prefix: "/session", color: nil, borderColor: nil},                      // secondary (blue)
	{prefix: "/status", color: nil, borderColor: nil},                       // secondary (blue)
	{prefix: "/skills", color: nil, borderColor: nil},                       // secondary (blue)
	{prefix: "/video-status", color: nil, borderColor: nil},                 // secondary (blue)
	{prefix: "/video-stop", color: nil, borderColor: nil, needsSpace: true}, // error (red)
}

// initKeywordColors sets colors from the theme (called once during NewInput).
func initKeywordColors(t Theme) {
	for i := range inputKeywords {
		kw := &inputKeywords[i]
		switch {
		case kw.prefix == "/btw":
			kw.color = t.Warning
			kw.borderColor = t.Warning
		case kw.prefix == "/quit" || kw.prefix == "/exit" || kw.prefix == "/q" || kw.prefix == "/video-stop":
			kw.color = t.Error
			kw.borderColor = t.Error
		case kw.prefix == "/new":
			kw.color = t.Success
			kw.borderColor = t.Success
		default:
			kw.color = t.Secondary
			kw.borderColor = t.Secondary
		}
	}
}

// Input wraps a textarea for user prompt entry.
type Input struct {
	textarea  textarea.Model
	styles    Styles
	width     int
	enabled   bool
	lastLines int // track line count to avoid unnecessary layout

	// history stores previously submitted inputs for up/down arrow navigation.
	history      []string
	historyIdx   int    // -1 means not browsing history
	historyDraft string // text being edited before entering history mode
}

// NewInput creates an Input component.
func NewInput(s Styles) *Input {
	initKeywordColors(s.Theme)

	ta := textarea.New()
	ta.Placeholder = "Type a message..."
	ta.ShowLineNumbers = false
	ta.SetHeight(inputMinHeight)
	ta.MaxHeight = inputMaxHeight
	ta.CharLimit = 0 // unlimited
	ta.Prompt = ""   // no prompt prefix — Claude Code uses border instead

	// Apply theme colors to textarea
	ts := ta.Styles()
	ts.Focused.Base = lipgloss.NewStyle()
	ts.Focused.Text = lipgloss.NewStyle().Foreground(s.Theme.Fg)
	ts.Focused.Placeholder = lipgloss.NewStyle().Foreground(s.Theme.Muted)
	ts.Focused.Prompt = lipgloss.NewStyle()
	ts.Focused.CursorLine = lipgloss.NewStyle()
	ts.Blurred.Base = lipgloss.NewStyle()
	ts.Blurred.Text = lipgloss.NewStyle().Foreground(s.Theme.FgDim)
	ts.Blurred.Placeholder = lipgloss.NewStyle().Foreground(s.Theme.Muted)
	ta.SetStyles(ts)

	ta.Focus()

	return &Input{
		textarea:   ta,
		styles:     s,
		enabled:    true,
		historyIdx: -1,
	}
}

// SetWidth updates the input width.
func (i *Input) SetWidth(w int) {
	i.width = w
	i.textarea.SetWidth(w - 4) // room for border padding
}

// SetEnabled enables or disables submission (disabled while model is running).
// The textarea remains focused so the user can type ahead.
func (i *Input) SetEnabled(enabled bool) {
	i.enabled = enabled
	if enabled {
		i.textarea.Placeholder = "Type a message..."
	} else {
		i.textarea.Placeholder = "Type ahead while thinking..."
	}
	// Always keep textarea focused so user can type.
	if !i.textarea.Focused() {
		i.textarea.Focus()
	}
}

// Value returns the current input text.
func (i *Input) Value() string {
	return strings.TrimSpace(i.textarea.Value())
}

// Reset clears the input.
func (i *Input) Reset() {
	i.textarea.Reset()
	i.textarea.SetHeight(inputMinHeight)
}

// SaveHistory appends text to the input history (deduplicates consecutive entries).
func (i *Input) SaveHistory(text string) {
	if text == "" {
		return
	}
	if len(i.history) == 0 || i.history[len(i.history)-1] != text {
		i.history = append(i.history, text)
	}
	i.historyIdx = -1
	i.historyDraft = ""
}

// Update handles key events for the textarea.
// Input is always editable (type-ahead); submission is gated by enabled.
// When the textarea is single-line, up/down arrows navigate input history.
func (i *Input) Update(msg tea.Msg) (*Input, tea.Cmd) {
	if km, ok := msg.(tea.KeyPressMsg); ok && len(i.history) > 0 && i.textarea.LineCount() <= 1 {
		switch km.String() {
		case "up":
			if i.historyIdx == -1 {
				// Entering history mode: save current draft.
				i.historyDraft = i.textarea.Value()
				i.historyIdx = len(i.history) - 1
			} else if i.historyIdx > 0 {
				i.historyIdx--
			}
			i.textarea.SetValue(i.history[i.historyIdx])
			i.textarea.CursorEnd()
			return i, nil
		case "down":
			if i.historyIdx == -1 {
				break // not in history mode
			}
			if i.historyIdx < len(i.history)-1 {
				i.historyIdx++
				i.textarea.SetValue(i.history[i.historyIdx])
				i.textarea.CursorEnd()
			} else {
				// Past the end: restore draft.
				i.historyIdx = -1
				i.textarea.SetValue(i.historyDraft)
				i.textarea.CursorEnd()
				i.historyDraft = ""
			}
			return i, nil
		}
	}

	var cmd tea.Cmd
	i.textarea, cmd = i.textarea.Update(msg)
	return i, cmd
}

// LineCount returns the current number of visual lines in the textarea.
func (i *Input) LineCount() int {
	return i.textarea.LineCount()
}

// HeightChanged reports whether the textarea line count changed since last check.
// This allows the app to skip expensive layout recalculation on most keystrokes.
func (i *Input) HeightChanged() bool {
	lines := i.textarea.LineCount()
	if lines != i.lastLines {
		i.lastLines = lines
		return true
	}
	return false
}

// detectKeyword checks if the input starts with a recognized keyword.
// Returns the matched keyword definition or nil.
func (i *Input) detectKeyword() *inputKeyword {
	value := strings.TrimSpace(i.textarea.Value())
	if value == "" || value[0] != '/' {
		return nil
	}
	lower := strings.ToLower(value)
	for idx := range inputKeywords {
		kw := &inputKeywords[idx]
		if !strings.HasPrefix(lower, kw.prefix) {
			continue
		}
		rest := value[len(kw.prefix):]
		if kw.needsSpace {
			// Must be followed by space or be exact match
			if rest != "" && rest[0] != ' ' {
				continue
			}
		} else {
			// Must be exact match or followed by space
			if rest != "" && rest[0] != ' ' {
				continue
			}
		}
		return kw
	}
	return nil
}

// highlightKeywordInView replaces the first occurrence of the keyword text
// in the rendered textarea output with a colored version.
// ANSI color codes are zero-width, so cursor positioning is preserved.
func highlightKeywordInView(rendered string, keyword string, c color.Color) string {
	idx := strings.Index(rendered, keyword)
	if idx < 0 {
		// Try case-insensitive: the textarea renders the raw value,
		// so the case should match, but be safe.
		lower := strings.ToLower(rendered)
		lowerKw := strings.ToLower(keyword)
		idx = strings.Index(lower, lowerKw)
		if idx < 0 {
			return rendered
		}
		keyword = rendered[idx : idx+len(keyword)]
	}
	styled := lipgloss.NewStyle().Foreground(c).Bold(true).Render(keyword)
	return rendered[:idx] + styled + rendered[idx+len(keyword):]
}

// View renders the input area with a top rounded border (Claude Code style).
// Keywords at the start of input are highlighted with color.
func (i *Input) View() string {
	kw := i.detectKeyword()

	borderColor := i.styles.Theme.PromptBorder
	if kw != nil {
		borderColor = kw.borderColor
	}

	borderStyle := lipgloss.NewStyle().
		Width(i.width).
		Border(lipgloss.RoundedBorder(), true, false, true, false). // top + bottom
		BorderForeground(borderColor).
		Padding(0, 1)

	content := i.textarea.View()

	// Highlight the keyword text in the rendered output.
	if kw != nil {
		content = highlightKeywordInView(content, kw.prefix, kw.color)
	}

	return borderStyle.Render(content)
}

// Focused returns whether the textarea has focus.
func (i *Input) Focused() bool {
	return i.textarea.Focused()
}
