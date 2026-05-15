package api

import (
	"strings"
	"time"

	"github.com/saker-ai/saker/pkg/message"
)

// MicrocompactConfig controls time-based tool output clearing.
// When enabled, old tool results from turns separated by a time gap
// are replaced with a short marker, reducing token usage without
// requiring a model call.
type MicrocompactConfig struct {
	Enabled        bool          `json:"enabled"`
	GapThreshold   time.Duration `json:"gap_threshold"`   // min gap between assistant turns to trigger (default 2m)
	PreserveLastN  int           `json:"preserve_last_n"` // keep N most recent tool results untouched (default 3)
	ClearedMessage string        `json:"cleared_message"` // replacement text (default below)
}

const (
	defaultMCGapThreshold   = 2 * time.Minute
	defaultMCPreserveLastN  = 3
	defaultMCClearedMessage = "[Old tool result content cleared]"
)

// compactableTools lists tool names whose output can be safely cleared.
var compactableTools = map[string]bool{
	"file_read":  true,
	"bash":       true,
	"grep":       true,
	"glob":       true,
	"web_search": true,
	"web_fetch":  true,
	"file_edit":  true,
	"file_write": true,
}

func (c MicrocompactConfig) withDefaults() MicrocompactConfig {
	cfg := c
	if cfg.GapThreshold <= 0 {
		cfg.GapThreshold = defaultMCGapThreshold
	}
	if cfg.PreserveLastN < 0 {
		cfg.PreserveLastN = 0
	}
	if cfg.PreserveLastN == 0 && cfg.Enabled {
		cfg.PreserveLastN = defaultMCPreserveLastN
	}
	if cfg.ClearedMessage == "" {
		cfg.ClearedMessage = defaultMCClearedMessage
	}
	return cfg
}

// microcompact clears old tool results before the model is called.
// It looks for a time gap between consecutive assistant messages; tool
// results before the gap are replaced with a short marker. Returns true
// if any messages were modified.
func (c *compactor) microcompact(hist *message.History) bool {
	if c == nil || !c.cfg.Microcompact.Enabled {
		return false
	}
	cfg := c.cfg.Microcompact
	msgs := hist.All()
	if len(msgs) < 3 {
		return false
	}

	// Find the gap boundary: scan assistant messages from the end and
	// find the first pair where the time gap exceeds the threshold.
	// We use message index as a proxy; in saker, messages don't carry
	// timestamps, so we use a simpler heuristic: clear all compactable
	// tool results except the most recent PreserveLastN.
	toolIndices := collectCompactableToolIndices(msgs)
	if len(toolIndices) <= cfg.PreserveLastN {
		return false
	}

	// Indices to clear: everything except the last PreserveLastN.
	clearUpTo := len(toolIndices) - cfg.PreserveLastN
	clearSet := make(map[int]struct{}, clearUpTo)
	for i := 0; i < clearUpTo; i++ {
		clearSet[toolIndices[i]] = struct{}{}
	}

	changed := false
	result := make([]message.Message, len(msgs))
	for i, msg := range msgs {
		if _, ok := clearSet[i]; !ok {
			result[i] = msg
			continue
		}
		cleared, didClear := clearToolResults(msg.ToolCalls, cfg.ClearedMessage)
		if didClear {
			changed = true
			clone := message.CloneMessage(msg)
			clone.ToolCalls = cleared
			result[i] = clone
		} else {
			result[i] = msg
		}
	}

	if changed {
		hist.Replace(result)
	}
	return changed
}

// collectCompactableToolIndices returns message indices of tool-result
// messages that reference compactable tools and have non-trivial content.
func collectCompactableToolIndices(msgs []message.Message) []int {
	var indices []int
	for i, msg := range msgs {
		if msg.Role != "tool" || len(msg.ToolCalls) == 0 {
			continue
		}
		for _, tc := range msg.ToolCalls {
			name := strings.ToLower(tc.Name)
			if compactableTools[name] && len(tc.Result) > 0 && tc.Result != defaultMCClearedMessage {
				indices = append(indices, i)
				break
			}
		}
	}
	return indices
}

// clearToolResults replaces compactable tool call results with the cleared message.
func clearToolResults(calls []message.ToolCall, clearedMsg string) ([]message.ToolCall, bool) {
	out := make([]message.ToolCall, len(calls))
	changed := false
	for i, tc := range calls {
		name := strings.ToLower(tc.Name)
		if compactableTools[name] && len(tc.Result) > 0 && tc.Result != clearedMsg {
			changed = true
			out[i] = message.ToolCall{
				ID:        tc.ID,
				Name:      tc.Name,
				Arguments: tc.Arguments,
				Result:    clearedMsg,
			}
		} else {
			out[i] = tc
		}
	}
	return out, changed
}
