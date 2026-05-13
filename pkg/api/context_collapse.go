package api

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cinience/saker/pkg/message"
)

// CollapseConfig controls tool output folding to save tokens.
// Large tool outputs from older turns are replaced with short summaries,
// reducing context usage before a full compaction is needed.
type CollapseConfig struct {
	Enabled          bool `json:"enabled"`
	MaxToolOutputLen int  `json:"max_tool_output_len"` // max chars per tool output (default 10000)
	PreserveLastN    int  `json:"preserve_last_n"`     // skip the N most recent tool results (default 2)
	SummaryMaxLen    int  `json:"summary_max_len"`     // max length of collapsed summary (default 200)
}

const (
	defaultMaxToolOutputLen = 10000
	defaultPreserveLastN    = 2
	defaultSummaryMaxLen    = 200
	collapsedTailLines      = 5
)

func (c CollapseConfig) withDefaults() CollapseConfig {
	cfg := c
	if cfg.MaxToolOutputLen <= 0 {
		cfg.MaxToolOutputLen = defaultMaxToolOutputLen
	}
	if cfg.PreserveLastN < 0 {
		cfg.PreserveLastN = 0
	}
	if cfg.PreserveLastN == 0 && cfg.Enabled {
		cfg.PreserveLastN = defaultPreserveLastN
	}
	if cfg.SummaryMaxLen <= 0 {
		cfg.SummaryMaxLen = defaultSummaryMaxLen
	}
	return cfg
}

// collapseToolOutputs folds large tool outputs in older messages to save tokens.
// It skips the most recent PreserveLastN tool results and replaces oversized
// outputs with brief summaries. The returned slice is a new copy.
func (c *compactor) collapseToolOutputs(msgs []message.Message) []message.Message {
	if c == nil || !c.cfg.Collapse.Enabled || len(msgs) == 0 {
		return msgs
	}
	cfg := c.cfg.Collapse

	// Count tool result messages from the end to find the preserve boundary.
	toolResultIndices := findToolResultIndices(msgs)
	preserveFrom := len(toolResultIndices) - cfg.PreserveLastN
	if preserveFrom < 0 {
		preserveFrom = 0
	}
	// Build set of indices that should be preserved (most recent N tool results).
	preserveSet := make(map[int]struct{}, cfg.PreserveLastN)
	for i := preserveFrom; i < len(toolResultIndices); i++ {
		preserveSet[toolResultIndices[i]] = struct{}{}
	}

	result := make([]message.Message, len(msgs))
	changed := false
	for i, msg := range msgs {
		if msg.Role != "tool" || len(msg.ToolCalls) == 0 {
			result[i] = msg
			continue
		}
		if _, preserve := preserveSet[i]; preserve {
			result[i] = msg
			continue
		}
		collapsed, didCollapse := collapseToolCalls(msg.ToolCalls, cfg)
		if didCollapse {
			changed = true
			clone := message.CloneMessage(msg)
			clone.ToolCalls = collapsed
			result[i] = clone
		} else {
			result[i] = msg
		}
	}
	if !changed {
		return msgs
	}
	return result
}

// findToolResultIndices returns indices of tool-result messages in order.
func findToolResultIndices(msgs []message.Message) []int {
	var indices []int
	for i, msg := range msgs {
		if msg.Role == "tool" && len(msg.ToolCalls) > 0 {
			indices = append(indices, i)
		}
	}
	return indices
}

// collapseToolCalls replaces oversized tool call results with summaries.
func collapseToolCalls(calls []message.ToolCall, cfg CollapseConfig) ([]message.ToolCall, bool) {
	out := make([]message.ToolCall, len(calls))
	changed := false
	for i, call := range calls {
		if len(call.Result) <= cfg.MaxToolOutputLen {
			out[i] = call
			continue
		}
		changed = true
		out[i] = message.ToolCall{
			ID:        call.ID,
			Name:      call.Name,
			Arguments: call.Arguments,
			Result:    collapseOutput(call.Name, call.Result, cfg.SummaryMaxLen),
		}
	}
	return out, changed
}

// collapseOutput produces a tool-specific summary for an oversized output.
func collapseOutput(toolName, output string, maxLen int) string {
	lines := strings.Split(output, "\n")
	lineCount := len(lines)
	name := strings.ToLower(toolName)

	var summary string
	switch {
	case strings.Contains(name, "read"):
		summary = fmt.Sprintf("[read: %d lines collapsed]", lineCount)

	case strings.Contains(name, "bash"):
		tail := lastNLines(lines, collapsedTailLines)
		summary = fmt.Sprintf("[bash output: %d lines total]\n…\n%s", lineCount, tail)

	case strings.Contains(name, "grep"):
		nonEmpty := countNonEmpty(lines)
		summary = fmt.Sprintf("[grep: %d matches collapsed]", nonEmpty)

	case strings.Contains(name, "glob"):
		nonEmpty := countNonEmpty(lines)
		summary = fmt.Sprintf("[glob: %d files collapsed]", nonEmpty)

	case strings.Contains(name, "write") || strings.Contains(name, "edit"):
		summary = fmt.Sprintf("[%s: %d lines collapsed]", toolName, lineCount)

	default:
		summary = fmt.Sprintf("[Tool output collapsed: %d lines, %d chars]", lineCount, len(output))
	}

	if len(summary) > maxLen {
		summary = summary[:maxLen-1] + "…"
	}
	return summary
}

// lastNLines returns the last n non-empty lines joined.
func lastNLines(lines []string, n int) string {
	var result []string
	for i := len(lines) - 1; i >= 0 && len(result) < n; i-- {
		if s := strings.TrimSpace(lines[i]); s != "" {
			result = append([]string{s}, result...)
		}
	}
	return strings.Join(result, "\n")
}

func countNonEmpty(lines []string) int {
	count := 0
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			count++
		}
	}
	return count
}

// extractFilePath tries to extract a file_path from tool call arguments JSON.
func extractFilePath(args map[string]any) string {
	if args == nil {
		return ""
	}
	if fp, ok := args["file_path"].(string); ok {
		return fp
	}
	// Try JSON string arguments.
	if raw, ok := args["arguments"].(string); ok {
		var m map[string]any
		if json.Unmarshal([]byte(raw), &m) == nil {
			if fp, ok := m["file_path"].(string); ok {
				return fp
			}
		}
	}
	return ""
}
