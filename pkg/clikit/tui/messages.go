package tui

import "github.com/cinience/saker/pkg/api"

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
