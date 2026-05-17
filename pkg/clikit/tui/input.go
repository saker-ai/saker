package tui

import (
	"image/color"
	"strings"
	"sync"

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

// completionState tracks Tab-completion for slash commands.
type completionState struct {
	active   bool
	matches  []string
	selected int
}

// searchState tracks Ctrl+R reverse history search.
type searchState struct {
	active bool
	query  string
	idx    int // position in history being shown (-1 = no match)
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

	completion    completionState
	extraCommands []string // dynamic commands (skills, etc.)
	search        searchState
}

var keywordColorsOnce sync.Once

// NewInput creates an Input component.
func NewInput(s Styles) *Input {
	keywordColorsOnce.Do(func() { initKeywordColors(s.Theme) })

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
	const maxHistorySize = 500
	if len(i.history) > maxHistorySize {
		i.history = i.history[len(i.history)-maxHistorySize:]
	}
	i.historyIdx = -1
	i.historyDraft = ""
}

// SetExtraCommands sets additional slash commands for Tab completion (e.g., skill names).
func (i *Input) SetExtraCommands(cmds []string) {
	i.extraCommands = cmds
}

// allCommands returns built-in keywords + extra commands for completion.
func (i *Input) allCommands() []string {
	cmds := make([]string, 0, len(inputKeywords)+len(i.extraCommands))
	for _, kw := range inputKeywords {
		cmds = append(cmds, kw.prefix)
	}
	cmds = append(cmds, i.extraCommands...)
	return cmds
}

// filterCommands returns commands matching the given prefix.
func filterCommands(cmds []string, prefix string) []string {
	prefix = strings.ToLower(prefix)
	var out []string
	for _, c := range cmds {
		if strings.HasPrefix(strings.ToLower(c), prefix) {
			out = append(out, c)
		}
	}
	return out
}

// searchHistory finds the next match (searching backward from idx) for query.
func (i *Input) searchHistory(query string, startIdx int) (string, int) {
	lower := strings.ToLower(query)
	for idx := startIdx; idx >= 0; idx-- {
		if strings.Contains(strings.ToLower(i.history[idx]), lower) {
			return i.history[idx], idx
		}
	}
	return "", -1
}

// Update handles key events for the textarea.
// Input is always editable (type-ahead); submission is gated by enabled.
// When the textarea is single-line, up/down arrows navigate input history.
func (i *Input) Update(msg tea.Msg) (*Input, tea.Cmd) {
	if km, ok := msg.(tea.KeyPressMsg); ok {
		// Ctrl+R: enter/continue reverse search.
		if km.String() == "ctrl+r" && len(i.history) > 0 {
			if !i.search.active {
				i.search.active = true
				i.search.query = ""
				i.search.idx = len(i.history) - 1
			} else if i.search.idx > 0 {
				_, i.search.idx = i.searchHistory(i.search.query, i.search.idx-1)
			}
			return i, nil
		}

		// Handle keys during search mode.
		if i.search.active {
			switch km.String() {
			case "esc":
				i.search.active = false
				return i, nil
			case "enter":
				if i.search.idx >= 0 {
					i.textarea.SetValue(i.history[i.search.idx])
					i.textarea.CursorEnd()
				}
				i.search.active = false
				return i, nil
			case "backspace":
				if len(i.search.query) > 0 {
					i.search.query = i.search.query[:len(i.search.query)-1]
					_, i.search.idx = i.searchHistory(i.search.query, len(i.history)-1)
				}
				return i, nil
			default:
				r := km.String()
				if len(r) == 1 && r[0] >= 32 {
					i.search.query += r
					start := len(i.history) - 1
					if i.search.idx >= 0 {
						start = i.search.idx
					}
					_, i.search.idx = i.searchHistory(i.search.query, start)
				}
				return i, nil
			}
		}

		// Tab: slash command completion.
		if km.String() == "tab" {
			value := strings.TrimSpace(i.textarea.Value())
			if i.completion.active {
				if len(i.completion.matches) > 0 {
					i.completion.selected = (i.completion.selected + 1) % len(i.completion.matches)
				}
				return i, nil
			}
			if strings.HasPrefix(value, "/") && i.textarea.LineCount() <= 1 {
				matches := filterCommands(i.allCommands(), value)
				if len(matches) == 1 {
					i.textarea.SetValue(matches[0] + " ")
					i.textarea.CursorEnd()
					return i, nil
				}
				if len(matches) > 0 {
					i.completion.active = true
					i.completion.matches = matches
					i.completion.selected = 0
					return i, nil
				}
			}
			return i, nil
		}

		// Handle keys during completion.
		if i.completion.active {
			switch km.String() {
			case "esc":
				i.completion.active = false
				return i, nil
			case "up":
				if i.completion.selected > 0 {
					i.completion.selected--
				}
				return i, nil
			case "down":
				if i.completion.selected < len(i.completion.matches)-1 {
					i.completion.selected++
				}
				return i, nil
			case "enter":
				if len(i.completion.matches) > 0 {
					i.textarea.SetValue(i.completion.matches[i.completion.selected] + " ")
					i.textarea.CursorEnd()
				}
				i.completion.active = false
				return i, nil
			default:
				i.completion.active = false
			}
		}
	}

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
		// Both modes require: exact match (rest == "") or rest starts with space.
		// Branches were intended to differ but converged; keep one check until a
		// distinct rule is needed.
		if rest != "" && rest[0] != ' ' {
			continue
		}
		_ = kw.needsSpace
		return kw
	}
	return nil
}

// highlightKeywordInView replaces the first occurrence of the keyword text
// in the rendered textarea output with a colored version.
// ANSI color codes are zero-width, so cursor positioning is preserved.
func highlightKeywordInView(rendered, keyword string, c color.Color) string {
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

	var parts []string

	// Completion overlay above the input.
	if i.completion.active && len(i.completion.matches) > 0 {
		max := 5
		if len(i.completion.matches) < max {
			max = len(i.completion.matches)
		}
		var lines []string
		for idx := 0; idx < max; idx++ {
			m := i.completion.matches[idx]
			if idx == i.completion.selected {
				lines = append(lines, lipgloss.NewStyle().
					Foreground(i.styles.Theme.Fg).
					Background(i.styles.Theme.UserMsgBg).
					Bold(true).Render("  "+m+"  "))
			} else {
				lines = append(lines, lipgloss.NewStyle().
					Foreground(i.styles.Theme.FgDim).Render("  "+m))
			}
		}
		overlay := strings.Join(lines, "\n")
		parts = append(parts, overlay)
	}

	// Search mode indicator.
	if i.search.active {
		prompt := lipgloss.NewStyle().Foreground(i.styles.Theme.Warning).Render("(reverse-i-search)")
		query := lipgloss.NewStyle().Foreground(i.styles.Theme.Fg).Render(i.search.query)
		result := ""
		if i.search.idx >= 0 && i.search.idx < len(i.history) {
			result = lipgloss.NewStyle().Foreground(i.styles.Theme.FgDim).
				Render(": " + truncInputLine(i.history[i.search.idx], i.width-30))
		}
		parts = append(parts, prompt+query+result)
	}

	parts = append(parts, borderStyle.Render(content))
	return strings.Join(parts, "\n")
}

func truncInputLine(s string, max int) string {
	if max < 10 {
		max = 10
	}
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}

// Focused returns whether the textarea has focus.
func (i *Input) Focused() bool {
	return i.textarea.Focused()
}
