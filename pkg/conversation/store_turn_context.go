package conversation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrTurnContextNotFound is returned by GetTurnContext when a thread
// has no persisted snapshot yet — i.e. either nothing has been written
// for any of its turns, or every snapshot was wiped (P3+ may add a GC
// path; P2 itself never deletes).
//
// Callers resuming a thread should treat this as "cold cache" and fall
// back to rebuilding the prompt from the events log, NOT as a hard error.
var ErrTurnContextNotFound = errors.New("conversation: turn context not found")

// PutTurnContext writes (or overwrites) the cache-breakpoint snapshot for
// (threadID, turnID). Multiple writes against the same turn are an UPSERT
// — Snapshot evolves in place across the streaming lifetime of a turn,
// so the gateway can checkpoint after each provider response without
// growing the table by one row per chunk.
//
// metadata is stored as raw JSON via GORM's serializer; passing nil
// writes NULL. Callers that don't have side-channel data can safely
// pass nil here.
//
// The implementation uses GORM's clause.OnConflict with the (thread_id,
// turn_id) unique index to push the upsert into a single SQL round-trip
// — works on SQLite (INSERT ... ON CONFLICT DO UPDATE) and Postgres
// (INSERT ... ON CONFLICT (...) DO UPDATE) without dialect-specific
// branches.
func (s *Store) PutTurnContext(
	ctx context.Context,
	threadID, turnID string,
	snapshot []byte,
	metadata json.RawMessage,
) error {
	if threadID == "" {
		return errors.New("conversation.PutTurnContext: threadID required")
	}
	if turnID == "" {
		return errors.New("conversation.PutTurnContext: turnID required")
	}
	if len(snapshot) == 0 {
		// Empty snapshots round-trip but signal a caller bug — the whole
		// point of a cache breakpoint is the bytes. Refuse rather than
		// store a useless row.
		return errors.New("conversation.PutTurnContext: snapshot required")
	}

	now := nowUTC()
	row := &TurnContext{
		ThreadID:  threadID,
		TurnID:    turnID,
		Snapshot:  snapshot,
		Metadata:  metadata,
		CreatedAt: now,
		UpdatedAt: now,
	}

	// On conflict (same thread_id+turn_id), refresh the payload columns
	// and bump updated_at so GetTurnContext-by-thread-latest still picks
	// the most recently rewritten breakpoint. created_at is intentionally
	// preserved (the column is omitted from DoUpdates) so it reflects
	// when this turn's cache state first appeared, not the latest write.
	err := s.withCtx(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "thread_id"}, {Name: "turn_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"snapshot":   snapshot,
			"metadata":   metadata,
			"updated_at": now,
		}),
	}).Create(row).Error
	if err != nil {
		return fmt.Errorf("conversation.PutTurnContext: %w", err)
	}
	return nil
}

// GetTurnContext returns the most recently written cache breakpoint for
// the thread (max updated_at across all of its turns). The returned
// snapshot is the bytes the caller wrote to PutTurnContext — opaque to
// this package.
//
// Returns ErrTurnContextNotFound (sentinel, check with errors.Is) when
// no row exists for the thread; this is a cold-cache signal, not a hard
// error.
//
// The (thread_id, updated_at DESC) covering index makes this an
// O(log n) point lookup — no full-table scan even on threads with
// thousands of turns.
func (s *Store) GetTurnContext(ctx context.Context, threadID string) (*TurnContext, error) {
	if threadID == "" {
		return nil, errors.New("conversation.GetTurnContext: threadID required")
	}
	var tc TurnContext
	err := s.withCtx(ctx).
		Where("thread_id = ?", threadID).
		Order("updated_at DESC").
		Order("id DESC"). // tiebreak when two writes share a nanosecond
		Take(&tc).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrTurnContextNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("conversation.GetTurnContext: %w", err)
	}
	return &tc, nil
}

// GetTurnContextByTurn returns the breakpoint for a specific (thread,
// turn) pair, or ErrTurnContextNotFound. Useful for debugging "what was
// the cache state when turn X ran?" without filtering through the
// latest-by-thread path.
func (s *Store) GetTurnContextByTurn(ctx context.Context, threadID, turnID string) (*TurnContext, error) {
	if threadID == "" {
		return nil, errors.New("conversation.GetTurnContextByTurn: threadID required")
	}
	if turnID == "" {
		return nil, errors.New("conversation.GetTurnContextByTurn: turnID required")
	}
	var tc TurnContext
	err := s.withCtx(ctx).
		Where("thread_id = ? AND turn_id = ?", threadID, turnID).
		Take(&tc).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrTurnContextNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("conversation.GetTurnContextByTurn: %w", err)
	}
	return &tc, nil
}
