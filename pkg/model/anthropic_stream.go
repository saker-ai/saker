// anthropic_stream.go: SSE streaming helpers (terminal tool-call extraction, usage fallback).
package model

import (
	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
)

func extractToolCall(msg anthropicsdk.Message) *ToolCall {
	if len(msg.Content) == 0 {
		return nil
	}
	return toolCallFromBlock(msg.Content[len(msg.Content)-1])
}

func usageFromFallback(final anthropicsdk.Usage, tracked Usage) Usage {
	if tracked.InputTokens == 0 && tracked.OutputTokens == 0 {
		return convertUsage(final)
	}
	if tracked.TotalTokens == 0 {
		tracked.TotalTokens = tracked.InputTokens + tracked.OutputTokens
	}
	return tracked
}
