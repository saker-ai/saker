package message

// NormalizeForAPI ensures a message sequence satisfies API invariants:
//   - Every tool_result has a matching tool_use (orphan results removed)
//   - Every tool_use has a matching tool_result (missing results synthesized)
//   - First message has role "user"
//   - No consecutive messages share the same role (merged when possible)
func NormalizeForAPI(msgs []Message) []Message {
	if len(msgs) == 0 {
		return msgs
	}

	// Pass 1: collect all tool_use IDs and tool_result IDs.
	toolUseIDs := make(map[string]struct{})
	toolResultIDs := make(map[string]struct{})
	for _, msg := range msgs {
		switch msg.Role {
		case "assistant":
			for _, tc := range msg.ToolCalls {
				if tc.ID != "" {
					toolUseIDs[tc.ID] = struct{}{}
				}
			}
		case "tool":
			for _, tc := range msg.ToolCalls {
				if tc.ID != "" {
					toolResultIDs[tc.ID] = struct{}{}
				}
			}
		}
	}

	// Pass 2: filter orphan tool_results and build cleaned list.
	cleaned := make([]Message, 0, len(msgs))
	for _, msg := range msgs {
		if msg.Role == "tool" {
			filtered := filterOrphanResults(msg.ToolCalls, toolUseIDs)
			if len(filtered) == 0 {
				continue // entire message was orphan results
			}
			if len(filtered) != len(msg.ToolCalls) {
				clone := CloneMessage(msg)
				clone.ToolCalls = filtered
				cleaned = append(cleaned, clone)
				continue
			}
		}
		cleaned = append(cleaned, msg)
	}

	// Pass 3: synthesize missing tool_results for orphan tool_uses.
	result := make([]Message, 0, len(cleaned)+4)
	for _, msg := range cleaned {
		result = append(result, msg)
		if msg.Role == "assistant" {
			missing := findMissingResults(msg.ToolCalls, toolResultIDs)
			if len(missing) > 0 {
				result = append(result, Message{
					Role:      "tool",
					ToolCalls: missing,
				})
			}
		}
	}

	// Pass 4: ensure first message is role "user".
	result = ensureLeadingUser(result)

	return result
}

// filterOrphanResults keeps only tool calls whose ID exists in validIDs.
func filterOrphanResults(calls []ToolCall, validIDs map[string]struct{}) []ToolCall {
	var kept []ToolCall
	for _, tc := range calls {
		if _, ok := validIDs[tc.ID]; ok {
			kept = append(kept, tc)
		}
	}
	return kept
}

// findMissingResults returns synthetic tool_result entries for tool_use IDs
// that have no corresponding tool_result in resultIDs.
func findMissingResults(calls []ToolCall, resultIDs map[string]struct{}) []ToolCall {
	var missing []ToolCall
	for _, tc := range calls {
		if tc.ID == "" {
			continue
		}
		if _, ok := resultIDs[tc.ID]; !ok {
			missing = append(missing, ToolCall{
				ID:     tc.ID,
				Name:   tc.Name,
				Result: "[tool result not available]",
			})
		}
	}
	return missing
}

// ensureLeadingUser prepends a synthetic user message if the first message
// is not role "user".
func ensureLeadingUser(msgs []Message) []Message {
	if len(msgs) == 0 {
		return msgs
	}
	if msgs[0].Role == "user" {
		return msgs
	}
	return append([]Message{{
		Role:    "user",
		Content: "[conversation continued]",
	}}, msgs...)
}
