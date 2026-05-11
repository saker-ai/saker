package model

import (
	"encoding/json"

	"github.com/openai/openai-go"
)

// openai_response.go decodes the openai SDK response objects (both completion
// and usage) back into the provider-neutral Response/Usage types. JSON-arg
// parsing for tool calls also lives here because both the streaming and
// non-streaming paths share it.

func parseJSONArgs(raw string) map[string]any {
	if raw == "" {
		return nil
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return map[string]any{"raw": raw}
	}
	return args
}

func convertOpenAIResponse(completion *openai.ChatCompletion) *Response {
	if completion == nil || len(completion.Choices) == 0 {
		return &Response{
			Message: Message{Role: "assistant"},
		}
	}

	choice := completion.Choices[0]
	msg := choice.Message

	var toolCalls []ToolCall
	for _, tc := range msg.ToolCalls {
		toolCalls = append(toolCalls, ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: parseJSONArgs(tc.Function.Arguments),
		})
	}

	var reasoningContent string
	if raw := msg.RawJSON(); raw != "" {
		var parsed map[string]json.RawMessage
		if err := json.Unmarshal([]byte(raw), &parsed); err == nil {
			if rc, ok := parsed["reasoning_content"]; ok {
				json.Unmarshal(rc, &reasoningContent) //nolint:errcheck // best-effort extraction
			}
		}
	}

	return &Response{
		Message: Message{
			Role:             "assistant",
			Content:          msg.Content,
			ToolCalls:        toolCalls,
			ReasoningContent: reasoningContent,
		},
		Usage:      convertOpenAIUsage(completion.Usage),
		StopReason: choice.FinishReason,
	}
}

func convertOpenAIUsage(usage openai.CompletionUsage) Usage {
	return Usage{
		InputTokens:  int(usage.PromptTokens),
		OutputTokens: int(usage.CompletionTokens),
		TotalTokens:  int(usage.TotalTokens),
	}
}
