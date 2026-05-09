package server

import (
	"strings"
	"testing"
)

// TestStreamFilterPlainTextPasses confirms the fast path forwards prose
// untouched when no tag-shaped chars are present.
func TestStreamFilterPlainTextPasses(t *testing.T) {
	t.Parallel()
	f := &streamArtifactFilter{}
	if got := f.Push("hello world"); got != "hello world" {
		t.Fatalf("plain text must pass through, got %q", got)
	}
	if got := f.Flush(); got != "" {
		t.Fatalf("flush after clean push must be empty, got %q", got)
	}
}

// TestStreamFilterStripsFullTagArrivedInOneChunk regresses the eddaff17
// case where the model emitted "</tool_call>" in a single text_delta. Even
// without splitting across chunks, the filter must remove it.
func TestStreamFilterStripsFullTagArrivedInOneChunk(t *testing.T) {
	t.Parallel()
	f := &streamArtifactFilter{}
	got := f.Push("answer </tool_call> ok")
	if strings.Contains(got, "tool_call") {
		t.Fatalf("artifact must be stripped, got %q", got)
	}
}

// TestStreamFilterHoldsPartialTagAcrossChunks is the core SSE contract: a
// tag split mid-stream must NOT leak the opener to subscribers before the
// closer arrives, otherwise the user's UI would briefly show "</tool_c"
// before it disappears on stream end.
func TestStreamFilterHoldsPartialTagAcrossChunks(t *testing.T) {
	t.Parallel()
	f := &streamArtifactFilter{}
	out1 := f.Push("answer </tool")
	if strings.Contains(out1, "tool") {
		t.Fatalf("partial tag must be held back, got %q", out1)
	}
	if !strings.HasPrefix(out1, "answer ") {
		t.Fatalf("safe prefix must still be emitted, got %q", out1)
	}
	out2 := f.Push("_call> done")
	if strings.Contains(out2, "tool_call") {
		t.Fatalf("once completed, tag must be stripped, got %q", out2)
	}
	if !strings.Contains(out2, "done") {
		t.Fatalf("text after tag must survive, got %q", out2)
	}
}

// TestStreamFilterHoldsSentinelToken ensures <|...|> open-model fences are
// also held mid-stream, since they hit fenceArtifactRe in the strip pass.
func TestStreamFilterHoldsSentinelToken(t *testing.T) {
	t.Parallel()
	f := &streamArtifactFilter{}
	out1 := f.Push("hi <|FunctionCallBeg")
	if strings.Contains(out1, "<|") {
		t.Fatalf("partial sentinel must be held, got %q", out1)
	}
	out2 := f.Push("in|> bye")
	if strings.Contains(out2, "FunctionCallBegin") {
		t.Fatalf("completed sentinel must be stripped, got %q", out2)
	}
	if !strings.Contains(out2, "bye") {
		t.Fatalf("text after sentinel must survive, got %q", out2)
	}
}

// TestStreamFilterDoesNotHoldProseLessThan ensures we don't bog down on
// natural-language "<" usage like "5 < 10" — the next char (' ' or '1') is
// not a tag-name start, so emit immediately.
func TestStreamFilterDoesNotHoldProseLessThan(t *testing.T) {
	t.Parallel()
	f := &streamArtifactFilter{}
	got := f.Push("5 < 10 is true")
	if got != "5 < 10 is true" {
		t.Fatalf("prose '<' must not be held, got %q", got)
	}
}

// TestStreamFilterFlushReleasesHeldData confirms that if a stream ends with
// a partial fragment (model truncated mid-tag), Flush returns it run through
// the strip pass — half-tags are dropped, valid prose surfaces.
func TestStreamFilterFlushReleasesHeldData(t *testing.T) {
	t.Parallel()
	f := &streamArtifactFilter{}
	out1 := f.Push("done <tool")
	if strings.Contains(out1, "tool") {
		t.Fatalf("partial held, got %q", out1)
	}
	flushed := f.Flush()
	// "<tool" alone doesn't match the strip regex (no closing); we'd rather
	// surface partial garbage than silently drop bytes — but the user-facing
	// reality is that a truncated stream is an error path anyway.
	if !strings.Contains(out1+flushed, "done") {
		t.Fatalf("expected 'done' to survive across push+flush, got push=%q flush=%q", out1, flushed)
	}
}

// TestStreamFilterFullTagBlockStripsContent regresses the eddaff17 risk that
// a model emits a complete <tool_call>...</tool_call> block mid-stream. The
// filter must catch it the moment the closing tag arrives.
func TestStreamFilterFullTagBlockStripsContent(t *testing.T) {
	t.Parallel()
	f := &streamArtifactFilter{}
	out1 := f.Push("hello <tool_call>{\"name\":\"foo\"}")
	out2 := f.Push("</tool_call> world")
	combined := out1 + out2
	if strings.Contains(combined, "tool_call") || strings.Contains(combined, "foo") {
		t.Fatalf("full tag block contents must be stripped, got %q", combined)
	}
	if !strings.Contains(combined, "hello") || !strings.Contains(combined, "world") {
		t.Fatalf("surrounding prose must survive, got %q", combined)
	}
}

// TestStreamFilterPathologicalLongPartial defends against a stream that
// keeps emitting "<" without ever closing — the held buffer is capped at
// streamFilterMaxBuffer; past that we flush to avoid unbounded growth.
func TestStreamFilterPathologicalLongPartial(t *testing.T) {
	t.Parallel()
	f := &streamArtifactFilter{}
	// Force the held buffer past the cap.
	big := "<" + strings.Repeat("tool_callX", 1024) // > 4 KiB, no '>'
	out := f.Push(big)
	// The cap branch flushes through the strip pass; without a closing '>',
	// nothing matches the strip regex, so we expect the bytes to come out
	// rather than be lost or held forever.
	if len(out) == 0 && len(f.pending) > streamFilterMaxBuffer {
		t.Fatalf("held buffer exceeded cap (%d > %d) instead of flushing",
			len(f.pending), streamFilterMaxBuffer)
	}
}

// TestStreamFilterByteByByteStreamMatchesAllAtOnce: drive the filter
// one byte at a time and assert the combined output (after flush) matches
// the strip-pass result for the whole input. This is the integration-style
// guarantee that streaming order doesn't change the final result.
func TestStreamFilterByteByByteStreamMatchesAllAtOnce(t *testing.T) {
	t.Parallel()
	input := "prefix <tool_call>{\"x\":1}</tool_call> middle </function_calls> tail"
	want := stripFunctionCallArtifacts(input)
	want = strings.TrimSpace(want)

	f := &streamArtifactFilter{}
	var got strings.Builder
	for i := 0; i < len(input); i++ {
		got.WriteString(f.Push(input[i : i+1]))
	}
	got.WriteString(f.Flush())
	gotStr := strings.TrimSpace(got.String())

	if gotStr != want {
		t.Fatalf("byte-by-byte stream mismatched all-at-once strip\n got: %q\nwant: %q", gotStr, want)
	}
}
