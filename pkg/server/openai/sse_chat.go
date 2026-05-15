package openai

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/saker-ai/saker/pkg/api"
)

// chatChunkBuilder is per-run state that turns saker StreamEvents into
// OpenAI chat.completion.chunk objects. One builder per request; the
// builder owns the cumulative `Created` timestamp, the assigned chunk id,
// and the streaming-artifact filter applied to every text delta.
type chatChunkBuilder struct {
	id        string
	runID     string
	created   int64
	model     string
	finish    string
	roleSent  bool
	emittedAt time.Time
	// errorMode is one of ErrorDetailDev / ErrorDetailProd. Empty defaults
	// to ErrorDetailDev (raw error text passes through to the client).
	errorMode string
	// usage is the most recent token usage snapshot we've observed in the
	// stream, captured for stream_options.include_usage. Zero value means
	// "no usage observed yet".
	usage chatUsageSnapshot
}

// chatUsageSnapshot mirrors api.Usage but flattened to the OpenAI usage
// shape (prompt/completion/total). Zero value = "no usage seen".
type chatUsageSnapshot struct {
	PromptTokens     int
	CompletionTokens int
}

func newChatChunkBuilder(id, runID, model, errorMode string) *chatChunkBuilder {
	return &chatChunkBuilder{
		id:        id,
		runID:     runID,
		model:     model,
		created:   time.Now().Unix(),
		errorMode: errorMode,
	}
}

// translate converts a saker StreamEvent into zero-or-more
// ChatCompletionChunk envelopes ready for SSE serialization. Returns
// (nil, "") when the event has no client-visible payload (e.g. an
// agent-internal iteration_start).
//
// finishReason carries the OpenAI-flavored finish reason ("stop",
// "tool_calls", "length") when the underlying StreamEvent indicates the
// run is wrapping. Caller uses it to decide when to emit the final
// chunk + [DONE] marker.
func (b *chatChunkBuilder) translate(evt api.StreamEvent, exposeTools bool, filter interface {
	Push(string) string
	Flush() string
}) (chunks []ChatCompletionChunk, finish string) {
	switch evt.Type {
	case api.EventAgentStart, api.EventMessageStart:
		// Emit a role-only chunk so SDKs that track delta.role can latch
		// it once at the start.
		if !b.roleSent {
			b.roleSent = true
			chunks = append(chunks, b.envelope(ChatChoice{
				Index: 0,
				Delta: &ChatMessageOut{Role: "assistant"},
			}))
		}
		// Anthropic streams the prompt-token count on message_start; grab
		// it now so include_usage can emit a meaningful prompt count even
		// when message_delta arrives without input_tokens later.
		if evt.Message != nil && evt.Message.Usage != nil {
			b.captureUsage(evt.Message.Usage)
		}
	case api.EventContentBlockDelta:
		if evt.Delta == nil || evt.Delta.Text == "" {
			return nil, ""
		}
		safe := filter.Push(evt.Delta.Text)
		if safe == "" {
			return nil, ""
		}
		chunks = append(chunks, b.envelope(ChatChoice{
			Index: 0,
			Delta: &ChatMessageOut{Content: safe},
		}))
	case api.EventToolExecutionStart:
		if !exposeTools {
			return nil, ""
		}
		// Surface a tool_call delta so the client knows a tool fired.
		args := ""
		if evt.Input != nil {
			if s, ok := evt.Input.(string); ok {
				args = s
			} else if mb, ok := evt.Input.(map[string]any); ok {
				args = mapToCompactJSON(mb)
			}
		}
		chunks = append(chunks, b.envelope(ChatChoice{
			Index: 0,
			Delta: &ChatMessageOut{
				ToolCalls: []ChatToolCall{{
					ID:   evt.ToolUseID,
					Type: "function",
					Function: ChatToolCallInvocation{
						Name:      evt.Name,
						Arguments: args,
					},
				}},
			},
		}))
	case api.EventMessageDelta:
		if evt.Delta != nil && evt.Delta.StopReason != "" {
			finish = mapStopReason(evt.Delta.StopReason)
			b.finish = finish
		}
		if evt.Usage != nil {
			b.captureUsage(evt.Usage)
		}
	case api.EventAgentStop, api.EventMessageStop:
		// Capture cumulative usage if the runtime stamped one onto the
		// terminal envelope.
		if evt.Message != nil && evt.Message.Usage != nil {
			b.captureUsage(evt.Message.Usage)
		}
		if evt.Usage != nil {
			b.captureUsage(evt.Usage)
		}
		// Finalize: flush filter then emit a finish-marker chunk.
		var tail string
		if filter != nil {
			tail = filter.Flush()
		}
		if tail != "" {
			chunks = append(chunks, b.envelope(ChatChoice{
				Index: 0,
				Delta: &ChatMessageOut{Content: tail},
			}))
		}
		if b.finish == "" {
			b.finish = "stop"
		}
		finish = b.finish
		chunks = append(chunks, b.envelope(ChatChoice{
			Index:        0,
			Delta:        &ChatMessageOut{},
			FinishReason: b.finish,
		}))
	case api.EventError:
		// On error, surface a content delta first so clients see WHY the
		// run failed (an empty stop with no signal is the worst debugging
		// experience), then a finish chunk so SDKs don't leave the stream
		// half-open. finish_reason stays "stop" because OpenAI's vocabulary
		// has no dedicated error code at the chunk level — failures
		// usually surface as HTTP 5xx, but mid-stream we can only stop
		// cleanly.
		//
		// Production mode (errorMode=prod) replaces the raw message with
		// a run_id pointer so operators can correlate to logs without
		// leaking stack traces, provider URLs, or transient infra detail
		// to the client.
		var content string
		if b.errorMode == ErrorDetailProd {
			if b.runID != "" {
				content = "[saker error] internal error (run_id=" + b.runID + ")"
			} else {
				content = "[saker error] internal error"
			}
		} else {
			msg := stringifyOutput(evt.Output)
			if msg == "" {
				msg = "saker runtime error"
			}
			content = "[saker error] " + msg
		}
		chunks = append(chunks, b.envelope(ChatChoice{
			Index: 0,
			Delta: &ChatMessageOut{Content: content},
		}))
		b.finish = "stop"
		finish = "stop"
		chunks = append(chunks, b.envelope(ChatChoice{
			Index:        0,
			Delta:        &ChatMessageOut{},
			FinishReason: "stop",
		}))
	}
	return chunks, finish
}

func (b *chatChunkBuilder) envelope(choice ChatChoice) ChatCompletionChunk {
	return ChatCompletionChunk{
		ID:      b.id,
		Object:  "chat.completion.chunk",
		Created: b.created,
		Model:   b.model,
		Choices: []ChatChoice{choice},
	}
}

// captureUsage merges an Anthropic-shaped Usage into the builder's running
// snapshot. Anthropic emits cumulative totals at message_start (input only)
// and message_delta / message_stop (output appended), so we treat each
// non-zero field as the latest cumulative value rather than summing.
func (b *chatChunkBuilder) captureUsage(u *api.Usage) {
	if u == nil {
		return
	}
	if u.InputTokens > 0 {
		b.usage.PromptTokens = u.InputTokens
	}
	if u.OutputTokens > 0 {
		b.usage.CompletionTokens = u.OutputTokens
	}
}

// usageChunk produces the OpenAI-shaped final usage frame requested by
// stream_options.include_usage. Per the OpenAI spec the chunk has an empty
// choices array and a populated usage object. Returns (chunk, true) when
// usage is observable, (zero-value, false) when nothing was captured (in
// which case the caller should skip emitting the frame to avoid emitting
// an all-zero usage object).
func (b *chatChunkBuilder) usageChunk() (ChatCompletionChunk, bool) {
	if b.usage.PromptTokens == 0 && b.usage.CompletionTokens == 0 {
		return ChatCompletionChunk{}, false
	}
	return ChatCompletionChunk{
		ID:      b.id,
		Object:  "chat.completion.chunk",
		Created: b.created,
		Model:   b.model,
		Choices: []ChatChoice{},
		Usage: &ChatUsage{
			PromptTokens:     b.usage.PromptTokens,
			CompletionTokens: b.usage.CompletionTokens,
			TotalTokens:      b.usage.PromptTokens + b.usage.CompletionTokens,
		},
	}, true
}

// snapshotUsage exposes the captured usage for the non-streaming response
// path, where usage rides on the single chat.completion JSON. Returns nil
// when no usage was observed.
func (b *chatChunkBuilder) snapshotUsage() *ChatUsage {
	if b.usage.PromptTokens == 0 && b.usage.CompletionTokens == 0 {
		return nil
	}
	return &ChatUsage{
		PromptTokens:     b.usage.PromptTokens,
		CompletionTokens: b.usage.CompletionTokens,
		TotalTokens:      b.usage.PromptTokens + b.usage.CompletionTokens,
	}
}

// mapStopReason normalizes saker / Anthropic stop reasons onto OpenAI's
// finish_reason vocabulary.
func mapStopReason(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "end_turn", "stop_sequence":
		return "stop"
	case "tool_use":
		return "tool_calls"
	case "max_tokens":
		return "length"
	case "":
		return "stop"
	default:
		return s
	}
}

// stringifyOutput coerces a StreamEvent.Output (typed as interface{}) to a
// human-readable string. Strings pass through; everything else falls back
// to JSON. Empty result lets the caller substitute a default.
func stringifyOutput(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s)
	}
	if b, err := json.Marshal(v); err == nil {
		return string(b)
	}
	return ""
}

// mapToCompactJSON serializes a map to compact JSON without trailing
// newline, used to render tool input as a string for delta.tool_calls.
// Errors in marshal are swallowed — the worst case is empty arguments,
// which is what the OpenAI spec prescribes for tool calls with no args.
func mapToCompactJSON(m map[string]any) string {
	if len(m) == 0 {
		return "{}"
	}
	out, err := json.Marshal(m)
	if err != nil {
		return "{}"
	}
	return string(out)
}
