package model

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/openai/openai-go"
)

// openai_stream.go owns the streaming completion path: chunk accumulation,
// tool-call deltas, reasoning_content extraction, and the final-result emit.

// CompleteStream issues a streaming completion, forwarding deltas to cb.
func (m *openaiModel) CompleteStream(ctx context.Context, req Request, cb StreamHandler) error {
	if cb == nil {
		return errors.New("stream callback required")
	}

	recordModelRequest(ctx, req)

	return m.doWithRetry(ctx, func(ctx context.Context) error {
		params, err := m.buildParams(req)
		if err != nil {
			return err
		}

		// Enable usage reporting in stream
		params.StreamOptions = openai.ChatCompletionStreamOptionsParam{
			IncludeUsage: openai.Bool(true),
		}

		stream := m.completions.NewStreaming(ctx, params, m.extraBodyOpts()...)
		if stream == nil {
			return errors.New("openai stream not available")
		}
		defer stream.Close()

		var (
			accumulatedContent   strings.Builder
			accumulatedReasoning strings.Builder
			accumulatedCalls     = make(map[int]*toolCallAccumulator)
			finalUsage           Usage
			finishReason         string
		)

		for stream.Next() {
			chunk := stream.Current()

			// Capture usage from final chunk
			if chunk.Usage.TotalTokens > 0 {
				finalUsage = convertOpenAIUsage(chunk.Usage)
			}

			for _, choice := range chunk.Choices {
				if choice.FinishReason != "" {
					finishReason = string(choice.FinishReason)
				}

				delta := choice.Delta

				// Handle reasoning_content from thinking models
				if raw := delta.RawJSON(); raw != "" {
					var dp map[string]json.RawMessage
					if err := json.Unmarshal([]byte(raw), &dp); err == nil {
						if rc, ok := dp["reasoning_content"]; ok {
							var s string
							if json.Unmarshal(rc, &s) == nil {
								accumulatedReasoning.WriteString(s)
							}
						}
					}
				}

				// Handle text content
				if delta.Content != "" {
					accumulatedContent.WriteString(delta.Content)
					if err := cb(StreamResult{Delta: delta.Content}); err != nil {
						return err
					}
				}

				// Handle tool calls
				for _, tc := range delta.ToolCalls {
					idx := int(tc.Index)
					acc, ok := accumulatedCalls[idx]
					if !ok {
						acc = &toolCallAccumulator{}
						accumulatedCalls[idx] = acc
					}

					if tc.ID != "" {
						acc.id = tc.ID
					}
					if tc.Function.Name != "" {
						acc.name = tc.Function.Name
					}
					acc.arguments.WriteString(tc.Function.Arguments)
				}
			}
		}

		if err := stream.Err(); err != nil {
			return err
		}

		// Emit completed tool calls in order (sort by index to preserve order)
		var indices []int
		for idx := range accumulatedCalls {
			indices = append(indices, idx)
		}
		sort.Ints(indices)

		var toolCalls []ToolCall
		for _, idx := range indices {
			acc := accumulatedCalls[idx]
			tc := acc.toToolCall()
			if tc != nil {
				toolCalls = append(toolCalls, *tc)
				if err := cb(StreamResult{ToolCall: tc}); err != nil {
					return err
				}
			}
		}

		resp := &Response{
			Message: Message{
				Role:             "assistant",
				Content:          accumulatedContent.String(),
				ToolCalls:        toolCalls,
				ReasoningContent: accumulatedReasoning.String(),
			},
			Usage:      finalUsage,
			StopReason: finishReason,
		}
		recordModelResponse(ctx, resp)
		return cb(StreamResult{Final: true, Response: resp})
	})
}

type toolCallAccumulator struct {
	id        string
	name      string
	arguments strings.Builder
}

func (a *toolCallAccumulator) toToolCall() *ToolCall {
	if a.id == "" || a.name == "" {
		return nil
	}
	return &ToolCall{
		ID:        a.id,
		Name:      a.name,
		Arguments: parseJSONArgs(a.arguments.String()),
	}
}
