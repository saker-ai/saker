package api

import (
	"testing"

	"github.com/cinience/saker/pkg/message"
)

func TestMicrocompact_nilCompactor(t *testing.T) {
	var c *compactor
	if c.microcompact(nil) {
		t.Error("nil compactor should return false")
	}
}

func TestMicrocompact_disabled(t *testing.T) {
	c := &compactor{cfg: CompactConfig{
		Microcompact: MicrocompactConfig{Enabled: false},
	}}
	if c.microcompact(nil) {
		t.Error("disabled should return false")
	}
}

func TestMicrocompact_fewMessages(t *testing.T) {
	c := &compactor{cfg: CompactConfig{
		Microcompact: MicrocompactConfig{Enabled: true}.withDefaults(),
	}}
	hist := message.NewHistory()
	hist.Append(message.Message{Role: "user", Content: "hi"})
	if c.microcompact(hist) {
		t.Error("too few messages should return false")
	}
}

func TestMicrocompact_clearsOldToolResults(t *testing.T) {
	cfg := CompactConfig{
		Microcompact: MicrocompactConfig{
			Enabled:       true,
			PreserveLastN: 1,
		}.withDefaults(),
	}
	c := &compactor{cfg: cfg}

	hist := message.NewHistory()
	hist.Append(message.Message{Role: "user", Content: "hello"})
	hist.Append(message.Message{Role: "assistant", ToolCalls: []message.ToolCall{
		{ID: "tc1", Name: "bash"},
	}})
	hist.Append(message.Message{Role: "tool", ToolCalls: []message.ToolCall{
		{ID: "tc1", Name: "bash", Result: "old output that is large"},
	}})
	hist.Append(message.Message{Role: "assistant", ToolCalls: []message.ToolCall{
		{ID: "tc2", Name: "file_read"},
	}})
	hist.Append(message.Message{Role: "tool", ToolCalls: []message.ToolCall{
		{ID: "tc2", Name: "file_read", Result: "recent file content"},
	}})
	hist.Append(message.Message{Role: "assistant", Content: "done"})

	changed := c.microcompact(hist)
	if !changed {
		t.Fatal("expected microcompact to make changes")
	}

	msgs := hist.All()
	// tc1 (older) should be cleared
	for _, msg := range msgs {
		if msg.Role == "tool" {
			for _, tc := range msg.ToolCalls {
				if tc.ID == "tc1" && tc.Result != defaultMCClearedMessage {
					t.Errorf("tc1 should be cleared, got: %s", tc.Result)
				}
			}
		}
	}
	// tc2 (recent, preserved) should keep its content
	for _, msg := range msgs {
		if msg.Role == "tool" {
			for _, tc := range msg.ToolCalls {
				if tc.ID == "tc2" && tc.Result == defaultMCClearedMessage {
					t.Error("tc2 should be preserved")
				}
			}
		}
	}
}

func TestMicrocompact_preservesNonCompactable(t *testing.T) {
	cfg := CompactConfig{
		Microcompact: MicrocompactConfig{
			Enabled:       true,
			PreserveLastN: 0,
		}.withDefaults(),
	}
	c := &compactor{cfg: cfg}

	hist := message.NewHistory()
	hist.Append(message.Message{Role: "user", Content: "hello"})
	hist.Append(message.Message{Role: "assistant", ToolCalls: []message.ToolCall{
		{ID: "tc1", Name: "custom_tool"},
	}})
	hist.Append(message.Message{Role: "tool", ToolCalls: []message.ToolCall{
		{ID: "tc1", Name: "custom_tool", Result: "custom result"},
	}})
	hist.Append(message.Message{Role: "assistant", Content: "done"})

	changed := c.microcompact(hist)
	if changed {
		t.Error("non-compactable tools should not be cleared")
	}
}

func TestMicrocompact_alreadyCleared(t *testing.T) {
	cfg := CompactConfig{
		Microcompact: MicrocompactConfig{
			Enabled:       true,
			PreserveLastN: 0,
		}.withDefaults(),
	}
	c := &compactor{cfg: cfg}

	hist := message.NewHistory()
	hist.Append(message.Message{Role: "user", Content: "hello"})
	hist.Append(message.Message{Role: "assistant", ToolCalls: []message.ToolCall{
		{ID: "tc1", Name: "bash"},
	}})
	hist.Append(message.Message{Role: "tool", ToolCalls: []message.ToolCall{
		{ID: "tc1", Name: "bash", Result: defaultMCClearedMessage},
	}})
	hist.Append(message.Message{Role: "assistant", Content: "done"})

	changed := c.microcompact(hist)
	if changed {
		t.Error("already cleared results should not trigger change")
	}
}

func TestCollectCompactableToolIndices(t *testing.T) {
	msgs := []message.Message{
		{Role: "user", Content: "hi"},
		{Role: "tool", ToolCalls: []message.ToolCall{
			{ID: "1", Name: "bash", Result: "output"},
		}},
		{Role: "tool", ToolCalls: []message.ToolCall{
			{ID: "2", Name: "custom", Result: "output"},
		}},
		{Role: "tool", ToolCalls: []message.ToolCall{
			{ID: "3", Name: "grep", Result: "matches"},
		}},
	}
	indices := collectCompactableToolIndices(msgs)
	if len(indices) != 2 {
		t.Errorf("expected 2 compactable indices, got %d", len(indices))
	}
	if indices[0] != 1 || indices[1] != 3 {
		t.Errorf("expected indices [1, 3], got %v", indices)
	}
}

func TestMicrocompactConfig_withDefaults(t *testing.T) {
	cfg := MicrocompactConfig{Enabled: true}.withDefaults()
	if cfg.GapThreshold != defaultMCGapThreshold {
		t.Errorf("GapThreshold = %v", cfg.GapThreshold)
	}
	if cfg.PreserveLastN != defaultMCPreserveLastN {
		t.Errorf("PreserveLastN = %d", cfg.PreserveLastN)
	}
	if cfg.ClearedMessage != defaultMCClearedMessage {
		t.Errorf("ClearedMessage = %q", cfg.ClearedMessage)
	}
}
