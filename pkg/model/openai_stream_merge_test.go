package model

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/ssestream"
)

// openaiFakeDecoder feeds the openai-go ssestream a fixed sequence of SSE
// events. Mirrors the anthropic_additional_test.go fakeDecoder but typed for
// openai.ChatCompletionChunk.
type openaiFakeDecoder struct {
	events []ssestream.Event
	idx    int
	err    error
}

func (d *openaiFakeDecoder) Next() bool {
	if d.idx >= len(d.events) {
		return false
	}
	d.idx++
	return true
}

func (d *openaiFakeDecoder) Event() ssestream.Event {
	if d.idx == 0 || d.idx > len(d.events) {
		return ssestream.Event{}
	}
	return d.events[d.idx-1]
}

func (d *openaiFakeDecoder) Close() error { return nil }
func (d *openaiFakeDecoder) Err() error   { return d.err }

// chunkEvent serializes one ChatCompletionChunk JSON for the SSE stream.
// We construct the JSON by hand rather than via openai.ChatCompletionChunk{}
// so we can exercise exactly what arrives over the wire — including
// pathological cases where adjacent deltas have no inter-token whitespace.
func chunkEvent(t *testing.T, content string) ssestream.Event {
	t.Helper()
	payload := map[string]any{
		"id":      "chatcmpl-test",
		"object":  "chat.completion.chunk",
		"created": 0,
		"model":   "test-model",
		"choices": []map[string]any{
			{
				"index":         0,
				"delta":         map[string]any{"content": content},
				"finish_reason": nil,
			},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal chunk: %v", err)
	}
	return ssestream.Event{Type: "", Data: body}
}

func finalChunkEvent(t *testing.T, finishReason string, prompt, completion int) ssestream.Event {
	t.Helper()
	payload := map[string]any{
		"id":      "chatcmpl-test",
		"object":  "chat.completion.chunk",
		"created": 0,
		"model":   "test-model",
		"choices": []map[string]any{
			{
				"index":         0,
				"delta":         map[string]any{},
				"finish_reason": finishReason,
			},
		},
		"usage": map[string]any{
			"prompt_tokens":     prompt,
			"completion_tokens": completion,
			"total_tokens":      prompt + completion,
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal final chunk: %v", err)
	}
	return ssestream.Event{Type: "", Data: body}
}

// fakeOpenAIStreamCompletions is a minimal openaiChatCompletions that returns
// a pre-built ssestream when NewStreaming is called. Implementing the
// interface directly is simpler than reusing mockOpenAIChatCompletions
// because that mock returns nil unless streamFunc is configured.
type fakeOpenAIStreamCompletions struct {
	stream *ssestream.Stream[openai.ChatCompletionChunk]
}

func (f *fakeOpenAIStreamCompletions) New(_ context.Context, _ openai.ChatCompletionNewParams, _ ...option.RequestOption) (*openai.ChatCompletion, error) {
	return nil, nil
}

func (f *fakeOpenAIStreamCompletions) NewStreaming(_ context.Context, _ openai.ChatCompletionNewParams, _ ...option.RequestOption) *ssestream.Stream[openai.ChatCompletionChunk] {
	return f.stream
}

// TestOpenAIStream_FaithfulMergeReproducesUpstreamCorruption verifies that
// when an OpenAI-compatible upstream sends content deltas with NO inter-token
// whitespace (which we observed in the wild from a deepseek-v4-pro proxy),
// our adapter concatenates them verbatim. This pinpoints garbled output like
// "smallestpossible016x" as upstream corruption rather than a bug in our
// stream-merge code — the test would fail loudly if we ever started
// silently dropping or transforming chunks.
func TestOpenAIStream_FaithfulMergeReproducesUpstreamCorruption(t *testing.T) {
	t.Parallel()

	// Three deltas that, when concatenated without separators, reproduce the
	// exact pattern we saw in tb2-20260427-080217's transcript. Note the
	// missing spaces between deltas.
	events := []ssestream.Event{
		chunkEvent(t, "the smallest"),
		chunkEvent(t, "possible0"),
		chunkEvent(t, "16x in the domain"),
		finalChunkEvent(t, "stop", 10, 5),
	}
	stream := ssestream.NewStream[openai.ChatCompletionChunk](&openaiFakeDecoder{events: events}, nil)

	mdl := &openaiModel{
		completions: &fakeOpenAIStreamCompletions{stream: stream},
		model:       "deepseek-v4-pro",
		maxTokens:   100,
		maxRetries:  0,
	}

	var deltas []string
	var finalContent string
	err := mdl.CompleteStream(context.Background(), Request{
		Messages: []Message{{Role: "user", Content: "explain"}},
	}, func(sr StreamResult) error {
		if sr.Final && sr.Response != nil {
			finalContent = sr.Response.Message.Content
			return nil
		}
		if sr.Delta != "" {
			deltas = append(deltas, sr.Delta)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("CompleteStream: %v", err)
	}

	wantMerged := "the smallestpossible016x in the domain"
	if finalContent != wantMerged {
		t.Errorf("merged content = %q, want %q\n(if these differ, our merge dropped or transformed a chunk)", finalContent, wantMerged)
	}

	// Each delta must arrive intact at the callback — no batching, no
	// transformation. The downstream user (e.g. terminalbench transcript
	// dumper) needs every chunk to be able to reconstruct on its own.
	if len(deltas) != 3 {
		t.Errorf("delta count = %d, want 3 (one per content chunk): %q", len(deltas), deltas)
	}
	merged := strings.Join(deltas, "")
	if merged != wantMerged {
		t.Errorf("delta concat = %q, want %q", merged, wantMerged)
	}
}

// TestOpenAIStream_PreservesLeadingTrailingSpaceInDeltas hammers the
// opposite corner: when upstream DOES include leading/trailing whitespace
// in deltas, we must preserve it. A regression here would silently
// re-introduce the symptom of garbled transcripts because the agent's
// reasoning text would lose word boundaries on every iteration.
func TestOpenAIStream_PreservesLeadingTrailingSpaceInDeltas(t *testing.T) {
	t.Parallel()

	events := []ssestream.Event{
		chunkEvent(t, "the"),
		chunkEvent(t, " un"),
		chunkEvent(t, "normalized"),
		chunkEvent(t, " log-density"),
		finalChunkEvent(t, "stop", 5, 4),
	}
	stream := ssestream.NewStream[openai.ChatCompletionChunk](&openaiFakeDecoder{events: events}, nil)

	mdl := &openaiModel{
		completions: &fakeOpenAIStreamCompletions{stream: stream},
		model:       "deepseek-v4-pro",
		maxTokens:   100,
		maxRetries:  0,
	}

	var finalContent string
	err := mdl.CompleteStream(context.Background(), Request{
		Messages: []Message{{Role: "user", Content: "go"}},
	}, func(sr StreamResult) error {
		if sr.Final && sr.Response != nil {
			finalContent = sr.Response.Message.Content
		}
		return nil
	})
	if err != nil {
		t.Fatalf("CompleteStream: %v", err)
	}

	if want := "the unnormalized log-density"; finalContent != want {
		t.Errorf("merged content = %q, want %q", finalContent, want)
	}
}
