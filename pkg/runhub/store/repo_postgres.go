//go:build postgres

// Postgres COPY-based bulk insert path. Built only with `-tags postgres`
// so the default build keeps pgxpool out of the link graph. CopyFrom is
// the fastest way to push many rows through pgx (single network
// round-trip + binary protocol, no per-row parse), but it doesn't
// support ON CONFLICT — so we COPY into a TEMP table inside a tx and
// drain it via INSERT ... SELECT ... ON CONFLICT DO NOTHING. That
// preserves the dedup semantics the rest of the package relies on
// (retried batches after a producer crash are no-ops on already-stored
// seqs) while still amortizing the network/parse cost over the whole
// batch.
//
// The pgxpool is constructed lazily on first call and held for the life
// of the Store; Store.Close shuts it down via pgxPoolShutdown below.

package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// copyStagingTable is the per-tx TEMP table name. Created with
// ON COMMIT DROP so it disappears with the transaction; concurrent
// callers each get their own tx-scoped namespace, no collision risk.
const copyStagingTable = "runhub_events_stage"

// tryCopyInsertEvents is the postgres-build implementation. Returns
// (handled=true) only when the COPY path was actually attempted —
// callers fall through to the prepared multi-row INSERT when this
// returns (false, nil), which happens whenever the driver isn't pg or
// the batch is below the configured threshold.
func (s *Store) tryCopyInsertEvents(ctx context.Context, rows []EventRow) (bool, error) {
	if s.driver != "postgres" || s.pgCopyThreshold <= 0 || len(rows) < s.pgCopyThreshold {
		return false, nil
	}
	pool, err := s.ensurePgxPool(ctx)
	if err != nil {
		return true, fmt.Errorf("runhub/store: ensure pgxpool: %w", err)
	}
	if err := copyInsertEvents(ctx, pool, rows); err != nil {
		return true, fmt.Errorf("runhub/store: copy insert events: %w", err)
	}
	return true, nil
}

// copyInsertEvents executes the COPY → SELECT-INSERT round trip inside
// a single transaction. The TEMP table is created on the same conn the
// COPY runs on (CREATE TEMP TABLE ... ON COMMIT DROP), so the staging
// rows are visible to the follow-up INSERT and gone after Commit.
//
// Failure modes the caller cares about:
//   - tx Begin / staging Create: surfaced as-is so operators see the
//     real reason the COPY path failed (won't fall back silently)
//   - CopyFrom: same — a network blip mid-COPY shouldn't be hidden by a
//     retry on the slow path
//   - INSERT...SELECT: this is where dedup actually happens; ON CONFLICT
//     DO NOTHING means "rows already in runhub_events stay put" so a
//     replay-after-crash is still a no-op
func copyInsertEvents(ctx context.Context, pool *pgxpool.Pool, rows []EventRow) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	// Defer-rollback is a no-op when Commit succeeded; only rolls back
	// when we returned early before reaching Commit.
	defer func() { _ = tx.Rollback(ctx) }()

	// Staging table mirrors runhub_events column types/order. ON COMMIT
	// DROP cleans up automatically; no leak even if the caller's ctx
	// times out mid-INSERT.
	createSQL := fmt.Sprintf(
		`CREATE TEMP TABLE %s (run_id text, seq bigint, type text, data bytea, stored timestamptz) ON COMMIT DROP`,
		copyStagingTable,
	)
	if _, err := tx.Exec(ctx, createSQL); err != nil {
		return fmt.Errorf("create staging table: %w", err)
	}

	// CopyFrom feeds the staging table from the in-memory row slice
	// using a row-source closure. Allocations: one EventRow → []any per
	// row inside CopyFrom — pgx itself batches these into binary frames.
	rowSource := pgx.CopyFromSlice(len(rows), func(i int) ([]any, error) {
		r := rows[i]
		return []any{r.RunID, r.Seq, r.Type, r.Data, r.Stored}, nil
	})
	if _, err := tx.CopyFrom(
		ctx,
		pgx.Identifier{copyStagingTable},
		[]string{"run_id", "seq", "type", "data", "stored"},
		rowSource,
	); err != nil {
		return fmt.Errorf("copy from: %w", err)
	}

	// Drain the staging table into the real table with ON CONFLICT DO
	// NOTHING — that's where dedup happens (composite PK is run_id+seq).
	insertSQL := fmt.Sprintf(
		`INSERT INTO runhub_events (run_id, seq, type, data, stored)
		 SELECT run_id, seq, type, data, stored FROM %s
		 ON CONFLICT DO NOTHING`,
		copyStagingTable,
	)
	if _, err := tx.Exec(ctx, insertSQL); err != nil {
		return fmt.Errorf("drain staging: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// ensurePgxPool returns the Store's *pgxpool.Pool, lazily constructing
// it on first call. The pool is independent of the gorm-managed
// connection pool because gorm wraps connections in its own
// reflection-driven prepared statement cache that conflicts with the
// raw COPY protocol pgx exposes. Returns an error only when the driver
// isn't postgres (defensive — the caller should already have driver-
// checked) or when pool construction itself fails.
func (s *Store) ensurePgxPool(ctx context.Context) (*pgxpool.Pool, error) {
	if s.driver != "postgres" {
		return nil, fmt.Errorf("runhub/store: pgxpool requires postgres driver, got %s", s.driver)
	}
	s.pgxMu.Lock()
	defer s.pgxMu.Unlock()
	if s.pgxState != nil {
		if existing, ok := s.pgxState.(*pgxpool.Pool); ok {
			return existing, nil
		}
	}
	cfg, err := pgxpool.ParseConfig(s.dsn)
	if err != nil {
		return nil, fmt.Errorf("parse pgxpool config: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("new pgxpool: %w", err)
	}
	s.pgxState = pool
	return pool, nil
}

// pgxPoolShutdown closes the pool if one was constructed. Called by
// Store.Close before the gorm pool teardown so any in-flight COPY tx
// has already aborted by the time Close returns.
func (s *Store) pgxPoolShutdown() {
	s.pgxMu.Lock()
	state := s.pgxState
	s.pgxState = nil
	s.pgxMu.Unlock()
	if pool, ok := state.(*pgxpool.Pool); ok && pool != nil {
		pool.Close()
	}
}

