package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	toolbuiltin "github.com/cinience/saker/pkg/tool/builtin"
)

func keyRune(r rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: r} }
func keySpecial(c rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: c}
}

var (
	keyEnterMsg = tea.KeyPressMsg{Code: tea.KeyEnter}
	keyEscMsg   = tea.KeyPressMsg{Code: tea.KeyEsc}
	keyDownMsg  = tea.KeyPressMsg{Code: tea.KeyDown}
	keyUpMsg    = tea.KeyPressMsg{Code: tea.KeyUp}
	keySpaceMsg = tea.KeyPressMsg{Code: tea.KeySpace}
	keyBackMsg  = tea.KeyPressMsg{Code: tea.KeyBackspace}
)

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

// Single-select: navigating then pressing Enter records the chosen label.
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
	// Up at top is no-op.
	p.HandleKey(keyUpMsg)
	if p.cursor != 0 {
		t.Fatalf("cursor should stay at 0 when going up at top, got %d", p.cursor)
	}
	// Move past last option to "Other..." (index = len(options) = 3).
	for i := 0; i < 10; i++ {
		p.HandleKey(keyDownMsg)
	}
	if p.cursor != 3 {
		t.Fatalf("cursor should clamp at Other index 3, got %d", p.cursor)
	}
}

// Multi-select: Space toggles, Enter submits comma-joined labels.
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
	// Submit.
	p.HandleKey(keyEnterMsg)
	got := <-out
	if got.Cancelled {
		t.Fatalf("should not be cancelled")
	}
	ans := got.Answers["Platforms?"]
	if ans != "Linux,macOS" {
		t.Fatalf("expected 'Linux,macOS', got %q", ans)
	}
}

// Multi-select: Enter with no Space presses still implicitly selects the
// cursor row (so a quick "just pick this one" Enter works in multi mode).
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
	got := <-out
	if got.Answers["Pick at least one?"] != "B" {
		t.Fatalf("expected implicit cursor selection 'B', got %q", got.Answers["Pick at least one?"])
	}
}

// Other: navigate to "Other...", Enter, type, Enter — answer is the typed text.
func TestQuestionPanel_OtherInput_RecordsTypedText(t *testing.T) {
	p, out := newPanelWithSingleQuestion()
	// Move to Other (index 3 = after Red/Green/Blue).
	for i := 0; i < 3; i++ {
		p.HandleKey(keyDownMsg)
	}
	p.HandleKey(keyEnterMsg) // enter input mode
	if p.mode != qmodeOtherInput {
		t.Fatalf("expected qmodeOtherInput, got %v", p.mode)
	}
	// Type "Pink".
	p.HandleKey(keyRune('P'))
	p.HandleKey(keyRune('i'))
	p.HandleKey(keyRune('n'))
	p.HandleKey(keyRune('k'))
	if p.otherText != "Pink" {
		t.Fatalf("expected otherText 'Pink', got %q", p.otherText)
	}
	// Submit.
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
	// Empty Enter — should not finish.
	p.HandleKey(keyEnterMsg)
	if p.IsFinished() {
		t.Fatalf("empty Other text should not finish the panel")
	}
	// Type then backspace.
	p.HandleKey(keyRune('A'))
	p.HandleKey(keyRune('B'))
	p.HandleKey(keyBackMsg)
	if p.otherText != "A" {
		t.Fatalf("expected 'A' after backspace, got %q", p.otherText)
	}
}

// Other: Esc in input mode returns to selection (does NOT cancel the panel).
func TestQuestionPanel_OtherInput_EscReturnsToSelect(t *testing.T) {
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
	if p.mode != qmodeSelect {
		t.Fatalf("expected return to qmodeSelect, got %v", p.mode)
	}
	if p.otherText != "" {
		t.Fatalf("expected otherText cleared, got %q", p.otherText)
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

// Multi-question: questions are presented sequentially; cursor resets each time.
func TestQuestionPanel_MultiQuestion_Sequential(t *testing.T) {
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
	p, out := NewQuestionPanel(NewStyles(DefaultTheme()), []toolbuiltin.Question{q1, q2})
	// Q1: pick B
	p.HandleKey(keyDownMsg)
	p.HandleKey(keyEnterMsg)
	if p.IsFinished() {
		t.Fatalf("panel should not be finished after first question of two")
	}
	if p.currentIdx != 1 {
		t.Fatalf("expected currentIdx=1 after answering Q1, got %d", p.currentIdx)
	}
	if p.cursor != 0 {
		t.Fatalf("expected cursor reset to 0 for Q2, got %d", p.cursor)
	}
	// Q2: pick Z
	p.HandleKey(keyDownMsg)
	p.HandleKey(keyDownMsg)
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
	// Channel should not have a second value.
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
	// Switch to input mode and ensure prompt renders.
	for i := 0; i < 3; i++ {
		p.HandleKey(keyDownMsg)
	}
	p.HandleKey(keyEnterMsg)
	v = p.View()
	if !strings.Contains(v, "Other > ") {
		t.Fatalf("expected Other input prompt, got %q", v)
	}
}
