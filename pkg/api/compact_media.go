package api

import (
	"github.com/cinience/saker/pkg/message"
)

// Base64 minimum length to detect embedded binary data in tool results.
const base64MinLen = 500

// stripMediaContent removes large base64-encoded data from messages before
// sending them to the summary model. This prevents the compaction API call
// from hitting prompt-too-long due to embedded images or binary content.
func stripMediaContent(msgs []message.Message) []message.Message {
	result := make([]message.Message, len(msgs))
	changed := false
	for i, msg := range msgs {
		stripped, didStrip := stripMediaFromMessage(msg)
		if didStrip {
			changed = true
			result[i] = stripped
		} else {
			result[i] = msg
		}
	}
	if !changed {
		return msgs
	}
	return result
}

func stripMediaFromMessage(msg message.Message) (message.Message, bool) {
	changed := false

	// Strip base64 data from tool call results.
	if len(msg.ToolCalls) > 0 {
		newCalls := make([]message.ToolCall, len(msg.ToolCalls))
		for i, tc := range msg.ToolCalls {
			if stripped, ok := stripBase64FromResult(tc.Result); ok {
				changed = true
				newCalls[i] = message.ToolCall{
					ID:        tc.ID,
					Name:      tc.Name,
					Arguments: tc.Arguments,
					Result:    stripped,
				}
			} else {
				newCalls[i] = tc
			}
		}
		if changed {
			clone := message.CloneMessage(msg)
			clone.ToolCalls = newCalls
			return clone, true
		}
	}

	// Strip base64 from content blocks (images/documents).
	if len(msg.ContentBlocks) > 0 {
		newBlocks := make([]message.ContentBlock, len(msg.ContentBlocks))
		for i, block := range msg.ContentBlocks {
			switch block.Type {
			case message.ContentBlockImage:
				changed = true
				newBlocks[i] = message.ContentBlock{Type: message.ContentBlockText, Text: "[image]"}
			case message.ContentBlockDocument:
				changed = true
				newBlocks[i] = message.ContentBlock{Type: message.ContentBlockText, Text: "[document]"}
			default:
				newBlocks[i] = block
			}
		}
		if changed {
			clone := message.CloneMessage(msg)
			clone.ContentBlocks = newBlocks
			return clone, true
		}
	}

	return msg, false
}

// stripBase64FromResult detects and replaces base64-encoded data in a tool
// result string. Returns the cleaned string and whether any change was made.
func stripBase64FromResult(result string) (string, bool) {
	if len(result) < base64MinLen {
		return result, false
	}

	// Heuristic: if the result looks like it's mostly base64, replace it.
	// Check for long runs of base64 characters.
	if looksLikeBase64(result) {
		return "[binary data removed for compaction]", true
	}
	return result, false
}

// looksLikeBase64 returns true if the string appears to be base64-encoded
// data (high ratio of base64 alphabet characters and length > threshold).
func looksLikeBase64(s string) bool {
	if len(s) < base64MinLen {
		return false
	}
	// Sample a portion to avoid scanning huge strings.
	sample := s
	if len(sample) > 2000 {
		sample = sample[:2000]
	}
	b64Chars := 0
	for _, r := range sample {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '+' || r == '/' || r == '=' {
			b64Chars++
		}
	}
	ratio := float64(b64Chars) / float64(len(sample))
	// If > 90% of characters are base64 alphabet and string is long, it's likely binary data.
	return ratio > 0.9 && len(s) > 1000
}
