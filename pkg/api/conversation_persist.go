package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/saker-ai/saker/pkg/conversation"
	"github.com/saker-ai/saker/pkg/message"
)

// conversationPersistTimeout caps a single persistHistory write into the
// conversation store. The whole sequence (ensure thread → open turn → N
// AppendEvents → close turn) must finish inside this budget so a degraded
// DB cannot stall the agent goroutine. Generous because a long Run can
// produce hundreds of events at once.
const conversationPersistTimeout = 10 * time.Second

// cliConversationProjectID is the synthetic project id used for CLI / SDK
// traffic that doesn't carry a real tenant identity. The OpenAI gateway
// path supplies its own projectID via authMiddleware; this one only
// applies when api.Runtime is invoked directly (CLI Run / RunStream /
// subagent).
const cliConversationProjectID = "default"

// cliConversationOwnerUserID labels CLI-originated threads. Used by future
// list / search UIs to distinguish CLI traffic from web/gateway traffic
// without grepping client="cli" everywhere.
const cliConversationOwnerUserID = "cli"

// cliConversationClient identifies the originating surface for the
// conversation row. Mirrors the values used by pkg/server/openai
// (chatPersister uses "openai") so list views can group by client.
const cliConversationClient = "cli"

// persistToConversation writes the new tail of `history` into the
// conversation store. It is a no-op when the store is unset (back-compat),
// when sessionID is blank, or when there are no new messages since the
// last call.
//
// The diff is computed against rt.convCursor[sessionID] — a per-session
// cursor that records "next message index to emit". This means the
// CLI Run / RunStream loop, which calls persistHistory once via defer,
// produces one turn per Run with all messages added during that Run as
// events under it.
//
// Errors are logged and swallowed: persistence is additive and must not
// break the agent loop.
func (rt *Runtime) persistToConversation(sessionID string, history *message.History) {
	if rt == nil || rt.conversationStore == nil || history == nil {
		return
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}

	snapshot := history.All()
	if len(snapshot) == 0 {
		return
	}

	rt.convCursorMu.Lock()
	cursor, threadKnown := rt.convCursor[sessionID]
	rt.convCursorMu.Unlock()

	// History.Replace (compaction / restore) can shrink the snapshot. When
	// that happens, reset the cursor: re-emitting the new short snapshot is
	// the right thing — it represents the model's current state of the
	// world, and a downstream consumer rebuilding from events will get the
	// post-compaction view.
	if cursor > len(snapshot) {
		cursor = 0
	}
	if cursor >= len(snapshot) {
		return
	}
	tail := snapshot[cursor:]

	logger := slog.Default()
	ctx, cancel := context.WithTimeout(context.Background(), conversationPersistTimeout)
	defer cancel()

	if !threadKnown {
		if err := rt.ensureConversationThread(ctx, sessionID, tail); err != nil {
			logger.Error("api: ensure conversation thread failed",
				"session_id", sessionID, "error", err)
			return
		}
	}

	turnID, err := rt.conversationStore.OpenTurn(ctx, sessionID, "")
	if err != nil {
		logger.Error("api: open conversation turn failed",
			"session_id", sessionID, "error", err)
		return
	}

	// Pre-walk the tail to recover tool_call_id ↔ tool_result pairings.
	// message.Message has no ToolCallID field on tool-role messages
	// (unlike OpenAI's wire shape); pkg/conversation projection requires
	// one for every EventKindToolResult or it rejects the insert. We
	// recover by positional matching: each assistant message enqueues
	// its ToolCalls.ID values, each subsequent tool message dequeues
	// the next id. Mirrors how providers stream them.
	toolCallIDs := pairToolResultsToCalls(tail)

	emitted := 0
	for i, msg := range tail {
		if err := rt.appendMessageEvents(ctx, sessionID, turnID, msg, toolCallIDs[i]); err != nil {
			logger.Warn("api: append conversation event failed",
				"session_id", sessionID, "turn_id", turnID, "error", err)
			break
		}
		emitted++
	}

	if err := rt.conversationStore.CloseTurn(ctx, turnID, conversation.TurnStatusCompleted); err != nil {
		logger.Warn("api: close conversation turn failed",
			"session_id", sessionID, "turn_id", turnID, "error", err)
	}

	rt.convCursorMu.Lock()
	rt.convCursor[sessionID] = cursor + emitted
	rt.convCursorMu.Unlock()
}

// ensureConversationThread creates the thread row on first contact for a
// session. Reuses the SDK-supplied sessionID as the thread id so the same
// identifier remains the stable handle across restarts (mirrors the
// gateway's openChatPersister behavior).
//
// The first user message in `tail` seeds the thread title; falls back to
// a generic label when the head is non-text (image-only prompt) or when
// the snapshot starts mid-conversation (resume from history).
func (rt *Runtime) ensureConversationThread(ctx context.Context, sessionID string, tail []message.Message) error {
	_, err := rt.conversationStore.GetThread(ctx, sessionID)
	if err == nil {
		return nil
	}
	if !errors.Is(err, conversation.ErrThreadNotFound) {
		return fmt.Errorf("get thread: %w", err)
	}
	title := conversationTitleFromTail(tail)
	if _, cErr := rt.conversationStore.CreateThreadWithID(
		ctx,
		sessionID,
		cliConversationProjectID,
		cliConversationOwnerUserID,
		title,
		cliConversationClient,
	); cErr != nil {
		return fmt.Errorf("create thread: %w", cErr)
	}
	return nil
}

// appendMessageEvents emits one or more events for a single
// message.Message. Most roles produce one event each; an assistant
// message with both text and tool calls fans out to (assistant_text +
// assistant_tool_call × N) so the projection table can attach tool
// results to the right call later.
//
// toolCallID is non-empty only for "tool" / "function" rows, supplied
// by pairToolResultsToCalls so the P1 projection can link the result
// back to its assistant call.
func (rt *Runtime) appendMessageEvents(ctx context.Context, threadID, turnID string, msg message.Message, toolCallID string) error {
	role := strings.ToLower(strings.TrimSpace(msg.Role))
	switch role {
	case "user":
		_, err := rt.conversationStore.AppendEvent(ctx, conversation.AppendEventInput{
			ThreadID:    threadID,
			ProjectID:   cliConversationProjectID,
			TurnID:      turnID,
			Kind:        conversation.EventKindUserMessage,
			Role:        "user",
			ContentText: msg.Content,
			ContentJSON: contentBlocksJSON(msg.ContentBlocks),
		})
		return err
	case "system", "developer":
		_, err := rt.conversationStore.AppendEvent(ctx, conversation.AppendEventInput{
			ThreadID:    threadID,
			ProjectID:   cliConversationProjectID,
			TurnID:      turnID,
			Kind:        conversation.EventKindSystem,
			Role:        "system",
			ContentText: msg.Content,
		})
		return err
	case "assistant":
		// Emit assistant_text first so the projection orders text-then-tools
		// when both are present (matching how providers stream them).
		if strings.TrimSpace(msg.Content) != "" || len(msg.ContentBlocks) > 0 {
			_, err := rt.conversationStore.AppendEvent(ctx, conversation.AppendEventInput{
				ThreadID:    threadID,
				ProjectID:   cliConversationProjectID,
				TurnID:      turnID,
				Kind:        conversation.EventKindAssistantText,
				Role:        "assistant",
				ContentText: msg.Content,
				ContentJSON: contentBlocksJSON(msg.ContentBlocks),
			})
			if err != nil {
				return err
			}
		}
		for _, tc := range msg.ToolCalls {
			payload := map[string]any{
				"id":   tc.ID,
				"name": tc.Name,
				"args": tc.Arguments,
			}
			if _, err := rt.conversationStore.AppendEvent(ctx, conversation.AppendEventInput{
				ThreadID:     threadID,
				ProjectID:    cliConversationProjectID,
				TurnID:       turnID,
				Kind:         conversation.EventKindAssistantToolCall,
				Role:         "assistant",
				ContentJSON:  payload,
				ToolCallID:   tc.ID,
				ToolCallName: tc.Name,
			}); err != nil {
				return err
			}
		}
		return nil
	case "tool", "function":
		// Without a recoverable tool_call_id (no matching prior assistant
		// ToolCalls entry), the P1 projection rejects EventKindToolResult.
		// Demote to System so we still capture the content rather than
		// breaking the whole turn.
		kind := conversation.EventKindToolResult
		if toolCallID == "" {
			kind = conversation.EventKindSystem
		}
		_, err := rt.conversationStore.AppendEvent(ctx, conversation.AppendEventInput{
			ThreadID:    threadID,
			ProjectID:   cliConversationProjectID,
			TurnID:      turnID,
			Kind:        kind,
			Role:        "tool",
			ContentText: msg.Content,
			ToolCallID:  toolCallID,
		})
		return err
	default:
		// Unknown roles: record loosely so future role values surface as
		// system-tagged events rather than vanishing entirely.
		_, err := rt.conversationStore.AppendEvent(ctx, conversation.AppendEventInput{
			ThreadID:    threadID,
			ProjectID:   cliConversationProjectID,
			TurnID:      turnID,
			Kind:        conversation.EventKindSystem,
			Role:        role,
			ContentText: msg.Content,
		})
		return err
	}
}

// conversationTitleFromTail derives a thread title from the first user
// message in tail. 80-char truncation matches the gateway's
// chatTitleFromRequest convention.
func conversationTitleFromTail(tail []message.Message) string {
	for _, m := range tail {
		if !strings.EqualFold(m.Role, "user") {
			continue
		}
		text := strings.TrimSpace(m.Content)
		if text == "" {
			continue
		}
		if len(text) > 80 {
			text = text[:80]
		}
		return text
	}
	return "CLI session"
}

// pairToolResultsToCalls returns a slice (one entry per message in tail)
// holding the tool_call_id each "tool"/"function" message answers, or ""
// for non-tool messages. It walks the tail in order, enqueuing every
// assistant message's ToolCalls.ID values and dequeueing the next id when
// a tool message lands. Calls with empty IDs are skipped (some legacy
// providers emit untyped tool_calls).
//
// This is positional matching — the same heuristic the OpenAI / Anthropic
// streaming protocols rely on. It breaks if the agent loop reorders tool
// results, but pkg/api never does that.
func pairToolResultsToCalls(tail []message.Message) []string {
	out := make([]string, len(tail))
	var pending []string
	for i, msg := range tail {
		role := strings.ToLower(strings.TrimSpace(msg.Role))
		switch role {
		case "assistant":
			for _, tc := range msg.ToolCalls {
				if tc.ID != "" {
					pending = append(pending, tc.ID)
				}
			}
		case "tool", "function":
			if len(pending) > 0 {
				out[i] = pending[0]
				pending = pending[1:]
			}
		}
	}
	return out
}

// contentBlocksJSON returns the marshalled blocks payload, or nil when
// the message has no structured content. Base64 image data is stripped
// before serialization to prevent SQLite bloat — callers wanting the
// original bytes should store them via the blob CAS (P3).
func contentBlocksJSON(blocks []message.ContentBlock) any {
	if len(blocks) == 0 {
		return nil
	}
	stripped := make([]message.ContentBlock, len(blocks))
	copy(stripped, blocks)
	for i := range stripped {
		if stripped[i].Type == message.ContentBlockImage && stripped[i].Data != "" {
			stripped[i].Data = "[stripped]"
		}
	}
	data, err := json.Marshal(stripped)
	if err != nil {
		return nil
	}
	return json.RawMessage(data)
}
