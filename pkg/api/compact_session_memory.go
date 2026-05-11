package api

import (
	"errors"
	"fmt"
	"strings"

	"github.com/cinience/saker/pkg/message"
)

// sessionMemoryCompact uses the memory store content as a summary, avoiding
// a model API call entirely. It keeps recent messages based on token and
// message count thresholds, similar to Claude Code's sessionMemoryCompact.
func (c *compactor) sessionMemoryCompact(hist *message.History, snapshot []message.Message, tokensBefore int) (compactResult, error) {
	if c.memStore == nil {
		return compactResult{}, errors.New("api: no memory store")
	}
	memCtx, err := c.memStore.BuildContext(25000) // 25KB budget
	if err != nil || strings.TrimSpace(memCtx) == "" {
		return compactResult{}, fmt.Errorf("api: session memory empty or error: %w", err)
	}

	cfg := c.cfg.SessionMemoryCompact
	counter := message.NaiveCounter{}

	// Calculate which messages to keep from the tail, working backwards.
	totalTokens := 0
	textMsgCount := 0
	startIndex := len(snapshot)

	for i := len(snapshot) - 1; i >= 0; i-- {
		msg := snapshot[i]
		msgTokens := counter.Count(msg)

		// Stop expanding if we hit the max cap.
		if totalTokens+msgTokens > cfg.MaxTokensToKeep {
			break
		}

		totalTokens += msgTokens
		if msg.Role == "user" || msg.Role == "assistant" {
			if strings.TrimSpace(msg.Content) != "" {
				textMsgCount++
			}
		}
		startIndex = i

		// Stop if we meet both minimums.
		if totalTokens >= cfg.MinTokensToKeep && textMsgCount >= cfg.MinTextMessages {
			break
		}
	}

	// Adjust startIndex to avoid splitting tool_use/tool_result pairs.
	startIndex = adjustIndexForToolPairs(snapshot, startIndex)

	kept := snapshot[startIndex:]
	if len(kept) == 0 {
		return compactResult{}, errors.New("api: session memory compact would keep no messages")
	}

	summary := fmt.Sprintf("会话记忆摘要（零成本压缩）：\n%s", memCtx)
	newMsgs := make([]message.Message, 0, 1+len(kept))
	newMsgs = append(newMsgs, message.Message{
		Role:    "system",
		Content: summary,
	})
	newMsgs = append(newMsgs, message.CloneMessages(kept)...)
	hist.Replace(newMsgs)

	tokensAfter := hist.TokenCount()
	return compactResult{
		summary:       summary,
		originalMsgs:  len(snapshot),
		preservedMsgs: len(kept),
		tokensBefore:  tokensBefore,
		tokensAfter:   tokensAfter,
	}, nil
}

// adjustIndexForToolPairs moves startIndex backwards to avoid splitting
// a tool_use from its tool_result. If the message at startIndex is a
// tool_result, include the preceding assistant message with the tool_use.
func adjustIndexForToolPairs(msgs []message.Message, startIndex int) int {
	if startIndex <= 0 || startIndex >= len(msgs) {
		return startIndex
	}

	// Collect tool_result IDs in the kept range.
	resultIDs := make(map[string]struct{})
	for i := startIndex; i < len(msgs); i++ {
		if msgs[i].Role == "tool" {
			for _, tc := range msgs[i].ToolCalls {
				if tc.ID != "" {
					resultIDs[tc.ID] = struct{}{}
				}
			}
		}
	}

	// Collect tool_use IDs already in the kept range.
	useIDs := make(map[string]struct{})
	for i := startIndex; i < len(msgs); i++ {
		if msgs[i].Role == "assistant" {
			for _, tc := range msgs[i].ToolCalls {
				if tc.ID != "" {
					useIDs[tc.ID] = struct{}{}
				}
			}
		}
	}

	// Find tool_results that need their tool_use included.
	needed := make(map[string]struct{})
	for id := range resultIDs {
		if _, ok := useIDs[id]; !ok {
			needed[id] = struct{}{}
		}
	}

	// Walk backwards to include messages with matching tool_uses.
	for i := startIndex - 1; i >= 0 && len(needed) > 0; i-- {
		if msgs[i].Role == "assistant" {
			for _, tc := range msgs[i].ToolCalls {
				if _, ok := needed[tc.ID]; ok {
					startIndex = i
					delete(needed, tc.ID)
				}
			}
		}
	}

	return startIndex
}
