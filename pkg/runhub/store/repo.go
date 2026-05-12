package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrNotFound is returned by LoadRun when no row matches the supplied id.
// Callers map this to their own not-found semantics (the gateway translates
// it to HTTP 404).
var ErrNotFound = errors.New("runhub/store: row not found")

// activeStatuses is the set of run statuses considered non-terminal —
// kept here so SweepExpired and LoadActiveRuns share the same vocabulary.
var activeStatuses = []string{"queued", "in_progress", "requires_action", "cancelling"}

// terminalStatuses mirrors activeStatuses for the post-finish retention
// sweep (SweepFinished).
var terminalStatuses = []string{"completed", "cancelled", "failed", "expired"}

// UpsertRun creates or replaces a RunRow keyed by ID. UpdatedAt is bumped
// to time.Now on every call; CreatedAt is preserved if already populated,
// otherwise stamped to UpdatedAt so the column is never zero.
func (s *Store) UpsertRun(ctx context.Context, row RunRow) error {
	now := time.Now()
	row.UpdatedAt = now
	if row.CreatedAt.IsZero() {
		row.CreatedAt = now
	}
	return s.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "id"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"session_id", "tenant_id", "status", "expires_at", "updated_at",
			}),
		}).
		Create(&row).Error
}

// UpdateStatus sets the status column and bumps UpdatedAt. No-op (no
// error) when the row doesn't exist — callers shouldn't depend on
// existence here, since the in-memory hub is the source of truth for
// liveness.
func (s *Store) UpdateStatus(ctx context.Context, runID, status string) error {
	return s.db.WithContext(ctx).
		Model(&RunRow{}).
		Where("id = ?", runID).
		Updates(map[string]any{
			"status":     status,
			"updated_at": time.Now(),
		}).Error
}

// LoadRun returns the RunRow for runID, or ErrNotFound. Used after a
// MemoryHub.Get miss to attempt restart-replay.
func (s *Store) LoadRun(ctx context.Context, runID string) (RunRow, error) {
	var row RunRow
	err := s.db.WithContext(ctx).First(&row, "id = ?", runID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return RunRow{}, ErrNotFound
	}
	return row, err
}

// LoadActiveRuns returns every RunRow in a non-terminal state. Called
// once at PersistentHub Open time so client reconnects after a process
// restart can find their run by id.
func (s *Store) LoadActiveRuns(ctx context.Context) ([]RunRow, error) {
	var rows []RunRow
	err := s.db.WithContext(ctx).
		Where("status IN ?", activeStatuses).
		Order("created_at ASC").
		Find(&rows).Error
	return rows, err
}

// InsertEvent appends one EventRow. The composite PK (RunID, Seq) protects
// against double-write; a conflict is silently ignored so a crash-recovered
// producer that replays the tail of its ring can't error out.
func (s *Store) InsertEvent(ctx context.Context, row EventRow) error {
	if row.Stored.IsZero() {
		row.Stored = time.Now()
	}
	return s.db.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(&row).Error
}

// InsertEventsBatch appends a slice of EventRows in a single multi-row
// INSERT. Same DoNothing-on-conflict semantics as InsertEvent so a
// retried batch is a safe no-op for already-stored seqs. Empty slice is
// a no-op (nil error).
//
// Used by the async batch writer to amortize fsync / network round-trip
// over many events; for SQLite + LAN postgres this typically gives a
// 5–10× throughput improvement over per-event InsertEvent.
//
// Two implementations are routed by driver + batch size:
//
//   - postgres + len(rows) >= Config.PGCopyThreshold: pgx.CopyFrom into
//     a TEMP staging table followed by INSERT … SELECT … ON CONFLICT
//     DO NOTHING. CopyFrom alone can't express the conflict clause, so
//     the staging table preserves the dedup semantics callers rely on.
//   - everything else: handwritten multi-row INSERT (see
//     insertEventsBatchPrepared). With gorm.Config.PrepareStmt=true the
//     driver caches the parsed statement keyed on SQL text, so a
//     steady-state run reusing fixed batch sizes hits a hot path after
//     the first call.
//
// The COPY path returns its own error directly — we don't silently
// fall back to the prepared path on COPY failure because that would
// mask real DB issues operators need to see.
func (s *Store) InsertEventsBatch(ctx context.Context, rows []EventRow) error {
	if len(rows) == 0 {
		return nil
	}
	now := time.Now()
	for i := range rows {
		if rows[i].Stored.IsZero() {
			rows[i].Stored = now
		}
	}
	if handled, err := s.tryCopyInsertEvents(ctx, rows); handled {
		return err
	}
	return s.insertEventsBatchPrepared(ctx, rows)
}

// insertEventsBatchPrepared issues one multi-row INSERT … ON CONFLICT
// DO NOTHING via db.Exec, bypassing GORM struct reflection. The SQL
// text is identical for every batch of the same length, so when
// gorm.Config.PrepareStmt is enabled the underlying driver caches the
// plan and reuses it across calls — which is the entire point of this
// helper.
//
// Dialects covered:
//   - sqlite: ON CONFLICT DO NOTHING is supported since 3.24 (~2018);
//     all builds we ship hit that floor.
//   - postgres: same syntax, identical semantics.
//
// `?` placeholders are normalized to `$N` by gorm/pgx for postgres on
// the way out, so we don't have to dialect-switch the SQL builder.
func (s *Store) insertEventsBatchPrepared(ctx context.Context, rows []EventRow) error {
	const (
		colsPerRow = 5
		header     = "INSERT INTO runhub_events (run_id, seq, type, data, stored) VALUES "
		footer     = " ON CONFLICT DO NOTHING"
		rowTuple   = "(?,?,?,?,?)"
	)

	// Pre-size the SQL buffer: header + N * (rowTuple + ",") + footer.
	// Saves a couple of grow rounds on big batches without paying for an
	// upfront full materialization.
	var sb strings.Builder
	sb.Grow(len(header) + len(footer) + len(rows)*(len(rowTuple)+1))
	sb.WriteString(header)
	for i := range rows {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(rowTuple)
	}
	sb.WriteString(footer)

	// One flat positional-args slice in row-major order. Allocated once,
	// no per-row reflection. Strings + ints are passed by value; data is
	// the original byte slice (no copy needed — the driver consumes it
	// during Exec, which returns before the caller could mutate it).
	args := make([]any, 0, len(rows)*colsPerRow)
	for _, r := range rows {
		args = append(args, r.RunID, r.Seq, r.Type, r.Data, r.Stored)
	}

	return s.db.WithContext(ctx).Exec(sb.String(), args...).Error
}

// LoadEventsSince returns events for runID with Seq strictly greater than
// sinceSeq, oldest → newest. Pass sinceSeq=0 for everything.
func (s *Store) LoadEventsSince(ctx context.Context, runID string, sinceSeq int) ([]EventRow, error) {
	var rows []EventRow
	err := s.db.WithContext(ctx).
		Where("run_id = ? AND seq > ?", runID, sinceSeq).
		Order("seq ASC").
		Find(&rows).Error
	return rows, err
}

// MaxSeq returns the largest Seq stored for runID, or 0 when no events
// exist. Used by the PG cross-process listener to bootstrap its replay
// cursor without a dedicated metadata row.
func (s *Store) MaxSeq(ctx context.Context, runID string) (int, error) {
	var maxSeq *int
	err := s.db.WithContext(ctx).
		Model(&EventRow{}).
		Where("run_id = ?", runID).
		Select("MAX(seq)").
		Scan(&maxSeq).Error
	if err != nil {
		return 0, err
	}
	if maxSeq == nil {
		return 0, nil
	}
	return *maxSeq, nil
}

// DeleteRun removes one RunRow plus all its events in a single
// transaction so a partially-deleted run can never linger.
func (s *Store) DeleteRun(ctx context.Context, runID string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("run_id = ?", runID).Delete(&EventRow{}).Error; err != nil {
			return err
		}
		return tx.Delete(&RunRow{}, "id = ?", runID).Error
	})
}

// SweepExpired flips every active row whose ExpiresAt has passed to
// "expired" and returns the affected ids so the caller can also tear
// down any matching in-memory state. Rows with zero ExpiresAt (no
// deadline) are skipped.
func (s *Store) SweepExpired(ctx context.Context, before time.Time) ([]string, error) {
	var rows []RunRow
	err := s.db.WithContext(ctx).
		Select("id").
		Where("status IN ? AND expires_at != ? AND expires_at < ?",
			activeStatuses, time.Time{}, before).
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		if err := s.UpdateStatus(ctx, r.ID, "expired"); err != nil {
			return ids, err
		}
		ids = append(ids, r.ID)
	}
	return ids, nil
}

// SweepFinished returns terminal RunRow ids whose UpdatedAt is older than
// before — the "ready to delete" set for the GC's retention pass.
func (s *Store) SweepFinished(ctx context.Context, before time.Time) ([]string, error) {
	var rows []RunRow
	err := s.db.WithContext(ctx).
		Select("id").
		Where("status IN ? AND updated_at < ?", terminalStatuses, before).
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	return ids, nil
}
