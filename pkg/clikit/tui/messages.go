package tui

import (
	"github.com/saker-ai/saker/pkg/api"
	toolbuiltin "github.com/saker-ai/saker/pkg/tool/builtin"
)

// Internal message types for the bubbletea Update loop.

// StreamEventMsg wraps an api.StreamEvent received from the model.
type StreamEventMsg struct {
	Event api.StreamEvent
}

// StreamDoneMsg signals that the current stream has finished.
type StreamDoneMsg struct{}

// BtwDoneMsg signals that a /btw side question has finished.
type BtwDoneMsg struct{}

// BtwErrorMsg carries an error from a /btw side question.
type BtwErrorMsg struct {
	Err error
}

// IMDoneMsg signals that an /im side question has finished.
type IMDoneMsg struct{}

// IMErrorMsg carries an error from an /im side question.
type IMErrorMsg struct {
	Err error
}

// StreamErrorMsg carries an error from the streaming goroutine.
type StreamErrorMsg struct {
	Err error
}

// CommandResultMsg carries the output of a slash command.
type CommandResultMsg struct {
	Text string
	Quit bool
}

// StatusMsg updates the status bar text.
type StatusMsg struct {
	Text string
}

// OpenQuestionPanelMsg requests opening the AskUserQuestion interactive panel.
// Sent via program.Send() from the tool execution goroutine. The Reply channel
// is closed by the panel when the user finishes (either submitting answers or
// cancelling); the bridge function blocks on it.
type OpenQuestionPanelMsg struct {
	Questions []toolbuiltin.Question
	Reply     chan<- QuestionPanelOutcome
}

// CloseQuestionPanelMsg cancels the currently open question panel (used when
// the caller's ctx is cancelled while waiting for the user).
type CloseQuestionPanelMsg struct{}

// QuestionPanelDoneMsg is sent internally when the panel finishes; the App
// uses it to clean up state and forward the outcome to the original askFn caller.
type QuestionPanelDoneMsg struct {
	Outcome QuestionPanelOutcome
}
