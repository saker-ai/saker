package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	toolbuiltin "github.com/cinience/saker/pkg/tool/builtin"
)

func keyRune(r rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: r} }

var (
	keyEnterMsg    = tea.KeyPressMsg{Code: tea.KeyEnter}
	keyEscMsg      = tea.KeyPressMsg{Code: tea.KeyEsc}
	keyDownMsg     = tea.KeyPressMsg{Code: tea.KeyDown}
	keyUpMsg       = tea.KeyPressMsg{Code: tea.KeyUp}
	keySpaceMsg    = tea.KeyPressMsg{Code: tea.KeySpace}
	keyBackMsg     = tea.KeyPressMsg{Code: tea.KeyBackspace}
	keyTabMsg      = tea.KeyPressMsg{Code: tea.KeyTab}
	keyShiftTabMsg = tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
)

// cursorOf returns the cursor position for the panel's current question.
func cursorOf(p *QuestionPanel) int { return p.cursors[p.currentIdx] }

// modeOf returns the active mode for the panel's current question.
func modeOf(p *QuestionPanel) questionPanelMode { return p.modes[p.currentIdx] }

// otherTextOf returns the Other text for the panel's current question.
func otherTextOf(p *QuestionPanel) string { return p.otherTexts[p.currentIdx] }

func newPanelWithSingleQuestion() (*QuestionPanel, <-chan QuestionPanelOutcome) {
	q := toolbuiltin.Question{
		Question: "Pick a color?",
		Header:   "Color",
		Options: []toolbuiltin.QuestionOption{
			{Label: "Red", Description: "warm"},
			{Label: "Green", Description: "cool"},
			{Label: "Blue", Description: "cold"},
		},
		MultiSelect: false,
	}
	return NewQuestionPanel(NewStyles(DefaultTheme()), []toolbuiltin.Question{q})
}

// Two single-select questions — exercises the smart-advance + review flow.
func newPanelWithTwoQuestions() (*QuestionPanel, <-chan QuestionPanelOutcome) {
	q1 := toolbuiltin.Question{
		Question:    "Q1?",
		Header:      "1",
		Options:     []toolbuiltin.QuestionOption{{Label: "A", Description: ""}, {Label: "B", Description: ""}},
		MultiSelect: false,
	}
	q2 := toolbuiltin.Question{
		Question:    "Q2?",
		Header:      "2",
		Options:     []toolbuiltin.QuestionOption{{Label: "X", Description: ""}, {Label: "Y", Description: ""}, {Label: "Z", Description: ""}},
		MultiSelect: false,
	}
	return NewQuestionPanel(NewStyles(DefaultTheme()), []toolbuiltin.Question{q1, q2})
}

// Single-select single-question short-circuits review and delivers immediately.
func TestQuestionPanel_SingleSelect_PicksLabel(t *testing.T) {
	p, out := newPanelWithSingleQuestion()
	p.HandleKey(keyDownMsg) // Red → Green
	p.HandleKey(keyEnterMsg)
	if !p.IsFinished() {
		t.Fatalf("panel should be finished after Enter")
	}
	got := <-out
	if got.Cancelled {
		t.Fatalf("should not be cancelled")
	}
	if got.Answers["Pick a color?"] != "Green" {
		t.Fatalf("expected Green, got %q", got.Answers["Pick a color?"])
	}
}

// Single-select: arrow up clamps at 0; arrow down clamps at last (Other) index.
func TestQuestionPanel_SingleSelect_CursorBounds(t *testing.T) {
	p, _ := newPanelWithSingleQuestion()
	p.HandleKey(keyUpMsg)
	if cursorOf(p) != 0 {
		t.Fatalf("cursor should stay at 0 when going up at top, got %d", cursorOf(p))
	}
	for i := 0; i < 10; i++ {
		p.HandleKey(keyDownMsg)
	}
	if cursorOf(p) != 3 {
		t.Fatalf("cursor should clamp at Other index 3, got %d", cursorOf(p))
	}
}

// Multi-select with single question: Space toggles, Enter submits, then review confirms.
func TestQuestionPanel_MultiSelect_SubmitsCommaJoined(t *testing.T) {
	q := toolbuiltin.Question{
		Question: "Platforms?",
		Header:   "OS",
		Options: []toolbuiltin.QuestionOption{
			{Label: "Linux", Description: "tux"},
			{Label: "macOS", Description: "apple"},
			{Label: "Windows", Description: "win"},
		},
		MultiSelect: true,
	}
	p, out := NewQuestionPanel(NewStyles(DefaultTheme()), []toolbuiltin.Question{q})
	// Toggle Linux (cursor=0), move to macOS, toggle.
	p.HandleKey(keySpaceMsg)
	p.HandleKey(keyDownMsg)
	p.HandleKey(keySpaceMsg)
	// Submit current question.
	p.HandleKey(keyEnterMsg)
	if !p.isAtReview() {
		t.Fatalf("multi-select single-question should land on review after Enter, currentIdx=%d", p.currentIdx)
	}
	// Confirm submission on review screen.
	p.HandleKey(keyEnterMsg)
	got := <-out
	if got.Cancelled {
		t.Fatalf("should not be cancelled")
	}
	if got.Answers["Platforms?"] != "Linux,macOS" {
		t.Fatalf("expected 'Linux,macOS', got %q", got.Answers["Platforms?"])
	}
}

// Multi-select: Enter on a row with no Space presses implicitly selects that row.
func TestQuestionPanel_MultiSelect_EnterImplicitlySelectsCursor(t *testing.T) {
	q := toolbuiltin.Question{
		Question:    "Pick at least one?",
		Header:      "X",
		Options:     []toolbuiltin.QuestionOption{{Label: "A", Description: "a"}, {Label: "B", Description: "b"}},
		MultiSelect: true,
	}
	p, out := NewQuestionPanel(NewStyles(DefaultTheme()), []toolbuiltin.Question{q})
	p.HandleKey(keyDownMsg) // cursor on B
	p.HandleKey(keyEnterMsg)
	// Multi-select still triggers a review step (hasReviewStep=true).
	if !p.isAtReview() {
		t.Fatalf("expected review screen after implicit selection")
	}
	p.HandleKey(keyEnterMsg) // submit on review
	got := <-out
	if got.Answers["Pick at least one?"] != "B" {
		t.Fatalf("expected implicit cursor selection 'B', got %q", got.Answers["Pick at least one?"])
	}
}

// Other: navigate to "Other...", Enter, type, Enter — answer is the typed text.
// Single-question single-select: no review step, delivers immediately.
func TestQuestionPanel_OtherInput_RecordsTypedText(t *testing.T) {
	p, out := newPanelWithSingleQuestion()
	for i := 0; i < 3; i++ {
		p.HandleKey(keyDownMsg)
	}
	p.HandleKey(keyEnterMsg) // enter input mode
	if modeOf(p) != qmodeOtherInput {
		t.Fatalf("expected qmodeOtherInput, got %v", modeOf(p))
	}
	p.HandleKey(keyRune('P'))
	p.HandleKey(keyRune('i'))
	p.HandleKey(keyRune('n'))
	p.HandleKey(keyRune('k'))
	if otherTextOf(p) != "Pink" {
		t.Fatalf("expected otherText 'Pink', got %q", otherTextOf(p))
	}
	p.HandleKey(keyEnterMsg)
	got := <-out
	if got.Answers["Pick a color?"] != "Pink" {
		t.Fatalf("expected 'Pink', got %q", got.Answers["Pick a color?"])
	}
}

// Other: empty Enter does NOT submit; Backspace deletes runes.
func TestQuestionPanel_OtherInput_EmptyEnterAndBackspace(t *testing.T) {
	p, _ := newPanelWithSingleQuestion()
	for i := 0; i < 3; i++ {
		p.HandleKey(keyDownMsg)
	}
	p.HandleKey(keyEnterMsg)
	p.HandleKey(keyEnterMsg) // empty Enter — should not finish
	if p.IsFinished() {
		t.Fatalf("empty Other text should not finish the panel")
	}
	p.HandleKey(keyRune('A'))
	p.HandleKey(keyRune('B'))
	p.HandleKey(keyBackMsg)
	if otherTextOf(p) != "A" {
		t.Fatalf("expected 'A' after backspace, got %q", otherTextOf(p))
	}
}

// Other: Esc in input mode returns to selection (does NOT cancel) and PRESERVES the typed text
// — the user may want to re-enter and continue editing.
func TestQuestionPanel_OtherInput_EscReturnsToSelectAndPreservesText(t *testing.T) {
	p, _ := newPanelWithSingleQuestion()
	for i := 0; i < 3; i++ {
		p.HandleKey(keyDownMsg)
	}
	p.HandleKey(keyEnterMsg)
	p.HandleKey(keyRune('X'))
	p.HandleKey(keyEscMsg)
	if p.IsFinished() {
		t.Fatalf("Esc in input mode must not finish the panel")
	}
	if modeOf(p) != qmodeSelect {
		t.Fatalf("expected return to qmodeSelect, got %v", modeOf(p))
	}
	if otherTextOf(p) != "X" {
		t.Fatalf("expected typed text preserved, got %q", otherTextOf(p))
	}
}

// Esc in select mode cancels the whole panel.
func TestQuestionPanel_EscInSelectMode_Cancels(t *testing.T) {
	p, out := newPanelWithSingleQuestion()
	p.HandleKey(keyEscMsg)
	if !p.IsFinished() {
		t.Fatalf("Esc in select mode should finish the panel")
	}
	got := <-out
	if !got.Cancelled {
		t.Fatalf("expected Cancelled outcome")
	}
	if got.Answers != nil {
		t.Fatalf("cancelled outcome must not include answers, got %v", got.Answers)
	}
}

// Multi-question: smart-advance after each answer, ending at the review screen.
// User then confirms on the review screen to deliver answers.
func TestQuestionPanel_MultiQuestion_SmartAdvanceThenReview(t *testing.T) {
	p, out := newPanelWithTwoQuestions()
	// Q1: pick B
	p.HandleKey(keyDownMsg)
	p.HandleKey(keyEnterMsg)
	if p.IsFinished() {
		t.Fatalf("panel should not be finished after first question of two")
	}
	if p.currentIdx != 1 {
		t.Fatalf("expected currentIdx=1 after Q1, got %d", p.currentIdx)
	}
	if cursorOf(p) != 0 {
		t.Fatalf("expected cursor=0 for fresh Q2, got %d", cursorOf(p))
	}
	// Q2: pick Z
	p.HandleKey(keyDownMsg)
	p.HandleKey(keyDownMsg)
	p.HandleKey(keyEnterMsg)
	if !p.isAtReview() {
		t.Fatalf("expected review screen after Q2, currentIdx=%d", p.currentIdx)
	}
	// Submit on review.
	p.HandleKey(keyEnterMsg)
	got := <-out
	if got.Answers["Q1?"] != "B" {
		t.Fatalf("expected Q1=B, got %q", got.Answers["Q1?"])
	}
	if got.Answers["Q2?"] != "Z" {
		t.Fatalf("expected Q2=Z, got %q", got.Answers["Q2?"])
	}
}

// Cancel(): outcome is delivered and second call is a no-op.
func TestQuestionPanel_Cancel_Idempotent(t *testing.T) {
	p, out := newPanelWithSingleQuestion()
	p.Cancel()
	p.Cancel() // must not panic / double-send
	got := <-out
	if !got.Cancelled {
		t.Fatalf("expected Cancelled outcome")
	}
	select {
	case extra := <-out:
		t.Fatalf("unexpected extra outcome: %+v", extra)
	default:
	}
}

// View renders something non-empty, contains the question text and Other item.
func TestQuestionPanel_View_RendersQuestionAndOther(t *testing.T) {
	p, _ := newPanelWithSingleQuestion()
	p.SetSize(80, 24)
	v := p.View()
	if v == "" {
		t.Fatalf("expected non-empty view")
	}
	if !strings.Contains(v, "Pick a color?") {
		t.Fatalf("view missing question text: %q", v)
	}
	if !strings.Contains(v, otherMenuLabel) {
		t.Fatalf("view missing Other... item: %q", v)
	}
	for i := 0; i < 3; i++ {
		p.HandleKey(keyDownMsg)
	}
	p.HandleKey(keyEnterMsg)
	v = p.View()
	if !strings.Contains(v, "Other > ") {
		t.Fatalf("expected Other input prompt, got %q", v)
	}
}

// ---- new tests for v2 redesign ----------------------------------------------

// Tab navigation: jump forward and back without losing answers or cursor state.
func TestQuestionPanel_TabNav_PreservesAnswers(t *testing.T) {
	p, _ := newPanelWithTwoQuestions()
	// Q1: pick A (cursor stays at 0), submit → smart-advance lands on Q2.
	p.HandleKey(keyEnterMsg)
	if p.currentIdx != 1 {
		t.Fatalf("expected to be on Q2 after answering Q1, got %d", p.currentIdx)
	}
	if got := p.answers["Q1?"]; got != "A" {
		t.Fatalf("expected Q1=A, got %q", got)
	}
	// Move cursor on Q2 to position 2 (Z), then Shift+Tab back to Q1 without answering.
	p.HandleKey(keyDownMsg)
	p.HandleKey(keyDownMsg)
	p.HandleKey(keyShiftTabMsg)
	if p.currentIdx != 0 {
		t.Fatalf("Shift+Tab should return to Q1, got currentIdx=%d", p.currentIdx)
	}
	// Q1 cursor was at 0; should still be 0 (preserved per-question).
	if cursorOf(p) != 0 {
		t.Fatalf("Q1 cursor should remain 0 after revisit, got %d", cursorOf(p))
	}
	// Q1 answer still recorded.
	if p.answers["Q1?"] != "A" {
		t.Fatalf("Q1 answer should survive nav, got %q", p.answers["Q1?"])
	}
	// Tab forward to Q2 — its cursor should still be 2 (preserved).
	p.HandleKey(keyTabMsg)
	if p.currentIdx != 1 {
		t.Fatalf("Tab should advance to Q2, got %d", p.currentIdx)
	}
	if cursorOf(p) != 2 {
		t.Fatalf("Q2 cursor should be preserved at 2, got %d", cursorOf(p))
	}
}

// Smart advance skips already-answered questions and goes to the review screen
// when no unanswered questions remain.
func TestQuestionPanel_SmartAdvance_SkipsAnswered(t *testing.T) {
	q1 := toolbuiltin.Question{
		Question:    "Q1?",
		Header:      "1",
		Options:     []toolbuiltin.QuestionOption{{Label: "A"}, {Label: "B"}},
		MultiSelect: false,
	}
	q2 := toolbuiltin.Question{
		Question:    "Q2?",
		Header:      "2",
		Options:     []toolbuiltin.QuestionOption{{Label: "X"}, {Label: "Y"}},
		MultiSelect: false,
	}
	q3 := toolbuiltin.Question{
		Question:    "Q3?",
		Header:      "3",
		Options:     []toolbuiltin.QuestionOption{{Label: "M"}, {Label: "N"}},
		MultiSelect: false,
	}
	p, _ := NewQuestionPanel(NewStyles(DefaultTheme()), []toolbuiltin.Question{q1, q2, q3})
	// Pre-fill Q1 and Q3 to simulate the user having tab-navigated and answered them.
	p.answers["Q1?"] = "A"
	p.answers["Q3?"] = "N"
	// Tab to Q2 and answer it → smart advance should jump to review (Q3 already answered).
	p.HandleKey(keyTabMsg) // Q1 → Q2
	if p.currentIdx != 1 {
		t.Fatalf("expected Q2, got %d", p.currentIdx)
	}
	p.HandleKey(keyEnterMsg) // pick X
	if !p.isAtReview() {
		t.Fatalf("expected review screen since all answered, currentIdx=%d", p.currentIdx)
	}
}

// Review screen: cursor on Submit, Enter delivers all answers.
func TestQuestionPanel_Review_SubmitDelivers(t *testing.T) {
	p, out := newPanelWithTwoQuestions()
	p.HandleKey(keyEnterMsg) // Q1 → A
	p.HandleKey(keyEnterMsg) // Q2 → X (lands at review)
	if !p.isAtReview() {
		t.Fatalf("setup precondition failed: expected review")
	}
	if p.reviewCursor != 0 {
		t.Fatalf("review cursor should default to Submit (0), got %d", p.reviewCursor)
	}
	p.HandleKey(keyEnterMsg)
	got := <-out
	if got.Cancelled {
		t.Fatalf("Submit must not produce Cancelled outcome")
	}
	if got.Answers["Q1?"] != "A" || got.Answers["Q2?"] != "X" {
		t.Fatalf("expected Q1=A,Q2=X, got %v", got.Answers)
	}
}

// Review screen: down to Cancel, Enter cancels.
func TestQuestionPanel_Review_CancelDelivers(t *testing.T) {
	p, out := newPanelWithTwoQuestions()
	p.HandleKey(keyEnterMsg)
	p.HandleKey(keyEnterMsg)
	if !p.isAtReview() {
		t.Fatalf("setup precondition failed")
	}
	p.HandleKey(keyDownMsg) // cursor → Cancel
	if p.reviewCursor != 1 {
		t.Fatalf("expected reviewCursor=1, got %d", p.reviewCursor)
	}
	p.HandleKey(keyEnterMsg)
	got := <-out
	if !got.Cancelled {
		t.Fatalf("expected Cancelled outcome")
	}
}

// Review screen: Shift+Tab returns to the last question.
func TestQuestionPanel_Review_ShiftTabReturnsToLastQuestion(t *testing.T) {
	p, _ := newPanelWithTwoQuestions()
	p.HandleKey(keyEnterMsg)
	p.HandleKey(keyEnterMsg)
	if !p.isAtReview() {
		t.Fatalf("setup precondition failed")
	}
	p.HandleKey(keyShiftTabMsg)
	if p.currentIdx != 1 {
		t.Fatalf("Shift+Tab from review should go to last question (1), got %d", p.currentIdx)
	}
}

// Single question single-select: no review step (immediate delivery on Enter).
func TestQuestionPanel_SingleQuestionSingleSelect_NoReview(t *testing.T) {
	p, _ := newPanelWithSingleQuestion()
	if p.hasReviewStep {
		t.Fatalf("single-question single-select must not have a review step")
	}
	if got := p.lastTabIdx(); got != 0 {
		t.Fatalf("lastTabIdx should be 0 (only the question), got %d", got)
	}
}

// Single question multi-select: review step IS shown (extra confirmation).
func TestQuestionPanel_SingleQuestionMultiSelect_HasReview(t *testing.T) {
	q := toolbuiltin.Question{
		Question:    "Pick many?",
		Header:      "M",
		Options:     []toolbuiltin.QuestionOption{{Label: "A"}, {Label: "B"}},
		MultiSelect: true,
	}
	p, _ := NewQuestionPanel(NewStyles(DefaultTheme()), []toolbuiltin.Question{q})
	if !p.hasReviewStep {
		t.Fatalf("single-question multi-select should have a review step")
	}
}

// Other text is preserved across tab navigation, even before submission.
func TestQuestionPanel_OtherText_PreservedAcrossNav(t *testing.T) {
	p, _ := newPanelWithTwoQuestions()
	// On Q1: cursor → Other (index 2 = after A/B), Enter to enter input mode, type "abc".
	p.HandleKey(keyDownMsg)
	p.HandleKey(keyDownMsg)
	if cursorOf(p) != 2 {
		t.Fatalf("cursor should be on Other (2), got %d", cursorOf(p))
	}
	p.HandleKey(keyEnterMsg)
	if modeOf(p) != qmodeOtherInput {
		t.Fatalf("expected Other input mode, got %v", modeOf(p))
	}
	p.HandleKey(keyRune('a'))
	p.HandleKey(keyRune('b'))
	p.HandleKey(keyRune('c'))
	// Tab away to Q2 — text and mode preserved on Q1.
	p.HandleKey(keyTabMsg)
	if p.currentIdx != 1 {
		t.Fatalf("Tab should land on Q2, got %d", p.currentIdx)
	}
	// Tab back to Q1.
	p.HandleKey(keyShiftTabMsg)
	if p.currentIdx != 0 {
		t.Fatalf("Shift+Tab should return to Q1, got %d", p.currentIdx)
	}
	if otherTextOf(p) != "abc" {
		t.Fatalf("Q1 Other text should be preserved as 'abc', got %q", otherTextOf(p))
	}
	if modeOf(p) != qmodeOtherInput {
		t.Fatalf("Q1 mode should remain qmodeOtherInput, got %v", modeOf(p))
	}
}

// Nav bar renders checkboxes that update as questions get answered, plus a Submit tab.
func TestQuestionPanel_NavBar_RendersCheckboxesAndSubmitTab(t *testing.T) {
	p, _ := newPanelWithTwoQuestions()
	p.SetSize(80, 24)
	v := p.View()
	if !strings.Contains(v, "☐") {
		t.Fatalf("nav bar should show ☐ for unanswered questions, view=%q", v)
	}
	if !strings.Contains(v, "Submit") {
		t.Fatalf("nav bar should include 'Submit' tab, view=%q", v)
	}
	// Answer Q1.
	p.HandleKey(keyEnterMsg)
	v = p.View()
	if !strings.Contains(v, "✓") {
		t.Fatalf("nav bar should show ✓ once a question is answered, view=%q", v)
	}
}

// Review screen view shows Q&A pairs and Submit/Cancel choices.
func TestQuestionPanel_Review_ViewListsAnswers(t *testing.T) {
	p, _ := newPanelWithTwoQuestions()
	p.SetSize(80, 24)
	p.HandleKey(keyEnterMsg) // Q1 → A
	p.HandleKey(keyEnterMsg) // Q2 → X
	if !p.isAtReview() {
		t.Fatalf("setup precondition failed")
	}
	v := p.View()
	if !strings.Contains(v, "Review your answers") {
		t.Fatalf("review screen should have title, view=%q", v)
	}
	if !strings.Contains(v, "Q1?") || !strings.Contains(v, "Q2?") {
		t.Fatalf("review should list both questions, view=%q", v)
	}
	if !strings.Contains(v, "Submit answers") || !strings.Contains(v, "Cancel") {
		t.Fatalf("review should offer Submit/Cancel, view=%q", v)
	}
}
