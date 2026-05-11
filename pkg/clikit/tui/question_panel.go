package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	toolbuiltin "github.com/cinience/saker/pkg/tool/builtin"
)

// otherMenuLabel is the special trailing menu item that lets the user provide
// a free-form answer per the AskUserQuestion spec ("Users will always be able
// to select Other to provide custom text input").
const otherMenuLabel = "Other..."

// questionPanelMode tracks which sub-UI is active inside the panel.
type questionPanelMode int

const (
	qmodeSelect questionPanelMode = iota
	qmodeOtherInput
)

// QuestionPanelOutcome is delivered on the panel's outcome channel when the
// user finishes interacting with the panel. Cancelled means the user pressed
// Esc/Ctrl+C; in that case Answers is nil.
type QuestionPanelOutcome struct {
	Answers   map[string]string
	Cancelled bool
}

// QuestionPanel renders the AskUserQuestion interactive form inside the TUI.
// It walks through each question in order, supporting single-select,
// multi-select, and a free-form "Other" override per question.
type QuestionPanel struct {
	styles    Styles
	questions []toolbuiltin.Question

	width  int
	height int

	currentIdx int                  // which question is shown
	cursor     int                  // current option index (0..len(options) is "Other..." item)
	selected   map[int]map[int]bool // {questionIdx: {optionIdx: true}} for multi-select

	mode      questionPanelMode
	otherText string

	answers  map[string]string // filled progressively, sent on completion
	outcome  chan QuestionPanelOutcome
	finished bool // guard against duplicate sends
}

// NewQuestionPanel creates a panel for the given question list and returns a
// channel on which the final outcome is delivered exactly once. The channel
// is buffered (size 1) so callers may select on it without risk of blocking
// the bubbletea event loop.
func NewQuestionPanel(s Styles, qs []toolbuiltin.Question) (*QuestionPanel, <-chan QuestionPanelOutcome) {
	out := make(chan QuestionPanelOutcome, 1)
	p := &QuestionPanel{
		styles:    s,
		questions: append([]toolbuiltin.Question(nil), qs...),
		selected:  make(map[int]map[int]bool),
		answers:   make(map[string]string),
		outcome:   out,
	}
	return p, out
}

// SetSize updates panel dimensions. Called from the App layout pass.
func (p *QuestionPanel) SetSize(w, h int) {
	p.width = w
	p.height = h
}

// IsFinished reports whether the panel has already delivered an outcome.
func (p *QuestionPanel) IsFinished() bool {
	return p.finished
}

// Cancel completes the panel with a cancelled outcome. Safe to call multiple
// times; only the first call delivers.
func (p *QuestionPanel) Cancel() {
	p.deliver(QuestionPanelOutcome{Cancelled: true})
}

// HandleKey processes a key press. Returns a tea.Cmd that the App should run
// (typically a no-op or a follow-up Msg). The panel handles its own state;
// the App only needs to detect IsFinished after a key to remove the panel.
func (p *QuestionPanel) HandleKey(msg tea.KeyPressMsg) tea.Cmd {
	if p.finished {
		return nil
	}
	if p.mode == qmodeOtherInput {
		return p.handleOtherInputKey(msg)
	}
	return p.handleSelectKey(msg)
}

func (p *QuestionPanel) handleSelectKey(msg tea.KeyPressMsg) tea.Cmd {
	q := p.currentQuestion()
	if q == nil {
		return nil
	}
	totalItems := len(q.Options) + 1 // + "Other..."

	switch msg.String() {
	case "esc", "ctrl+c":
		p.Cancel()
		return nil
	case "up", "k":
		if p.cursor > 0 {
			p.cursor--
		}
		return nil
	case "down", "j":
		if p.cursor < totalItems-1 {
			p.cursor++
		}
		return nil
	case "home", "g":
		p.cursor = 0
		return nil
	case "end", "G":
		p.cursor = totalItems - 1
		return nil
	case "space", " ":
		// Space toggles selection in multi-select mode (no-op on Other).
		if q.MultiSelect && p.cursor < len(q.Options) {
			sel := p.ensureSelectionMap(p.currentIdx)
			sel[p.cursor] = !sel[p.cursor]
		}
		return nil
	case "enter":
		// On "Other..." item: open free-form input regardless of mode.
		if p.cursor == len(q.Options) {
			p.mode = qmodeOtherInput
			p.otherText = ""
			return nil
		}
		if q.MultiSelect {
			// Multi-select: Enter on a regular option toggles+submits.
			// If nothing is yet selected, treat current cursor row as selected.
			sel := p.ensureSelectionMap(p.currentIdx)
			if !p.hasAnySelection(p.currentIdx) {
				sel[p.cursor] = true
			}
			return p.submitCurrent()
		}
		// Single-select: Enter picks the current row.
		p.answers[q.Question] = q.Options[p.cursor].Label
		return p.advance()
	}
	return nil
}

func (p *QuestionPanel) handleOtherInputKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		// Cancel input: back to selection mode (don't cancel the whole panel).
		p.mode = qmodeSelect
		p.otherText = ""
		return nil
	case "ctrl+c":
		p.Cancel()
		return nil
	case "enter":
		text := strings.TrimSpace(p.otherText)
		if text == "" {
			return nil
		}
		q := p.currentQuestion()
		if q == nil {
			return nil
		}
		p.answers[q.Question] = text
		p.mode = qmodeSelect
		p.otherText = ""
		return p.advance()
	case "backspace":
		if n := len(p.otherText); n > 0 {
			r := []rune(p.otherText)
			p.otherText = string(r[:len(r)-1])
		}
		return nil
	}
	// Treat any other single printable character as input.
	if msg.Mod == 0 && msg.Code >= 0x20 && msg.Code != 0x7f {
		p.otherText += string(msg.Code)
	}
	return nil
}

// submitCurrent gathers multi-select choices for the current question and advances.
func (p *QuestionPanel) submitCurrent() tea.Cmd {
	q := p.currentQuestion()
	if q == nil {
		return nil
	}
	sel := p.selected[p.currentIdx]
	labels := make([]string, 0, len(sel))
	for i, opt := range q.Options {
		if sel[i] {
			labels = append(labels, opt.Label)
		}
	}
	if len(labels) == 0 {
		// nothing selected: ignore Enter so the user must pick something.
		return nil
	}
	p.answers[q.Question] = strings.Join(labels, ",")
	return p.advance()
}

// advance moves to the next question or finishes the panel.
func (p *QuestionPanel) advance() tea.Cmd {
	p.currentIdx++
	if p.currentIdx >= len(p.questions) {
		// All questions answered.
		copyAns := make(map[string]string, len(p.answers))
		for k, v := range p.answers {
			copyAns[k] = v
		}
		p.deliver(QuestionPanelOutcome{Answers: copyAns})
		return nil
	}
	// Reset cursor for next question.
	p.cursor = 0
	p.mode = qmodeSelect
	p.otherText = ""
	return nil
}

func (p *QuestionPanel) deliver(o QuestionPanelOutcome) {
	if p.finished {
		return
	}
	p.finished = true
	select {
	case p.outcome <- o:
	default:
		// Buffer is 1; if it's somehow full (shouldn't happen), drop.
	}
}

func (p *QuestionPanel) currentQuestion() *toolbuiltin.Question {
	if p.currentIdx < 0 || p.currentIdx >= len(p.questions) {
		return nil
	}
	return &p.questions[p.currentIdx]
}

func (p *QuestionPanel) ensureSelectionMap(qIdx int) map[int]bool {
	if p.selected[qIdx] == nil {
		p.selected[qIdx] = make(map[int]bool)
	}
	return p.selected[qIdx]
}

func (p *QuestionPanel) hasAnySelection(qIdx int) bool {
	for _, v := range p.selected[qIdx] {
		if v {
			return true
		}
	}
	return false
}

// View renders the panel content. Caller decides where to place it on screen.
func (p *QuestionPanel) View() string {
	if p.finished {
		return ""
	}
	q := p.currentQuestion()
	if q == nil {
		return ""
	}

	w := p.width
	if w < 20 {
		w = 80
	}
	innerW := w - 4
	if innerW < 20 {
		innerW = 20
	}

	titleStyle := lipgloss.NewStyle().Foreground(p.styles.Theme.Primary).Bold(true)
	headerStyle := lipgloss.NewStyle().Foreground(p.styles.Theme.Secondary)
	descStyle := lipgloss.NewStyle().Foreground(p.styles.Theme.FgDim)
	cursorStyle := lipgloss.NewStyle().Foreground(p.styles.Theme.Primary).Bold(true)
	selectedStyle := lipgloss.NewStyle().Foreground(p.styles.Theme.Success)
	hintStyle := lipgloss.NewStyle().Foreground(p.styles.Theme.FgDim).Italic(true)

	var b strings.Builder

	// Top border with progress.
	progress := fmt.Sprintf(" Question %d of %d ", p.currentIdx+1, len(p.questions))
	titleBar := titleStyle.Render("[?]") + " " + headerStyle.Render(q.Header) + descStyle.Render(progress)
	b.WriteString("─ " + titleBar)
	b.WriteString("\n")

	// Question text.
	b.WriteString("  " + lipgloss.NewStyle().Foreground(p.styles.Theme.Fg).Render(q.Question))
	b.WriteString("\n")
	if q.MultiSelect {
		b.WriteString("  " + descStyle.Render("(multi-select — Space to toggle, Enter to submit)"))
	} else {
		b.WriteString("  " + descStyle.Render("(single-select — Enter to choose)"))
	}
	b.WriteString("\n\n")

	if p.mode == qmodeOtherInput {
		// Inline input UI.
		prompt := lipgloss.NewStyle().Foreground(p.styles.Theme.Primary).Render("Other > ")
		input := p.otherText + IconCursor
		b.WriteString("  " + prompt + input)
		b.WriteString("\n\n")
		b.WriteString("  " + hintStyle.Render("Enter to submit · Esc to go back · Ctrl+C to cancel"))
		return b.String()
	}

	// Render options.
	sel := p.selected[p.currentIdx]
	for i, opt := range q.Options {
		isCursor := p.cursor == i
		marker := "  "
		if q.MultiSelect {
			if sel[i] {
				marker = selectedStyle.Render("[x] ")
			} else {
				marker = "[ ] "
			}
		}
		cursorMark := "  "
		if isCursor {
			cursorMark = cursorStyle.Render("▶ ")
		}
		line := cursorMark + marker + opt.Label
		if opt.Description != "" {
			line += descStyle.Render(" — " + opt.Description)
		}
		b.WriteString("  " + line + "\n")
	}
	// Trailing "Other..." item.
	otherCursor := "  "
	if p.cursor == len(q.Options) {
		otherCursor = cursorStyle.Render("▶ ")
	}
	b.WriteString("  " + otherCursor + descStyle.Render("[+] "+otherMenuLabel) + "\n")

	// Hint bar.
	b.WriteString("\n")
	hint := "↑/↓ move · Enter pick · Esc cancel"
	if q.MultiSelect {
		hint = "↑/↓ move · Space toggle · Enter submit · Esc cancel"
	}
	b.WriteString("  " + hintStyle.Render(hint))

	return b.String()
}
