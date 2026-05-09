package clikit

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/cinience/saker/pkg/api"
	"github.com/google/uuid"
)

// SideQuestionPromptWrapper is the system prompt used to wrap /btw side questions.
// Matches Claude Code's implementation: the side question runs in a forked agent
// that shares the parent context but has no tools and is limited to one turn.
const SideQuestionPromptWrapper = `<system-reminder>This is a side question from the user via /btw command. Answer directly in a single response.

CONTEXT:
- You are a separate, lightweight agent forked from the main conversation
- You inherit the full conversation history for context
- The main agent is NOT interrupted — it continues working independently
- This is a one-off response — there will be no follow-up turns

CONSTRAINTS:
- You have NO tools available — you cannot read files, run commands, search, or take any actions
- Answer based on the conversation context and your knowledge only
- NEVER say "Let me try...", "I'll now...", or promise to take any action
- If you don't know, say so

Answer the question directly.</system-reminder>

`

// RunSideQuestion sends a quick question to the model without affecting the
// main conversation history. It uses a temporary session ID and streams the
// response inline with a visual header.
func RunSideQuestion(ctx context.Context, out, errOut io.Writer, eng StreamEngine, question string) error {
	if out == nil {
		out = io.Discard
	}
	if errOut == nil {
		errOut = io.Discard
	}

	tempSessionID := "btw-" + uuid.NewString()
	wrappedPrompt := SideQuestionPromptWrapper + question

	ch, err := eng.RunStream(ctx, tempSessionID, wrappedPrompt)
	if err != nil {
		return fmt.Errorf("btw: %w", err)
	}

	fmt.Fprintf(out, "\n/btw %s\n", question)
	fmt.Fprintln(out, strings.Repeat("─", 40))

	for evt := range ch {
		switch evt.Type {
		case api.EventContentBlockDelta:
			if evt.Delta != nil && evt.Delta.Type == "text_delta" {
				fmt.Fprint(out, evt.Delta.Text)
			}
		case api.EventError:
			if evt.Output != nil {
				fmt.Fprintf(errOut, "btw error: %v\n", evt.Output)
			}
		}
	}

	fmt.Fprintln(out)
	fmt.Fprintln(out, strings.Repeat("─", 40))
	return nil
}
