package conversation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"gorm.io/gorm"
)

// projectEventTx materializes the messages projection from a single
// event inside the same transaction that wrote the event.
//
// Eager (in-tx) projection vs deferred (async worker): chose eager so
// AppendEvent's contract is "after this returns, GetMessages reflects
// the new event." Async workers introduce a window where the event log
// has the row but the projection doesn't — manageable but adds two
// failure modes (worker backlog, projection-miss reads) for no concrete
// throughput win at our event rates. P1 bench measures the in-tx
// overhead at < 100µs per event.
//
// Event-kind dispatch:
//   - user_message → INSERT (role='user', tool_call_id='')
//   - system → INSERT (role='system', tool_call_id='')
//   - assistant_text → UPSERT on (thread_id, turn_id, 'assistant', '')
//     appending content_text to the existing row's content
//   - assistant_tool_call → UPSERT on the same key, appending one entry
//     to the tool_calls JSON array
//   - tool_result → INSERT (role='tool', tool_call_id=in.ToolCallID)
//     attaching this result to the assistant call by id
//   - usage / error → skip (bookkeeping events; not chat messages)
//
// Unrecognized kinds are silently skipped at projection time but still
// recorded in the events log — forward-compat for rolling upgrades.
func projectEventTx(tx *gorm.DB, evt *Event, in AppendEventInput) error {
	switch in.Kind {
	case EventKindUserMessage:
		return insertProjectedMessage(tx, evt, "user", "", evt.ContentText, nil)

	case EventKindSystem:
		return insertProjectedMessage(tx, evt, "system", "", evt.ContentText, nil)

	case EventKindAssistantText:
		return upsertAssistantMessage(tx, evt, evt.ContentText, nil)

	case EventKindAssistantToolCall:
		// Build a tool_call envelope from the event's content_json plus
		// the caller-supplied id and name. arguments is whatever was on
		// content_json — typically the raw JSON arguments string the
		// model emitted.
		var args json.RawMessage
		if len(evt.ContentJSON) > 0 {
			args = evt.ContentJSON
		}
		call := map[string]any{
			"id":        in.ToolCallID,
			"name":      in.ToolCallName,
			"arguments": args,
		}
		return upsertAssistantMessage(tx, evt, "", []map[string]any{call})

	case EventKindToolResult:
		if in.ToolCallID == "" {
			// A tool_result with no ToolCallID can't be attached to any
			// assistant call. Surface at append time rather than letting
			// a UI render an orphan.
			return errors.New("projectEvent: tool_result requires ToolCallID")
		}
		return insertProjectedMessage(tx, evt, "tool", in.ToolCallID, evt.ContentText, nil)

	case EventKindUsage, EventKindError:
		return nil
	}
	return nil
}

// insertProjectedMessage creates a new message row with the next
// thread-scoped pos. CreatedAt and UpdatedAt borrow the event's
// timestamp so the materialized row matches its source-of-truth event
// to the nanosecond — useful for debugging projection drift.
func insertProjectedMessage(
	tx *gorm.DB,
	evt *Event,
	role, toolCallID, content string,
	toolCalls json.RawMessage,
) error {
	pos, err := nextMessagePos(tx, evt.ThreadID)
	if err != nil {
		return err
	}
	msg := &Message{
		ThreadID:   evt.ThreadID,
		ProjectID:  evt.ProjectID,
		TurnID:     evt.TurnID,
		Pos:        pos,
		Role:       role,
		ToolCallID: toolCallID,
		Content:    content,
		ToolCalls:  toolCalls,
		CreatedAt:  evt.CreatedAt,
		UpdatedAt:  evt.CreatedAt,
	}
	if err := tx.Create(msg).Error; err != nil {
		return fmt.Errorf("insert projected message: %w", err)
	}
	return nil
}

// upsertAssistantMessage finds the assistant message for this
// (thread_id, turn_id) — there is at most one because the projection
// key is (thread_id, turn_id, role='assistant', tool_call_id=''). If
// absent, creates it with the appended content/tool_calls. If present,
// appends them to the existing row.
//
// Two paths:
//   - fast path (pure text, no tool_calls): UPDATE ... SET content =
//     content || ? — pushes the concatenation into SQLite, avoiding
//     both the SELECT round-trip and the O(n) Go-side string realloc
//     per chunk. Without this, a 200-chunk streamed message costs
//     O(N²) on the Go heap.
//   - slow path (anything with tool_calls): SELECT-then-UPSERT, since
//     tool_calls is JSON and needs deserialize/append/serialize in Go.
//
// Concurrency: the per-thread mutex on Store serializes AppendEvent
// for a given thread, so no two callers can race the read/write inside
// this function. Single-row-per-(turn,'assistant','') is enforced by
// that mutex + the in-tx pattern below — the schema does NOT have a
// unique constraint on the projection key (see Message struct doc for
// rationale).
func upsertAssistantMessage(
	tx *gorm.DB,
	evt *Event,
	appendContent string,
	appendToolCalls []map[string]any,
) error {
	// Fast path: pure-text streaming append.
	if appendContent != "" && len(appendToolCalls) == 0 {
		result := tx.Exec(
			`UPDATE messages SET content = content || ?, updated_at = ?
			 WHERE thread_id = ? AND turn_id = ? AND role = 'assistant' AND tool_call_id = ''`,
			appendContent, evt.CreatedAt, evt.ThreadID, evt.TurnID,
		)
		if result.Error != nil {
			return fmt.Errorf("append assistant content: %w", result.Error)
		}
		if result.RowsAffected > 0 {
			return nil
		}
		// No existing row — first chunk of the turn. Insert one.
		return insertProjectedMessage(tx, evt, "assistant", "", appendContent, nil)
	}

	// Slow path: read-modify-write (tool_calls needs JSON merge).
	var existing Message
	err := tx.Where(
		"thread_id = ? AND turn_id = ? AND role = ? AND tool_call_id = ?",
		evt.ThreadID, evt.TurnID, "assistant", "",
	).First(&existing).Error

	if errors.Is(err, gorm.ErrRecordNotFound) {
		var toolCallsJSON json.RawMessage
		if len(appendToolCalls) > 0 {
			b, mErr := json.Marshal(appendToolCalls)
			if mErr != nil {
				return fmt.Errorf("marshal tool_calls: %w", mErr)
			}
			toolCallsJSON = b
		}
		return insertProjectedMessage(tx, evt, "assistant", "", appendContent, toolCallsJSON)
	}
	if err != nil {
		return fmt.Errorf("find assistant message: %w", err)
	}

	existing.Content += appendContent
	if len(appendToolCalls) > 0 {
		var current []map[string]any
		if len(existing.ToolCalls) > 0 {
			if uErr := json.Unmarshal(existing.ToolCalls, &current); uErr != nil {
				return fmt.Errorf("unmarshal existing tool_calls: %w", uErr)
			}
		}
		current = append(current, appendToolCalls...)
		b, mErr := json.Marshal(current)
		if mErr != nil {
			return fmt.Errorf("marshal merged tool_calls: %w", mErr)
		}
		existing.ToolCalls = b
	}
	existing.UpdatedAt = evt.CreatedAt

	// Save() rewrites all columns. Pos / CreatedAt / ThreadID / TurnID
	// / Role / ToolCallID weren't touched above, so the rewrite is a
	// no-op for them. The AU trigger on messages will fire and refresh
	// the FTS index for this row's new content.
	if err := tx.Save(&existing).Error; err != nil {
		return fmt.Errorf("update assistant message: %w", err)
	}
	return nil
}

// nextMessagePos returns the next thread-scoped position counter.
// Called inside the AppendEvent transaction; the per-thread mutex on
// Store guarantees no concurrent caller can race past the read.
func nextMessagePos(tx *gorm.DB, threadID string) (int64, error) {
	var maxPos *int64
	if err := tx.Raw(
		"SELECT MAX(pos) FROM messages WHERE thread_id = ?",
		threadID,
	).Scan(&maxPos).Error; err != nil {
		return 0, fmt.Errorf("read max pos: %w", err)
	}
	if maxPos == nil {
		return 1, nil
	}
	return *maxPos + 1, nil
}

// GetMessages returns the materialized message projection for a thread,
// in pos-ascending order. AfterPos lets a paginating caller resume from
// a known cursor (e.g. SSE backfill).
func (s *Store) GetMessages(ctx context.Context, threadID string, opts GetMessagesOpts) ([]Message, error) {
	if threadID == "" {
		return nil, errors.New("conversation.GetMessages: threadID required")
	}
	q := s.withCtx(ctx).Model(&Message{}).Where("thread_id = ?", threadID)
	if opts.AfterPos > 0 {
		q = q.Where("pos > ?", opts.AfterPos)
	}
	q = q.Order("pos ASC").Limit(clampLimit(opts.Limit))

	var out []Message
	if err := q.Find(&out).Error; err != nil {
		return nil, fmt.Errorf("conversation.GetMessages: %w", err)
	}
	return out, nil
}
