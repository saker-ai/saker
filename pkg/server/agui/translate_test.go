package agui

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/saker-ai/saker/pkg/api"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
)

// nopFilter passes text through unchanged.
type nopFilter struct{}

func (nopFilter) Push(s string) string { return s }
func (nopFilter) Flush() string        { return "" }

// tailFilter returns nothing on Push but accumulates; Flush returns all.
type tailFilter struct{ buf string }

func (f *tailFilter) Push(s string) string { f.buf += s; return "" }
func (f *tailFilter) Flush() string        { return f.buf }

// capturedEvent records a single AG-UI SSE frame.
type capturedEvent struct {
	eventType string
}

// capturingSSEWriter records event types written via writeSSE.
type capturingSSEWriter struct {
	events []capturedEvent
}

func (c *capturingSSEWriter) WriteEventWithType(_ context.Context, _ io.Writer, _ aguievents.Event, eventType string) error {
	c.events = append(c.events, capturedEvent{eventType: eventType})
	return nil
}

func (c *capturingSSEWriter) types() []string {
	out := make([]string, len(c.events))
	for i, e := range c.events {
		out[i] = e.eventType
	}
	return out
}

func TestTranslateEvent_TextDelta_FirstEmitsStart(t *testing.T) {
	t.Parallel()
	state := newStreamState("t1", "r1")
	w := &capturingSSEWriter{}
	evt := api.StreamEvent{Type: api.EventContentBlockDelta, Delta: &api.Delta{Text: "hello"}}
	state.translateEvent(&bytes.Buffer{}, w, evt, nopFilter{})

	types := w.types()
	if len(types) != 2 {
		t.Fatalf("expected 2 events, got %d: %v", len(types), types)
	}
	if types[0] != "TEXT_MESSAGE_START" {
		t.Errorf("first event = %q, want TEXT_MESSAGE_START", types[0])
	}
	if types[1] != "TEXT_MESSAGE_CONTENT" {
		t.Errorf("second event = %q, want TEXT_MESSAGE_CONTENT", types[1])
	}
}

func TestTranslateEvent_TextDelta_SubsequentSkipsStart(t *testing.T) {
	t.Parallel()
	state := newStreamState("t1", "r1")
	w := &capturingSSEWriter{}
	evt := api.StreamEvent{Type: api.EventContentBlockDelta, Delta: &api.Delta{Text: "a"}}
	state.translateEvent(&bytes.Buffer{}, w, evt, nopFilter{})
	w.events = nil

	state.translateEvent(&bytes.Buffer{}, w, evt, nopFilter{})
	types := w.types()
	if len(types) != 1 {
		t.Fatalf("expected 1 event, got %d: %v", len(types), types)
	}
	if types[0] != "TEXT_MESSAGE_CONTENT" {
		t.Errorf("event = %q, want TEXT_MESSAGE_CONTENT", types[0])
	}
}

func TestTranslateEvent_TextDelta_EmptySkipped(t *testing.T) {
	t.Parallel()
	state := newStreamState("t1", "r1")
	w := &capturingSSEWriter{}
	state.translateEvent(&bytes.Buffer{}, w, api.StreamEvent{Type: api.EventContentBlockDelta, Delta: &api.Delta{Text: ""}}, nopFilter{})
	if len(w.events) != 0 {
		t.Errorf("empty text should emit nothing, got %d events", len(w.events))
	}

	state.translateEvent(&bytes.Buffer{}, w, api.StreamEvent{Type: api.EventContentBlockDelta, Delta: nil}, nopFilter{})
	if len(w.events) != 0 {
		t.Errorf("nil delta should emit nothing, got %d events", len(w.events))
	}
}

func TestTranslateEvent_ToolExecutionStart(t *testing.T) {
	t.Parallel()
	state := newStreamState("t1", "r1")
	w := &capturingSSEWriter{}
	evt := api.StreamEvent{
		Type:      api.EventToolExecutionStart,
		ToolUseID: "tc_1",
		Name:      "bash",
		Input:     map[string]string{"cmd": "ls"},
	}
	state.translateEvent(&bytes.Buffer{}, w, evt, nopFilter{})

	types := w.types()
	if len(types) != 2 {
		t.Fatalf("expected 2 events (start+args), got %d: %v", len(types), types)
	}
	if types[0] != "TOOL_CALL_START" {
		t.Errorf("first = %q, want TOOL_CALL_START", types[0])
	}
	if types[1] != "TOOL_CALL_ARGS" {
		t.Errorf("second = %q, want TOOL_CALL_ARGS", types[1])
	}
	if !state.toolCalls["tc_1"] {
		t.Error("tool call should be tracked")
	}
	if state.lastToolID != "tc_1" {
		t.Errorf("lastToolID = %q, want tc_1", state.lastToolID)
	}
}

func TestTranslateEvent_ToolExecutionStart_NoInput(t *testing.T) {
	t.Parallel()
	state := newStreamState("t1", "r1")
	w := &capturingSSEWriter{}
	evt := api.StreamEvent{
		Type:      api.EventToolExecutionStart,
		ToolUseID: "tc_2",
		Name:      "read",
	}
	state.translateEvent(&bytes.Buffer{}, w, evt, nopFilter{})

	types := w.types()
	if len(types) != 1 {
		t.Fatalf("expected 1 event (start only, no args), got %d: %v", len(types), types)
	}
	if types[0] != "TOOL_CALL_START" {
		t.Errorf("event = %q, want TOOL_CALL_START", types[0])
	}
}

func TestTranslateEvent_ToolExecutionStart_ClosesOpenTool(t *testing.T) {
	t.Parallel()
	state := newStreamState("t1", "r1")
	w := &capturingSSEWriter{}
	state.translateEvent(&bytes.Buffer{}, w, api.StreamEvent{
		Type: api.EventToolExecutionStart, ToolUseID: "tc_1", Name: "bash",
	}, nopFilter{})
	w.events = nil

	state.translateEvent(&bytes.Buffer{}, w, api.StreamEvent{
		Type: api.EventToolExecutionStart, ToolUseID: "tc_2", Name: "grep",
	}, nopFilter{})

	types := w.types()
	if len(types) < 2 {
		t.Fatalf("expected >=2 events, got %d: %v", len(types), types)
	}
	if types[0] != "TOOL_CALL_END" {
		t.Errorf("should close previous tool first, got %q", types[0])
	}
	if types[1] != "TOOL_CALL_START" {
		t.Errorf("then start new tool, got %q", types[1])
	}
}

func TestTranslateEvent_ToolExecutionResult(t *testing.T) {
	t.Parallel()
	state := newStreamState("t1", "r1")
	w := &capturingSSEWriter{}
	state.lastToolID = "tc_1"
	state.toolCalls["tc_1"] = true

	state.translateEvent(&bytes.Buffer{}, w, api.StreamEvent{
		Type: api.EventToolExecutionResult, ToolUseID: "tc_1",
	}, nopFilter{})

	types := w.types()
	if len(types) != 1 {
		t.Fatalf("expected 1 event, got %d: %v", len(types), types)
	}
	if types[0] != "TOOL_CALL_END" {
		t.Errorf("event = %q, want TOOL_CALL_END", types[0])
	}
	if state.lastToolID != "" {
		t.Errorf("lastToolID should be cleared, got %q", state.lastToolID)
	}
	if state.toolCalls["tc_1"] {
		t.Error("tool call should be removed from tracking")
	}
}

func TestTranslateEvent_IterationStartStop(t *testing.T) {
	t.Parallel()
	state := newStreamState("t1", "r1")
	w := &capturingSSEWriter{}

	state.translateEvent(&bytes.Buffer{}, w, api.StreamEvent{Type: api.EventIterationStart}, nopFilter{})
	types := w.types()
	if len(types) != 1 || types[0] != "STEP_STARTED" {
		t.Fatalf("iteration_start: got %v, want [STEP_STARTED]", types)
	}
	if state.iterCount != 1 {
		t.Errorf("iterCount = %d, want 1", state.iterCount)
	}
	w.events = nil

	state.translateEvent(&bytes.Buffer{}, w, api.StreamEvent{Type: api.EventIterationStop}, nopFilter{})
	types = w.types()
	if len(types) != 1 || types[0] != "STEP_FINISHED" {
		t.Fatalf("iteration_stop: got %v, want [STEP_FINISHED]", types)
	}
	if state.lastStep != "" {
		t.Errorf("lastStep should be cleared, got %q", state.lastStep)
	}
}

func TestTranslateEvent_IterationStart_ClosesOpenStep(t *testing.T) {
	t.Parallel()
	state := newStreamState("t1", "r1")
	w := &capturingSSEWriter{}

	state.translateEvent(&bytes.Buffer{}, w, api.StreamEvent{Type: api.EventIterationStart}, nopFilter{})
	w.events = nil

	state.translateEvent(&bytes.Buffer{}, w, api.StreamEvent{Type: api.EventIterationStart}, nopFilter{})
	types := w.types()
	if len(types) != 2 {
		t.Fatalf("expected 2 events, got %d: %v", len(types), types)
	}
	if types[0] != "STEP_FINISHED" {
		t.Errorf("should close previous step first, got %q", types[0])
	}
	if types[1] != "STEP_STARTED" {
		t.Errorf("then start new step, got %q", types[1])
	}
	if state.iterCount != 2 {
		t.Errorf("iterCount = %d, want 2", state.iterCount)
	}
}

func TestTranslateEvent_Error(t *testing.T) {
	t.Parallel()
	state := newStreamState("t1", "r1")
	w := &capturingSSEWriter{}

	state.translateEvent(&bytes.Buffer{}, w, api.StreamEvent{
		Type: api.EventError, Output: "something broke",
	}, nopFilter{})

	types := w.types()
	if len(types) != 1 || types[0] != "RUN_ERROR" {
		t.Fatalf("error event: got %v, want [RUN_ERROR]", types)
	}
}

func TestTranslateEvent_Error_DefaultMessage(t *testing.T) {
	t.Parallel()
	state := newStreamState("t1", "r1")
	w := &capturingSSEWriter{}

	state.translateEvent(&bytes.Buffer{}, w, api.StreamEvent{Type: api.EventError}, nopFilter{})

	if len(w.events) != 1 || w.types()[0] != "RUN_ERROR" {
		t.Fatalf("got %v, want [RUN_ERROR]", w.types())
	}
}

func TestFinalize_FullSequence(t *testing.T) {
	t.Parallel()
	state := newStreamState("t1", "r1")
	state.textStarted = true
	state.lastToolID = "tc_open"
	state.toolCalls["tc_open"] = true
	state.lastStep = "iteration_1"

	w := &capturingSSEWriter{}
	state.finalize(&bytes.Buffer{}, w, nopFilter{})

	types := w.types()
	want := []string{"TOOL_CALL_END", "TEXT_MESSAGE_END", "STEP_FINISHED", "RUN_FINISHED"}
	if len(types) != len(want) {
		t.Fatalf("finalize events = %v, want %v", types, want)
	}
	for i, wt := range want {
		if types[i] != wt {
			t.Errorf("event[%d] = %q, want %q", i, types[i], wt)
		}
	}
}

func TestFinalize_MinimalNoOpenState(t *testing.T) {
	t.Parallel()
	state := newStreamState("t1", "r1")
	w := &capturingSSEWriter{}
	state.finalize(&bytes.Buffer{}, w, nopFilter{})

	types := w.types()
	if len(types) != 1 || types[0] != "RUN_FINISHED" {
		t.Fatalf("minimal finalize = %v, want [RUN_FINISHED]", types)
	}
}

func TestFinalize_FlushesFilterTail(t *testing.T) {
	t.Parallel()
	state := newStreamState("t1", "r1")
	state.textStarted = true
	f := &tailFilter{buf: "leftover"}

	w := &capturingSSEWriter{}
	state.finalize(&bytes.Buffer{}, w, f)

	types := w.types()
	if len(types) != 3 {
		t.Fatalf("expected 3 events, got %v", types)
	}
	if types[0] != "TEXT_MESSAGE_CONTENT" {
		t.Errorf("flush tail should emit content, got %q", types[0])
	}
	if types[1] != "TEXT_MESSAGE_END" {
		t.Errorf("then end message, got %q", types[1])
	}
}

func TestInputToJSON(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		input interface{}
		want  string
	}{
		{"nil", nil, "{}"},
		{"string", `{"a":1}`, `{"a":1}`},
		{"map", map[string]int{"x": 1}, `{"x":1}`},
		{"slice", []string{"a"}, `["a"]`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := inputToJSON(c.input)
			if got != c.want {
				t.Errorf("inputToJSON(%v) = %q, want %q", c.input, got, c.want)
			}
		})
	}
}

func TestNewStreamState(t *testing.T) {
	t.Parallel()
	s := newStreamState("thread_abc", "run_xyz")
	if s.threadID != "thread_abc" {
		t.Errorf("threadID = %q", s.threadID)
	}
	if s.runID != "run_xyz" {
		t.Errorf("runID = %q", s.runID)
	}
	if !strings.HasPrefix(s.msgID, "msg_") {
		t.Errorf("msgID should start with msg_, got %q", s.msgID)
	}
	if s.textStarted {
		t.Error("textStarted should be false initially")
	}
	if len(s.toolCalls) != 0 {
		t.Error("toolCalls should be empty initially")
	}
}
