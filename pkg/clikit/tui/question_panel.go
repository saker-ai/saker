package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	toolbuiltin "github.com/saker-ai/saker/pkg/tool/builtin"
)

// otherMenuLabel is the special trailing menu item that lets the user provide
// a free-form answer per the AskUserQuestion spec ("Users will always be able
// to select Other to provide custom text input").
const otherMenuLabel = "Other..."

// questionPanelMode tracks which sub-UI is active for a given question.
// The review screen is identified by currentIdx==len(questions), not by mode.
type questionPanelMode int

const (
	qmodeSelect questionPanelMode = iota
	qmodeOtherInput
)

// QuestionPanelOutcome is delivered on the panel's outcome channel when the
// user finishes interacting with the panel. Cancelled means the user pressed
// Esc/Ctrl+C or chose Cancel on the review screen; in that case Answers is nil.
type QuestionPanelOutcome struct {
	Answers   map[string]string
	Cancelled bool
}

// QuestionPanel renders the AskUserQuestion interactive form inside the TUI.
//
// UX (v2, mirrors claude-code):
//   - Top tab bar shows every question with checkbox status (☐/✓).
//   - User navigates freely between questions with Tab/Shift+Tab or ←/→.
//   - Per-question state (cursor, multi-select bits, Other text, mode) is
//     preserved when revisiting a question.
//   - Enter on a question records the answer and smart-advances to the next
//     unanswered question, or to the review screen when all are answered.
//   - The review screen lists all Q&A and offers Submit/Cancel.
//   - Single question + single-select short-circuits review (Enter delivers).
type QuestionPanel struct {
	styles    Styles
	questions []toolbuiltin.Question

	width  int
	height int

	// currentIdx in [0, len(questions)] — equals len(questions) at the review screen
	// (only valid when hasReviewStep is true).
	currentIdx int

	// Per-question state, keyed by question index. Defaults are zero values
	// (cursor=0, mode=qmodeSelect, otherText="", no selections), so missing
	// keys behave as if the user had never visited that question.
	cursors    map[int]int
	selected   map[int]map[int]bool
	otherTexts map[int]string
	modes      map[int]questionPanelMode

	// Review screen state.
	reviewCursor  int  // 0=Submit, 1=Cancel
	hasReviewStep bool // false only for single-question single-select (immediate submit)

	answers  map[string]string
	outcome  chan QuestionPanelOutcome
	finished bool
}

// NewQuestionPanel creates a panel for the given question list and returns a
// channel on which the final outcome is delivered exactly once. The channel
// is buffered (size 1) so callers may select on it without risk of blocking
// the bubbletea event loop.
func NewQuestionPanel(s Styles, qs []toolbuiltin.Question) (*QuestionPanel, <-chan QuestionPanelOutcome) {
	out := make(chan QuestionPanelOutcome, 1)
	// Mirror claude-code's hideSubmitTab: the only case where we skip the review
	// step is single-question single-select — that interaction is unambiguous and
	// a separate confirmation step would feel like friction.
	hasReview := !(len(qs) == 1 && !qs[0].MultiSelect)
	p := &QuestionPanel{
		styles:        s,
		questions:     append([]toolbuiltin.Question(nil), qs...),
		cursors:       make(map[int]int),
		selected:      make(map[int]map[int]bool),
		otherTexts:    make(map[int]string),
		modes:         make(map[int]questionPanelMode),
		hasReviewStep: hasReview,
		answers:       make(map[string]string),
		outcome:       out,
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
	if p.isAtReview() {
		return p.handleReviewKey(msg)
	}
	if p.currentMode() == qmodeOtherInput {
		return p.handleOtherInputKey(msg)
	}
	return p.handleSelectKey(msg)
}

func (p *QuestionPanel) handleSelectKey(msg tea.KeyPressMsg) tea.Cmd {
	q := p.currentQuestion()
	if q == nil {
		return nil
	}
	totalItems := len(q.Options) + 1 // +1 for the "Other..." item

	switch msg.String() {
	case "esc", "ctrl+c":
		p.Cancel()
		return nil
	case "tab", "right":
		p.gotoTab(p.currentIdx + 1)
		return nil
	case "shift+tab", "left":
		p.gotoTab(p.currentIdx - 1)
		return nil
	case "up", "k":
		c := p.currentCursor()
		if c > 0 {
			p.setCursor(c - 1)
		}
		return nil
	case "down", "j":
		c := p.currentCursor()
		if c < totalItems-1 {
			p.setCursor(c + 1)
		}
		return nil
	case "home", "g":
		p.setCursor(0)
		return nil
	case "end", "G":
		p.setCursor(totalItems - 1)
		return nil
	case "space", " ":
		// Space toggles selection in multi-select mode (no-op on Other).
		if q.MultiSelect && p.currentCursor() < len(q.Options) {
			sel := p.ensureSelectionMap(p.currentIdx)
			c := p.currentCursor()
			sel[c] = !sel[c]
		}
		return nil
	case "enter":
		// Enter on the trailing "Other..." item: open free-form input.
		if p.currentCursor() == len(q.Options) {
			p.setMode(qmodeOtherInput)
			return nil
		}
		if q.MultiSelect {
			// Multi-select: Enter on a regular option toggles+submits.
			// If nothing is yet selected, treat current cursor row as selected.
			sel := p.ensureSelectionMap(p.currentIdx)
			if !p.hasAnySelection(p.currentIdx) {
				sel[p.currentCursor()] = true
			}
			return p.submitCurrent()
		}
		// Single-select: Enter picks the current row.
		p.answers[q.Question] = q.Options[p.currentCursor()].Label
		return p.advance()
	}
	return nil
}

func (p *QuestionPanel) handleOtherInputKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		// Back to selection mode; keep typed text so user can re-enter and resume.
		p.setMode(qmodeSelect)
		return nil
	case "ctrl+c":
		p.Cancel()
		return nil
	case "tab", "right":
		// Tab away preserves text and mode so revisit picks up where we left off.
		p.gotoTab(p.currentIdx + 1)
		return nil
	case "shift+tab", "left":
		p.gotoTab(p.currentIdx - 1)
		return nil
	case "enter":
		text := strings.TrimSpace(p.currentOtherText())
		if text == "" {
			return nil
		}
		q := p.currentQuestion()
		if q == nil {
			return nil
		}
		p.answers[q.Question] = text
		p.setMode(qmodeSelect)
		return p.advance()
	case "backspace":
		t := p.currentOtherText()
		if n := len(t); n > 0 {
			r := []rune(t)
			p.setOtherText(string(r[:len(r)-1]))
		}
		return nil
	}
	// Treat any other single printable character as input.
	if msg.Mod == 0 && msg.Code >= 0x20 && msg.Code != 0x7f {
		p.setOtherText(p.currentOtherText() + string(msg.Code))
	}
	return nil
}

func (p *QuestionPanel) handleReviewKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc", "ctrl+c":
		p.Cancel()
		return nil
	case "up", "k":
		p.reviewCursor = 0
		return nil
	case "down", "j":
		p.reviewCursor = 1
		return nil
	case "shift+tab", "left":
		// Go back to the last question for editing.
		if len(p.questions) > 0 {
			p.gotoTab(len(p.questions) - 1)
		}
		return nil
	case "tab", "right":
		// Already at the rightmost tab — no-op.
		return nil
	case "enter":
		if p.reviewCursor == 0 {
			return p.deliverAnswers()
		}
		p.Cancel()
		return nil
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
		// Nothing selected: ignore Enter so the user must pick something.
		return nil
	}
	p.answers[q.Question] = strings.Join(labels, ",")
	return p.advance()
}

// advance is called after a single-question submission. It either short-circuits
// for the single-question single-select case, jumps to the next unanswered
// question, or moves to the review screen.
func (p *QuestionPanel) advance() tea.Cmd {
	if !p.hasReviewStep {
		// Single question + single-select: deliver immediately.
		return p.deliverAnswers()
	}
	if next := p.firstUnansweredAfter(p.currentIdx); next >= 0 {
		p.gotoTab(next)
		return nil
	}
	p.gotoReview()
	return nil
}

// gotoTab moves to a tab index, clamping to [0, lastTabIdx]. The destination's
// cursor and mode are picked up from the per-question maps automatically.
func (p *QuestionPanel) gotoTab(idx int) {
	if idx < 0 {
		idx = 0
	}
	last := p.lastTabIdx()
	if idx > last {
		idx = last
	}
	p.currentIdx = idx
}

func (p *QuestionPanel) gotoReview() {
	if !p.hasReviewStep {
		return
	}
	p.currentIdx = len(p.questions)
	p.reviewCursor = 0
}

// firstUnansweredAfter searches from i+1 forward (then wraps from 0..i-1) for
// the first question that has no entry in p.answers. Returns -1 if all answered.
func (p *QuestionPanel) firstUnansweredAfter(i int) int {
	n := len(p.questions)
	for off := 1; off <= n; off++ {
		k := (i + off) % n
		if _, ok := p.answers[p.questions[k].Question]; !ok {
			return k
		}
	}
	return -1
}

func (p *QuestionPanel) deliverAnswers() tea.Cmd {
	copyAns := make(map[string]string, len(p.answers))
	for k, v := range p.answers {
		copyAns[k] = v
	}
	p.deliver(QuestionPanelOutcome{Answers: copyAns})
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
		// Buffer is 1; should never be full. Drop defensively.
	}
}

// ---- per-question state accessors -------------------------------------------

func (p *QuestionPanel) isAtReview() bool {
	return p.hasReviewStep && p.currentIdx == len(p.questions)
}

func (p *QuestionPanel) lastTabIdx() int {
	if p.hasReviewStep {
		return len(p.questions) // review tab
	}
	return len(p.questions) - 1
}

func (p *QuestionPanel) currentQuestion() *toolbuiltin.Question {
	if p.currentIdx < 0 || p.currentIdx >= len(p.questions) {
		return nil
	}
	return &p.questions[p.currentIdx]
}

func (p *QuestionPanel) currentMode() questionPanelMode { return p.modes[p.currentIdx] }
func (p *QuestionPanel) setMode(m questionPanelMode)    { p.modes[p.currentIdx] = m }

func (p *QuestionPanel) currentCursor() int     { return p.cursors[p.currentIdx] }
func (p *QuestionPanel) setCursor(c int)        { p.cursors[p.currentIdx] = c }
func (p *QuestionPanel) currentOtherText() string { return p.otherTexts[p.currentIdx] }
func (p *QuestionPanel) setOtherText(t string)    { p.otherTexts[p.currentIdx] = t }

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

// ---- rendering --------------------------------------------------------------

// View renders the panel content. Caller decides where to place it on screen.
func (p *QuestionPanel) View() string {
	if p.finished {
		return ""
	}
	var b strings.Builder
	b.WriteString(p.renderNavBar())
	b.WriteString("\n")
	if p.isAtReview() {
		b.WriteString(p.renderReview())
	} else {
		b.WriteString(p.renderQuestion())
	}
	b.WriteString("\n")
	b.WriteString(p.renderHintBar())
	return b.String()
}

func (p *QuestionPanel) renderNavBar() string {
	t := p.styles.Theme
	activeStyle := lipgloss.NewStyle().Background(t.Primary).Foreground(t.Bg).Bold(true).Padding(0, 1)
	answeredStyle := lipgloss.NewStyle().Foreground(t.Success).Padding(0, 1)
	pendingStyle := lipgloss.NewStyle().Foreground(t.FgDim).Padding(0, 1)

	parts := make([]string, 0, len(p.questions)+1)
	for i, q := range p.questions {
		header := q.Header
		if header == "" {
			header = fmt.Sprintf("Q%d", i+1)
		}
		check := "☐"
		_, answered := p.answers[q.Question]
		if answered {
			check = "✓"
		}
		label := check + " " + header
		switch {
		case p.currentIdx == i:
			parts = append(parts, activeStyle.Render(label))
		case answered:
			parts = append(parts, answeredStyle.Render(label))
		default:
			parts = append(parts, pendingStyle.Render(label))
		}
	}
	if p.hasReviewStep {
		label := "✓ Submit"
		if p.isAtReview() {
			parts = append(parts, activeStyle.Render(label))
		} else {
			parts = append(parts, pendingStyle.Render(label))
		}
	}
	return "  " + strings.Join(parts, "")
}

func (p *QuestionPanel) renderQuestion() string {
	q := p.currentQuestion()
	if q == nil {
		return ""
	}
	t := p.styles.Theme
	titleStyle := lipgloss.NewStyle().Foreground(t.Primary).Bold(true)
	headerStyle := lipgloss.NewStyle().Foreground(t.Secondary)
	descStyle := lipgloss.NewStyle().Foreground(t.FgDim)
	cursorStyle := lipgloss.NewStyle().Foreground(t.Primary).Bold(true)
	selectedStyle := lipgloss.NewStyle().Foreground(t.Success)
	textStyle := lipgloss.NewStyle().Foreground(t.Fg)

	var b strings.Builder
	progress := ""
	if len(p.questions) > 1 {
		progress = descStyle.Render(fmt.Sprintf(" — Question %d of %d", p.currentIdx+1, len(p.questions)))
	}
	b.WriteString("\n  " + titleStyle.Render("[?]") + " " + headerStyle.Render(q.Header) + progress)
	b.WriteString("\n  " + textStyle.Render(q.Question))
	b.WriteString("\n  ")
	if q.MultiSelect {
		b.WriteString(descStyle.Render("(multi-select — Space to toggle)"))
	} else {
		b.WriteString(descStyle.Render("(single-select)"))
	}
	b.WriteString("\n\n")

	if p.currentMode() == qmodeOtherInput {
		prompt := lipgloss.NewStyle().Foreground(t.Primary).Render("Other > ")
		input := p.currentOtherText() + IconCursor
		b.WriteString("  " + prompt + input + "\n")
		return b.String()
	}

	sel := p.selected[p.currentIdx]
	cursor := p.currentCursor()
	for i, opt := range q.Options {
		marker := "  "
		if q.MultiSelect {
			if sel[i] {
				marker = selectedStyle.Render("[x] ")
			} else {
				marker = "[ ] "
			}
		}
		cm := "  "
		if cursor == i {
			cm = cursorStyle.Render("▶ ")
		}
		line := cm + marker + opt.Label
		if opt.Description != "" {
			line += descStyle.Render(" — " + opt.Description)
		}
		b.WriteString("  " + line + "\n")
	}
	// Trailing "Other..." item — show preserved text in parens if any.
	cm := "  "
	if cursor == len(q.Options) {
		cm = cursorStyle.Render("▶ ")
	}
	otherLabel := descStyle.Render("[+] " + otherMenuLabel)
	if existing := p.currentOtherText(); existing != "" {
		otherLabel += descStyle.Render(" (" + existing + ")")
	}
	b.WriteString("  " + cm + otherLabel + "\n")

	return b.String()
}

func (p *QuestionPanel) renderReview() string {
	t := p.styles.Theme
	titleStyle := lipgloss.NewStyle().Foreground(t.Primary).Bold(true)
	qStyle := lipgloss.NewStyle().Foreground(t.Fg)
	aStyle := lipgloss.NewStyle().Foreground(t.Success)
	descStyle := lipgloss.NewStyle().Foreground(t.FgDim)
	warnStyle := lipgloss.NewStyle().Foreground(t.Warning)
	cursorStyle := lipgloss.NewStyle().Foreground(t.Primary).Bold(true)

	var b strings.Builder
	b.WriteString("\n  " + titleStyle.Render("[✓] Review your answers"))
	b.WriteString("\n\n")

	answered := 0
	for _, q := range p.questions {
		if _, ok := p.answers[q.Question]; ok {
			answered++
		}
	}
	if answered < len(p.questions) {
		b.WriteString("  " + warnStyle.Render(
			fmt.Sprintf("⚠ Not all questions answered (%d/%d)", answered, len(p.questions))))
		b.WriteString("\n\n")
	}

	for _, q := range p.questions {
		b.WriteString("  • " + qStyle.Render(q.Question) + "\n")
		if a, ok := p.answers[q.Question]; ok {
			b.WriteString("    → " + aStyle.Render(a) + "\n")
		} else {
			b.WriteString("    → " + descStyle.Render("(unanswered)") + "\n")
		}
	}

	b.WriteString("\n  " + descStyle.Render("Ready to submit your answers?"))
	b.WriteString("\n\n")

	submitMark := "  "
	cancelMark := "  "
	if p.reviewCursor == 0 {
		submitMark = cursorStyle.Render("▶ ")
	} else {
		cancelMark = cursorStyle.Render("▶ ")
	}
	b.WriteString("  " + submitMark + "Submit answers\n")
	b.WriteString("  " + cancelMark + "Cancel\n")
	return b.String()
}

func (p *QuestionPanel) renderHintBar() string {
	hintStyle := lipgloss.NewStyle().Foreground(p.styles.Theme.FgDim).Italic(true)
	var hint string
	switch {
	case p.isAtReview():
		hint = "↑/↓ select · Enter confirm · Tab/← back · Esc cancel"
	case p.currentMode() == qmodeOtherInput:
		hint = "Type · Enter submit · Esc back · Tab/Shift+Tab switch · Ctrl+C cancel"
	default:
		q := p.currentQuestion()
		base := "↑/↓ move · Enter pick"
		if q != nil && q.MultiSelect {
			base = "↑/↓ move · Space toggle · Enter submit"
		}
		if len(p.questions) > 1 || p.hasReviewStep {
			base += " · Tab/Shift+Tab switch question"
		}
		base += " · Esc cancel"
		hint = base
	}
	return "  " + hintStyle.Render(hint)
}
