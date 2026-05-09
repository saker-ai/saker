package message

import (
	"testing"
)

func TestNormalizeForAPI_empty(t *testing.T) {
	result := NormalizeForAPI(nil)
	if len(result) != 0 {
		t.Errorf("expected empty, got %d", len(result))
	}
}

func TestNormalizeForAPI_orphanToolResult(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "hello"},
		{Role: "tool", ToolCalls: []ToolCall{
			{ID: "orphan_id", Name: "bash", Result: "output"},
		}},
		{Role: "assistant", Content: "response"},
	}
	result := NormalizeForAPI(msgs)
	// The orphan tool_result should be removed
	for _, msg := range result {
		if msg.Role == "tool" {
			for _, tc := range msg.ToolCalls {
				if tc.ID == "orphan_id" {
					t.Error("orphan tool_result should have been removed")
				}
			}
		}
	}
}

func TestNormalizeForAPI_missingToolResult(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", ToolCalls: []ToolCall{
			{ID: "tc1", Name: "bash", Arguments: map[string]any{"command": "ls"}},
		}},
		// No corresponding tool_result for tc1
	}
	result := NormalizeForAPI(msgs)
	// Should synthesize a tool_result for tc1
	found := false
	for _, msg := range result {
		if msg.Role == "tool" {
			for _, tc := range msg.ToolCalls {
				if tc.ID == "tc1" && tc.Result == "[tool result not available]" {
					found = true
				}
			}
		}
	}
	if !found {
		t.Error("expected synthesized tool_result for orphan tool_use")
	}
}

func TestNormalizeForAPI_pairedToolCalls(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", ToolCalls: []ToolCall{
			{ID: "tc1", Name: "bash"},
		}},
		{Role: "tool", ToolCalls: []ToolCall{
			{ID: "tc1", Name: "bash", Result: "ok"},
		}},
		{Role: "assistant", Content: "done"},
	}
	result := NormalizeForAPI(msgs)
	if len(result) != 4 {
		t.Errorf("expected 4 messages, got %d", len(result))
	}
}

func TestNormalizeForAPI_ensureLeadingUser(t *testing.T) {
	msgs := []Message{
		{Role: "assistant", Content: "hello"},
	}
	result := NormalizeForAPI(msgs)
	if result[0].Role != "user" {
		t.Errorf("expected first message to be user, got %s", result[0].Role)
	}
	if result[1].Role != "assistant" {
		t.Errorf("expected second message to be assistant, got %s", result[1].Role)
	}
}

func TestNormalizeForAPI_partialOrphan(t *testing.T) {
	// Tool message with both valid and orphan results
	msgs := []Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", ToolCalls: []ToolCall{
			{ID: "tc1", Name: "bash"},
		}},
		{Role: "tool", ToolCalls: []ToolCall{
			{ID: "tc1", Name: "bash", Result: "ok"},
			{ID: "orphan", Name: "grep", Result: "found"},
		}},
	}
	result := NormalizeForAPI(msgs)
	// The tool message should only have tc1, not orphan
	for _, msg := range result {
		if msg.Role == "tool" {
			for _, tc := range msg.ToolCalls {
				if tc.ID == "orphan" {
					t.Error("orphan tool_result should have been filtered")
				}
			}
			if len(msg.ToolCalls) != 1 {
				t.Errorf("expected 1 tool call, got %d", len(msg.ToolCalls))
			}
		}
	}
}

func TestNormalizeForAPI_noModification(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
	}
	result := NormalizeForAPI(msgs)
	if len(result) != 2 {
		t.Errorf("expected 2 messages, got %d", len(result))
	}
}
