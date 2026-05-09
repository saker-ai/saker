package api

import (
	"strings"
	"testing"

	"github.com/cinience/saker/pkg/message"
)

func TestCollapseConfig_withDefaults(t *testing.T) {
	cfg := CollapseConfig{Enabled: true}.withDefaults()
	if cfg.MaxToolOutputLen != defaultMaxToolOutputLen {
		t.Errorf("MaxToolOutputLen = %d, want %d", cfg.MaxToolOutputLen, defaultMaxToolOutputLen)
	}
	if cfg.PreserveLastN != defaultPreserveLastN {
		t.Errorf("PreserveLastN = %d, want %d", cfg.PreserveLastN, defaultPreserveLastN)
	}
	if cfg.SummaryMaxLen != defaultSummaryMaxLen {
		t.Errorf("SummaryMaxLen = %d, want %d", cfg.SummaryMaxLen, defaultSummaryMaxLen)
	}
}

func TestCollapseToolOutputs_disabled(t *testing.T) {
	c := &compactor{cfg: CompactConfig{Collapse: CollapseConfig{Enabled: false}}}
	msgs := []message.Message{{Role: "tool", ToolCalls: []message.ToolCall{{Result: strings.Repeat("x", 20000)}}}}
	result := c.collapseToolOutputs(msgs)
	if len(result) != 1 || result[0].ToolCalls[0].Result != msgs[0].ToolCalls[0].Result {
		t.Error("disabled collapse should return messages unchanged")
	}
}

func TestCollapseToolOutputs_preservesRecent(t *testing.T) {
	c := &compactor{cfg: CompactConfig{
		Collapse: CollapseConfig{
			Enabled:          true,
			MaxToolOutputLen: 100,
			PreserveLastN:    1,
			SummaryMaxLen:    200,
		},
	}}

	bigOutput := strings.Repeat("line\n", 500) // > 100 chars
	msgs := []message.Message{
		{Role: "user", Content: "hello"},
		{Role: "tool", ToolCalls: []message.ToolCall{{Name: "bash", Result: bigOutput}}}, // old, should collapse
		{Role: "tool", ToolCalls: []message.ToolCall{{Name: "bash", Result: bigOutput}}}, // recent, should preserve
	}

	result := c.collapseToolOutputs(msgs)
	// First tool (old) should be collapsed.
	if result[1].ToolCalls[0].Result == bigOutput {
		t.Error("old tool output should be collapsed")
	}
	if !strings.Contains(result[1].ToolCalls[0].Result, "collapsed") &&
		!strings.Contains(result[1].ToolCalls[0].Result, "Bash") {
		t.Errorf("collapsed output should contain marker, got: %s", result[1].ToolCalls[0].Result)
	}
	// Last tool (recent) should be preserved.
	if result[2].ToolCalls[0].Result != bigOutput {
		t.Error("recent tool output should be preserved")
	}
}

func TestCollapseToolOutputs_smallOutputsUnchanged(t *testing.T) {
	c := &compactor{cfg: CompactConfig{
		Collapse: CollapseConfig{
			Enabled:          true,
			MaxToolOutputLen: 10000,
			PreserveLastN:    0,
			SummaryMaxLen:    200,
		},
	}}

	msgs := []message.Message{
		{Role: "tool", ToolCalls: []message.ToolCall{{Name: "bash", Result: "small output"}}},
	}
	result := c.collapseToolOutputs(msgs)
	if result[0].ToolCalls[0].Result != "small output" {
		t.Error("small output should not be collapsed")
	}
}

func TestCollapseOutput_toolSpecific(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		contains string
	}{
		{"read", "file_read", "Read"},
		{"bash", "bash", "Bash"},
		{"grep", "grep", "Grep"},
		{"glob", "glob", "Glob"},
		{"write", "file_write", "file_write"},
		{"unknown", "custom_tool", "collapsed"},
	}
	bigOutput := strings.Repeat("line\n", 100)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := collapseOutput(tt.toolName, bigOutput, 200)
			if !strings.Contains(result, tt.contains) {
				t.Errorf("collapseOutput(%s) = %q, want to contain %q", tt.toolName, result, tt.contains)
			}
		})
	}
}

func TestCollapseOutput_respectsMaxLen(t *testing.T) {
	bigOutput := strings.Repeat("line\n", 1000)
	result := collapseOutput("custom", bigOutput, 50)
	if len(result) > 50 {
		t.Errorf("collapsed output length %d exceeds maxLen 50", len(result))
	}
}

func TestFindToolResultIndices(t *testing.T) {
	msgs := []message.Message{
		{Role: "user", Content: "hi"},
		{Role: "tool", ToolCalls: []message.ToolCall{{Name: "bash"}}},
		{Role: "assistant", Content: "ok"},
		{Role: "tool", ToolCalls: []message.ToolCall{{Name: "read"}}},
	}
	indices := findToolResultIndices(msgs)
	if len(indices) != 2 || indices[0] != 1 || indices[1] != 3 {
		t.Errorf("findToolResultIndices = %v, want [1, 3]", indices)
	}
}
