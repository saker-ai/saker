package conversation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"gorm.io/gorm"
)

// AppendEvent inserts a single event into the log and returns its
// thread-scoped seq. The seq is monotonic and gap-free per thread:
// concurrent calls on the same thread are serialized by a per-thread
// mutex AND a transaction that re-reads MAX(seq) + 1 inside the same tx,
// so a SELECT-then-INSERT race cannot produce duplicates even on a
// pure-Postgres backend (where SQLite's single-writer guarantee doesn't
// apply).
//
// ProjectID is denormalized onto the event row deliberately — every
// search / list / cleanup query needs the project id, and JOINing through
// threads on a hot path would defeat the entire point of having a flat
// append log.
func (s *Store) AppendEvent(ctx context.Context, in AppendEventInput) (int64, error) {
	if in.ThreadID == "" {
		return 0, errors.New("conversation.AppendEvent: threadID required")
	}
	if in.ProjectID == "" {
		return 0, errors.New("conversation.AppendEvent: projectID required")
	}
	if in.TurnID == "" {
		return 0, errors.New("conversation.AppendEvent: turnID required")
	}
	if in.Kind == "" {
		return 0, errors.New("conversation.AppendEvent: kind required")
	}

	release := s.threadLock(in.ThreadID)
	defer release()

	var seq int64
	err := s.withCtx(ctx).Transaction(func(tx *gorm.DB) error {
		// MAX(seq)+1 inside the tx. SQLite serializes writers so this is
		// race-free; on Postgres the per-thread mutex above plus the
		// UNIQUE(thread_id, seq) constraint catches anything pathological.
		var maxSeq *int64
		if err := tx.Raw(
			"SELECT MAX(seq) FROM events WHERE thread_id = ?",
			in.ThreadID,
		).Scan(&maxSeq).Error; err != nil {
			return fmt.Errorf("read max seq: %w", err)
		}
		if maxSeq == nil {
			seq = 1
		} else {
			seq = *maxSeq + 1
		}

		evt := &Event{
			ThreadID:    in.ThreadID,
			ProjectID:   in.ProjectID,
			TurnID:      in.TurnID,
			Seq:         seq,
			Kind:        string(in.Kind),
			Role:        in.Role,
			ContentText: in.ContentText,
			CreatedAt:   nowUTC(),
		}
		if in.ContentJSON != nil {
			data, err := marshalJSONField(in.ContentJSON)
			if err != nil {
				return fmt.Errorf("marshal content_json: %w", err)
			}
			evt.ContentJSON = data
		}
		if len(in.BlobRefs) > 0 {
			data, err := marshalJSONField(in.BlobRefs)
			if err != nil {
				return fmt.Errorf("marshal blob_refs: %w", err)
			}
			evt.BlobRefs = data
		}

		if err := tx.Create(evt).Error; err != nil {
			return fmt.Errorf("insert event: %w", err)
		}

		// Atomically bump ref_count on every blob this event depends on.
		// Done in the same tx so a crash between the event insert and
		// the ref_count bump cannot leave dangling refs (events row
		// claims a blob, but blobs.ref_count says nobody refs it → GC
		// reclaims it → next read crashes). Strict: missing blobs
		// surface ErrBlobMissingForRef and the whole tx rolls back.
		if len(in.BlobRefs) > 0 {
			if err := incrementBlobRefsTx(tx, in.BlobRefs, evt.CreatedAt); err != nil {
				return fmt.Errorf("ref blobs: %w", err)
			}
		}

		// Bump the thread's updated_at so ListThreads ordering reflects
		// recent activity. Done in the same tx so a crash between the
		// event insert and the bump can't leave the thread looking stale
		// while having fresh events. RowsAffected is intentionally not
		// checked: a soft-deleted thread can still receive events (e.g.
		// in-flight turn cleanup) and we don't want to fail the append.
		if err := tx.Model(&Thread{}).
			Where("id = ?", in.ThreadID).
			Update("updated_at", evt.CreatedAt).Error; err != nil {
			return fmt.Errorf("touch thread: %w", err)
		}

		// Materialize the messages projection in the same tx so callers
		// that AppendEvent → GetMessages immediately see the new row.
		// See projectEventTx for the eager-vs-deferred rationale.
		if err := projectEventTx(tx, evt, in); err != nil {
			return fmt.Errorf("project event: %w", err)
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("conversation.AppendEvent: %w", err)
	}
	return seq, nil
}

// GetEvents returns events for a thread in seq-ascending order. The
// AfterSeq cursor lets a streaming subscriber resume from a known
// position without re-reading the prefix.
func (s *Store) GetEvents(ctx context.Context, threadID string, opts GetEventsOpts) ([]Event, error) {
	if threadID == "" {
		return nil, errors.New("conversation.GetEvents: threadID required")
	}
	q := s.withCtx(ctx).Model(&Event{}).Where("thread_id = ?", threadID)
	if opts.AfterSeq > 0 {
		q = q.Where("seq > ?", opts.AfterSeq)
	}
	if opts.TurnID != "" {
		q = q.Where("turn_id = ?", opts.TurnID)
	}
	q = q.Order("seq ASC").Limit(clampLimit(opts.Limit))

	var out []Event
	if err := q.Find(&out).Error; err != nil {
		return nil, fmt.Errorf("conversation.GetEvents: %w", err)
	}
	return out, nil
}

// marshalJSONField produces a json.RawMessage from caller-supplied data.
// Accepts: nil (→ nil), pre-marshaled json.RawMessage / []byte (→ pass
// through), or any value json.Marshal can handle. Returning RawMessage
// keeps the column round-trip clean: the GORM `serializer:json` tag
// will pass these bytes verbatim through driver.Valuer/Scan since
// RawMessage's MarshalJSON returns the bytes as-is.
func marshalJSONField(v any) (json.RawMessage, error) {
	if v == nil {
		return nil, nil
	}
	switch x := v.(type) {
	case json.RawMessage:
		return x, nil
	case []byte:
		return json.RawMessage(x), nil
	}
	return json.Marshal(v)
}
