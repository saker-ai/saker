package conversation

import (
	"encoding/json"

	"github.com/cinience/saker/pkg/message"
)

// ToRuntimeMessages converts projected conversation messages back to the
// runtime message.Message slice that historyStore / Run / RunStream
// expect. This is the inverse of appendMessageEvents in
// pkg/api/conversation_persist.go.
//
// Tool-call pairing: assistant messages carry a ToolCalls JSON array;
// tool-role messages carry a ToolCallID linking them back. Both are
// faithfully restored so the runtime can re-submit the conversation to
// a provider without loss.
func ToRuntimeMessages(msgs []Message) []message.Message {
	out := make([]message.Message, 0, len(msgs))
	for _, m := range msgs {
		rm := message.Message{
			Role:    m.Role,
			Content: m.Content,
		}
		if m.Role == "tool" && m.ToolCallID != "" {
			rm.ToolCalls = []message.ToolCall{{ID: m.ToolCallID, Result: m.Content}}
		}
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			rm.ToolCalls = parseToolCalls(m.ToolCalls)
		}
		out = append(out, rm)
	}
	return out
}

// parseToolCalls deserializes the JSON tool_calls array stored on
// assistant messages back to runtime ToolCall values.
func parseToolCalls(raw json.RawMessage) []message.ToolCall {
	var calls []struct {
		ID        string         `json:"id"`
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &calls); err != nil {
		return nil
	}
	out := make([]message.ToolCall, len(calls))
	for i, c := range calls {
		out[i] = message.ToolCall{
			ID:        c.ID,
			Name:      c.Name,
			Arguments: c.Arguments,
		}
	}
	return out
}
