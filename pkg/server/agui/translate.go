package agui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/saker-ai/saker/pkg/api"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
)

// streamState tracks per-stream lifecycle for translating saker StreamEvents
// into AG-UI typed events. One instance per SSE stream.
type streamState struct {
	threadID string
	runID    string
	msgID    string
	// textStarted is true after the first TEXT_MESSAGE_START has been emitted.
	textStarted bool
	// toolCalls tracks which tool call IDs have been started.
	toolCalls map[string]bool
	// lastToolID is the ID of the currently open tool call.
	lastToolID string
	// lastStep is the name of the currently open STEP.
	lastStep string
	// iterCount tracks how many iterations have started.
	iterCount int
}

func newStreamState(threadID, runID string) *streamState {
	return &streamState{
		threadID:  threadID,
		runID:     runID,
		msgID:     fmt.Sprintf("msg_%s", runID),
		toolCalls: make(map[string]bool),
	}
}

// translateEvent converts a single saker StreamEvent into zero or more AG-UI
// events written directly to the SSE writer. The text filter strips XML
// function-call artifacts from text deltas.
func (s *streamState) translateEvent(w io.Writer, sseW sseWriter, evt api.StreamEvent, filter textFilter) {
	switch evt.Type {
	case api.EventContentBlockDelta:
		if evt.Delta == nil || evt.Delta.Text == "" {
			return
		}
		safe := filter.Push(evt.Delta.Text)
		if safe == "" {
			return
		}
		if !s.textStarted {
			s.textStarted = true
			writeSSE(w, sseW, aguievents.NewTextMessageStartEvent(s.msgID, aguievents.WithRole("assistant")))
		}
		writeSSE(w, sseW, aguievents.NewTextMessageContentEvent(s.msgID, safe))

	case api.EventToolExecutionStart:
		if s.lastToolID != "" {
			writeSSE(w, sseW, aguievents.NewToolCallEndEvent(s.lastToolID))
		}
		toolID := evt.ToolUseID
		if toolID == "" {
			toolID = fmt.Sprintf("tc_%s_%s", s.runID, evt.Name)
		}
		s.toolCalls[toolID] = true
		s.lastToolID = toolID
		writeSSE(w, sseW, aguievents.NewToolCallStartEvent(toolID, evt.Name, aguievents.WithParentMessageID(s.msgID)))
		args := inputToJSON(evt.Input)
		if args != "" && args != "{}" {
			writeSSE(w, sseW, aguievents.NewToolCallArgsEvent(toolID, args))
		}

	case api.EventToolExecutionResult:
		toolID := evt.ToolUseID
		if toolID == "" {
			toolID = s.lastToolID
		}
		if toolID != "" {
			writeSSE(w, sseW, aguievents.NewToolCallEndEvent(toolID))
			if s.lastToolID == toolID {
				s.lastToolID = ""
			}
			delete(s.toolCalls, toolID)
		}

	case api.EventIterationStart:
		s.iterCount++
		stepName := fmt.Sprintf("iteration_%d", s.iterCount)
		if s.lastStep != "" {
			writeSSE(w, sseW, aguievents.NewStepFinishedEvent(s.lastStep))
		}
		s.lastStep = stepName
		writeSSE(w, sseW, aguievents.NewStepStartedEvent(stepName))

	case api.EventIterationStop:
		if s.lastStep != "" {
			writeSSE(w, sseW, aguievents.NewStepFinishedEvent(s.lastStep))
			s.lastStep = ""
		}

	case api.EventError:
		msg := "runtime error"
		if evt.Output != nil {
			if s, ok := evt.Output.(string); ok && s != "" {
				msg = s
			}
		}
		writeSSE(w, sseW, aguievents.NewRunErrorEvent(msg, aguievents.WithRunID(s.runID)))
	}
}

// finalize emits closing events for any open tool calls, text message,
// steps, and the RUN_FINISHED event.
func (s *streamState) finalize(w io.Writer, sseW sseWriter, filter textFilter) {
	if s.lastToolID != "" {
		writeSSE(w, sseW, aguievents.NewToolCallEndEvent(s.lastToolID))
		s.lastToolID = ""
	}
	if tail := filter.Flush(); tail != "" && s.textStarted {
		writeSSE(w, sseW, aguievents.NewTextMessageContentEvent(s.msgID, tail))
	}
	if s.textStarted {
		writeSSE(w, sseW, aguievents.NewTextMessageEndEvent(s.msgID))
	}
	if s.lastStep != "" {
		writeSSE(w, sseW, aguievents.NewStepFinishedEvent(s.lastStep))
		s.lastStep = ""
	}
	writeSSE(w, sseW, aguievents.NewRunFinishedEvent(s.threadID, s.runID))
}

// textFilter abstracts the stream artifact filter used to strip XML
// function-call artifacts from streaming text deltas.
type textFilter interface {
	Push(chunk string) string
	Flush() string
}

// sseWriter abstracts the AG-UI SDK SSE writer for testability.
type sseWriter interface {
	WriteEventWithType(ctx context.Context, w io.Writer, event aguievents.Event, eventType string) error
}

// writeSSE is a convenience wrapper that writes an AG-UI event as a typed
// SSE frame. Errors are swallowed — a failed write means the client
// disconnected and the main loop will catch it via ctx.Done().
func writeSSE(w io.Writer, sseW sseWriter, event aguievents.Event) {
	_ = sseW.WriteEventWithType(context.Background(), w, event, string(event.Type()))
}

// inputToJSON serializes a StreamEvent.Input (typed as interface{}) to a
// compact JSON string suitable for TOOL_CALL_ARGS delta.
func inputToJSON(input interface{}) string {
	if input == nil {
		return "{}"
	}
	if s, ok := input.(string); ok {
		return s
	}
	b, err := json.Marshal(input)
	if err != nil {
		return "{}"
	}
	return string(b)
}
