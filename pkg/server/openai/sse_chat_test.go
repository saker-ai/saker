package openai

import (
	"strings"
	"testing"

	"github.com/cinience/saker/pkg/api"
)

// nopFilter is a passthrough streamArtifactFilter stand-in for tests:
// every Push returns its input verbatim, Flush returns "".
type nopFilter struct{}

func (nopFilter) Push(s string) string { return s }
func (nopFilter) Flush() string        { return "" }

func TestMapStopReason(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"end_turn", "stop"},
		{"stop_sequence", "stop"},
		{"tool_use", "tool_calls"},
		{"max_tokens", "length"},
		{"", "stop"},
		{"  END_TURN  ", "stop"},
		{"weird_unmapped", "weird_unmapped"},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			if got := mapStopReason(c.in); got != c.want {
				t.Errorf("mapStopReason(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestChatChunkBuilder_RoleEmittedOnce(t *testing.T) {
	b := newChatChunkBuilder("chatcmpl-1", "run_1", "saker-mid", ErrorDetailDev)
	chunks, _ := b.translate(api.StreamEvent{Type: api.EventMessageStart}, false, nopFilter{})
	if len(chunks) != 1 {
		t.Fatalf("first message_start: got %d chunks, want 1", len(chunks))
	}
	if chunks[0].Choices[0].Delta == nil || chunks[0].Choices[0].Delta.Role != "assistant" {
		t.Errorf("expected role=assistant delta, got %+v", chunks[0].Choices[0])
	}
	chunks2, _ := b.translate(api.StreamEvent{Type: api.EventMessageStart}, false, nopFilter{})
	if len(chunks2) != 0 {
		t.Errorf("second message_start should not re-emit role, got %d chunks", len(chunks2))
	}
}

func TestChatChunkBuilder_TextDelta(t *testing.T) {
	b := newChatChunkBuilder("id", "run", "m", ErrorDetailDev)
	evt := api.StreamEvent{Type: api.EventContentBlockDelta, Delta: &api.Delta{Text: "hello"}}
	chunks, _ := b.translate(evt, false, nopFilter{})
	if len(chunks) != 1 {
		t.Fatalf("got %d chunks, want 1", len(chunks))
	}
	if chunks[0].Choices[0].Delta.Content != "hello" {
		t.Errorf("delta.content = %q, want hello", chunks[0].Choices[0].Delta.Content)
	}
}

func TestChatChunkBuilder_TextDelta_FilterSwallows(t *testing.T) {
	swallow := struct {
		nopFilter
	}{}
	_ = swallow
	type swallower struct{}
	// build a filter that swallows everything
	type customFilter struct{}
	b := newChatChunkBuilder("id", "run", "m", ErrorDetailDev)
	evt := api.StreamEvent{Type: api.EventContentBlockDelta, Delta: &api.Delta{Text: "anything"}}
	chunks, _ := b.translate(evt, false, swallowAllFilter{})
	if len(chunks) != 0 {
		t.Fatalf("filter swallowed text but got %d chunks", len(chunks))
	}
}

type swallowAllFilter struct{}

func (swallowAllFilter) Push(s string) string { return "" }
func (swallowAllFilter) Flush() string        { return "" }

func TestChatChunkBuilder_ToolExecution_ExposeToggle(t *testing.T) {
	b := newChatChunkBuilder("id", "run", "m", ErrorDetailDev)
	evt := api.StreamEvent{
		Type:      api.EventToolExecutionStart,
		ToolUseID: "call_1",
		Name:      "search",
		Input:     map[string]any{"q": "bugs"},
	}
	if chunks, _ := b.translate(evt, false, nopFilter{}); len(chunks) != 0 {
		t.Errorf("expose=false: expected 0 chunks, got %d", len(chunks))
	}
	chunks, _ := b.translate(evt, true, nopFilter{})
	if len(chunks) != 1 {
		t.Fatalf("expose=true: expected 1 chunk, got %d", len(chunks))
	}
	tcs := chunks[0].Choices[0].Delta.ToolCalls
	if len(tcs) != 1 || tcs[0].ID != "call_1" || tcs[0].Function.Name != "search" {
		t.Errorf("tool_call delta wrong: %+v", tcs)
	}
	if !strings.Contains(tcs[0].Function.Arguments, `"q":"bugs"`) {
		t.Errorf("expected JSON-encoded args, got %q", tcs[0].Function.Arguments)
	}
}

func TestChatChunkBuilder_MessageDeltaCapturesStopAndUsage(t *testing.T) {
	b := newChatChunkBuilder("id", "run", "m", ErrorDetailDev)
	evt := api.StreamEvent{
		Type:  api.EventMessageDelta,
		Delta: &api.Delta{StopReason: "tool_use"},
		Usage: &api.Usage{InputTokens: 100, OutputTokens: 25},
	}
	chunks, finish := b.translate(evt, false, nopFilter{})
	if finish != "tool_calls" {
		t.Errorf("finish = %q, want tool_calls", finish)
	}
	if len(chunks) != 0 {
		t.Errorf("message_delta should not emit chunks itself, got %d", len(chunks))
	}
	if b.usage.PromptTokens != 100 || b.usage.CompletionTokens != 25 {
		t.Errorf("usage capture: got %+v", b.usage)
	}
}

func TestChatChunkBuilder_MessageStop_FinalChunkAndFlush(t *testing.T) {
	b := newChatChunkBuilder("id", "run", "m", ErrorDetailDev)
	chunks, finish := b.translate(api.StreamEvent{Type: api.EventMessageStop}, false, nopFilter{})
	if finish != "stop" {
		t.Errorf("finish = %q, want stop", finish)
	}
	if len(chunks) != 1 {
		t.Fatalf("got %d chunks, want 1 (final)", len(chunks))
	}
	if chunks[0].Choices[0].FinishReason != "stop" {
		t.Errorf("final FinishReason = %q, want stop", chunks[0].Choices[0].FinishReason)
	}
}

func TestChatChunkBuilder_ErrorBranch_Dev(t *testing.T) {
	b := newChatChunkBuilder("id", "run_xyz", "m", ErrorDetailDev)
	chunks, finish := b.translate(api.StreamEvent{
		Type:   api.EventError,
		Output: "things broke",
	}, false, nopFilter{})
	if finish != "stop" {
		t.Errorf("finish = %q, want stop", finish)
	}
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks (content+finish), got %d", len(chunks))
	}
	if !strings.Contains(chunks[0].Choices[0].Delta.Content, "things broke") {
		t.Errorf("dev mode should leak raw error, got %q", chunks[0].Choices[0].Delta.Content)
	}
	if strings.Contains(chunks[0].Choices[0].Delta.Content, "run_xyz") {
		t.Errorf("dev mode should not include run_id, got %q", chunks[0].Choices[0].Delta.Content)
	}
}

func TestChatChunkBuilder_ErrorBranch_Prod(t *testing.T) {
	b := newChatChunkBuilder("id", "run_xyz", "m", ErrorDetailProd)
	chunks, _ := b.translate(api.StreamEvent{
		Type:   api.EventError,
		Output: "secret stack trace with provider URL",
	}, false, nopFilter{})
	if len(chunks) < 1 {
		t.Fatal("expected at least 1 chunk")
	}
	content := chunks[0].Choices[0].Delta.Content
	if strings.Contains(content, "secret stack trace") {
		t.Errorf("prod mode leaked raw error: %q", content)
	}
	if !strings.Contains(content, "run_xyz") {
		t.Errorf("prod mode should reference run_id, got %q", content)
	}
	if !strings.Contains(content, "internal error") {
		t.Errorf("prod mode should say 'internal error', got %q", content)
	}
}

func TestChatChunkBuilder_CaptureUsage_Cumulative(t *testing.T) {
	b := newChatChunkBuilder("id", "run", "m", ErrorDetailDev)
	b.captureUsage(&api.Usage{InputTokens: 100, OutputTokens: 0})
	b.captureUsage(&api.Usage{InputTokens: 0, OutputTokens: 50})
	b.captureUsage(&api.Usage{InputTokens: 0, OutputTokens: 75}) // OutputTokens grows
	if b.usage.PromptTokens != 100 {
		t.Errorf("prompt tokens = %d, want 100", b.usage.PromptTokens)
	}
	if b.usage.CompletionTokens != 75 {
		t.Errorf("completion tokens = %d, want 75 (latest non-zero wins)", b.usage.CompletionTokens)
	}
}

func TestChatChunkBuilder_UsageChunk(t *testing.T) {
	b := newChatChunkBuilder("id", "run", "m", ErrorDetailDev)
	if _, ok := b.usageChunk(); ok {
		t.Error("expected (false) when no usage observed yet")
	}
	b.captureUsage(&api.Usage{InputTokens: 10, OutputTokens: 5})
	chunk, ok := b.usageChunk()
	if !ok {
		t.Fatal("expected (true) once usage is captured")
	}
	if chunk.Usage == nil {
		t.Fatal("usage chunk must have Usage populated")
	}
	if chunk.Usage.PromptTokens != 10 || chunk.Usage.CompletionTokens != 5 || chunk.Usage.TotalTokens != 15 {
		t.Errorf("usage = %+v, want {10,5,15}", chunk.Usage)
	}
	if len(chunk.Choices) != 0 {
		t.Errorf("OpenAI spec requires empty choices, got %d", len(chunk.Choices))
	}
}

func TestChatChunkBuilder_SnapshotUsage(t *testing.T) {
	b := newChatChunkBuilder("id", "run", "m", ErrorDetailDev)
	if u := b.snapshotUsage(); u != nil {
		t.Errorf("expected nil snapshot when nothing captured, got %+v", u)
	}
	b.captureUsage(&api.Usage{InputTokens: 7, OutputTokens: 3})
	u := b.snapshotUsage()
	if u == nil || u.PromptTokens != 7 || u.CompletionTokens != 3 || u.TotalTokens != 10 {
		t.Errorf("snapshot wrong: %+v", u)
	}
}

func TestStringifyOutput(t *testing.T) {
	if got := stringifyOutput(nil); got != "" {
		t.Errorf("nil = %q, want empty", got)
	}
	if got := stringifyOutput("  abc  "); got != "abc" {
		t.Errorf("string trim: got %q, want abc", got)
	}
	if got := stringifyOutput(map[string]any{"k": "v"}); !strings.Contains(got, `"k":"v"`) {
		t.Errorf("map fallback to JSON: got %q", got)
	}
}

func TestMapToCompactJSON(t *testing.T) {
	if got := mapToCompactJSON(nil); got != "{}" {
		t.Errorf("nil = %q, want {}", got)
	}
	got := mapToCompactJSON(map[string]any{"a": 1})
	if !strings.Contains(got, `"a":1`) {
		t.Errorf("got %q", got)
	}
	if strings.Contains(got, "\n") {
		t.Errorf("compact json should have no newlines, got %q", got)
	}
}
