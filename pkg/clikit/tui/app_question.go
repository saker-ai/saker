// app_question.go: bridge between the AskUserQuestion tool execution goroutine
// and the bubbletea event loop. The tool calls askQuestionFromTUI, which sends
// a tea.Msg to open the panel, then blocks on a channel until the user submits
// or cancels.
package tui

import (
	"context"
	"errors"

	toolbuiltin "github.com/cinience/saker/pkg/tool/builtin"
)

// askQuestionFromTUI is registered with the RuntimeAdapter via
// AskQuestionRegistrar. It is invoked from the agent's tool execution
// goroutine — never from the bubbletea event loop — so it must use
// program.Send() to interact with the App and block on a channel until done.
func (a *App) askQuestionFromTUI(ctx context.Context, questions []toolbuiltin.Question) (map[string]string, error) {
	if a == nil || a.program == nil {
		return nil, errors.New("tui: question panel is not available (no running program)")
	}
	if len(questions) == 0 {
		return map[string]string{}, nil
	}

	// Buffered (size 1) so the App can deliver the outcome without blocking
	// even if we lose the race with ctx.Done.
	reply := make(chan QuestionPanelOutcome, 1)
	a.program.Send(OpenQuestionPanelMsg{
		Questions: questions,
		Reply:     reply,
	})

	select {
	case outcome := <-reply:
		if outcome.Cancelled {
			return nil, errors.New("user cancelled the question panel")
		}
		if outcome.Answers == nil {
			return map[string]string{}, nil
		}
		return outcome.Answers, nil
	case <-ctx.Done():
		// Tell the App to close the panel so the UI doesn't get stuck.
		a.program.Send(CloseQuestionPanelMsg{})
		return nil, ctx.Err()
	}
}
