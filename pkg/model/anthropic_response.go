// anthropic_response.go: non-streaming response parsing helpers (content blocks, tool calls, usage).
package model

import (
	"encoding/json"
	"log/slog"
	"strings"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
)

func convertResponseMessage(msg anthropicsdk.Message) Message {
	var textParts []string
	var thinkingParts []string
	var toolCalls []ToolCall
	for _, block := range msg.Content {
		if tc := toolCallFromBlock(block); tc != nil {
			toolCalls = append(toolCalls, *tc)
			continue
		}
		if block.Type == "thinking" && block.Thinking != "" {
			thinkingParts = append(thinkingParts, block.Thinking)
			continue
		}
		if text := block.Text; text != "" {
			textParts = append(textParts, text)
		}
	}

	role := strings.TrimSpace(string(msg.Role))
	if role == "" {
		role = "assistant"
	}
	return Message{
		Role:             role,
		Content:          strings.Join(textParts, ""),
		ToolCalls:        toolCalls,
		ReasoningContent: strings.Join(thinkingParts, ""),
	}
}

func toolCallFromBlock(block anthropicsdk.ContentBlockUnion) *ToolCall {
	if block.Type != "tool_use" {
		return nil
	}
	id := strings.TrimSpace(block.ID)
	name := strings.TrimSpace(block.Name)
	if id == "" || name == "" {
		return nil
	}
	args := decodeJSON(block.Input)
	if len(args) == 0 && len(block.Input) > 0 {
		slog.Warn("tool_use has empty input", "name", name, "raw", string(block.Input), "hint", "API proxy may have stripped arguments")
	}
	return &ToolCall{
		ID:        id,
		Name:      name,
		Arguments: args,
	}
}

func decodeJSON(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return map[string]any{"raw": string(raw)}
	}
	if v == nil {
		return nil
	}
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{"value": v}
}

func convertUsage(u anthropicsdk.Usage) Usage {
	input := int(u.InputTokens)
	// Usage fields already treat cache tokens as part of input; keep explicit copy.
	cacheRead := int(u.CacheReadInputTokens)
	cacheCreate := int(u.CacheCreationInputTokens)
	return Usage{
		InputTokens:         input,
		OutputTokens:        int(u.OutputTokens),
		TotalTokens:         int(u.OutputTokens) + input,
		CacheReadTokens:     cacheRead,
		CacheCreationTokens: cacheCreate,
	}
}
