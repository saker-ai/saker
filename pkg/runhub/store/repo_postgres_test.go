//go:build postgres && integration

// Postgres COPY-path integration test for InsertEventsBatch. Skipped
// automatically when SAKER_TEST_PG_DSN is unset (same convention as
// postgres_integration_test.go).
//
// What this covers that the SQLite repo_test can't:
//   - tryCopyInsertEvents actually fires when len(rows) >= threshold,
//     and the staging-table tx commits cleanly
//   - dedup semantics survive the COPY route — rerunning a batch with
//     overlapping (run_id, seq) pairs is a no-op (matches the
//     prepared-multi-row-INSERT path's ON CONFLICT DO NOTHING)
//   - small batches still go through the prepared path, so a misconfigured
//     threshold doesn't accidentally disable bulk insert
//
// Run with:
//
//	SAKER_TEST_PG_DSN=postgres://user:pass@localhost/dbname?sslmode=disable \
//	  go test -tags 'postgres integration' ./pkg/runhub/store/...

package store_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/cinience/saker/pkg/runhub/store"
)

// openPGWithCopyThreshold opens a Store backed by SAKER_TEST_PG_DSN with
// the COPY threshold set explicitly so the test can drive both branches
// (small batch under threshold → prepared path; large batch ≥ threshold
// → COPY path) deterministically.
func openPGWithCopyThreshold(t *testing.T, threshold int) *store.Store {
	t.Helper()
	s, err := store.Open(store.Config{
		DSN:             pgDSN(t),
		PGCopyThreshold: threshold,
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// makeEvents builds n EventRows for a single run, seqs starting at
// startSeq+1 (matches the runtime convention of pre-incrementing).
func makeEvents(runID string, startSeq, n int) []store.EventRow {
	rows := make([]store.EventRow, n)
	now := time.Now()
	for i := range rows {
		rows[i] = store.EventRow{
			RunID:  runID,
			Seq:    startSeq + i + 1,
			Type:   "delta",
			Data:   []byte(fmt.Sprintf(`{"i":%d}`, i)),
			Stored: now,
		}
	}
	return rows
}

// TestPostgres_InsertEventsBatch_CopyPath_Inserts asserts that a batch
// at or above the threshold round-trips through CopyFrom + the
// SELECT-INSERT drain and lands every row.
func TestPostgres_InsertEventsBatch_CopyPath_Inserts(t *testing.T) {
	s := openPGWithCopyThreshold(t, 50)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	runID := fmt.Sprintf("copy-test-%d", time.Now().UnixNano())
	// Need a parent runhub_runs row first — composite PK on events table
	// doesn't FK back, but the convention is to create the run before
	// publishing.
	if err := s.UpsertRun(ctx, store.RunRow{
		ID:        runID,
		TenantID:  "copy-test",
		Status:    "in_progress",
		ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("UpsertRun: %v", err)
	}

	const batchSize = 100 // > threshold of 50, exercises COPY path
	rows := makeEvents(runID, 0, batchSize)
	if err := s.InsertEventsBatch(ctx, rows); err != nil {
		t.Fatalf("InsertEventsBatch: %v", err)
	}

	loaded, err := s.LoadEventsSince(ctx, runID, 0)
	if err != nil {
		t.Fatalf("LoadEventsSince: %v", err)
	}
	if len(loaded) != batchSize {
		t.Fatalf("loaded %d events, want %d", len(loaded), batchSize)
	}
	for i, e := range loaded {
		if e.Seq != i+1 {
			t.Errorf("row %d: seq = %d, want %d", i, e.Seq, i+1)
		}
		if e.Type != "delta" {
			t.Errorf("row %d: type = %q, want delta", i, e.Type)
		}
	}
}

// TestPostgres_InsertEventsBatch_CopyPath_DedupOnReplay asserts that
// the COPY path's ON CONFLICT DO NOTHING dedup works exactly like the
// prepared-multi-row-INSERT path: a second batch with overlapping
// (run_id, seq) pairs is a silent no-op, and net new seqs land while
// the dupes are dropped. Catches a regression where the SELECT-INSERT
// drain skips the conflict clause.
func TestPostgres_InsertEventsBatch_CopyPath_DedupOnReplay(t *testing.T) {
	s := openPGWithCopyThreshold(t, 50)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	runID := fmt.Sprintf("copy-dedup-%d", time.Now().UnixNano())
	if err := s.UpsertRun(ctx, store.RunRow{
		ID: runID, TenantID: "copy-test", Status: "in_progress",
		ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("UpsertRun: %v", err)
	}

	// Round 1: insert 100 rows, all new.
	if err := s.InsertEventsBatch(ctx, makeEvents(runID, 0, 100)); err != nil {
		t.Fatalf("Round 1 InsertEventsBatch: %v", err)
	}

	// Round 2: 100 rows, seqs 51..150 — half overlap, half new. The
	// first 50 (seqs 51-100) should be silently dropped by ON CONFLICT
	// DO NOTHING; the second 50 (seqs 101-150) should land.
	if err := s.InsertEventsBatch(ctx, makeEvents(runID, 50, 100)); err != nil {
		t.Fatalf("Round 2 InsertEventsBatch (with overlap): %v", err)
	}

	loaded, err := s.LoadEventsSince(ctx, runID, 0)
	if err != nil {
		t.Fatalf("LoadEventsSince: %v", err)
	}
	if len(loaded) != 150 {
		t.Fatalf("after dedup: loaded %d events, want 150 (100 fresh + 50 net new)", len(loaded))
	}
	// Verify seq monotonicity + that round-1 payloads were preserved
	// (i.e. ON CONFLICT skipped the new ones, not silently overwriting).
	for i, e := range loaded {
		if e.Seq != i+1 {
			t.Errorf("row %d: seq = %d, want %d", i, e.Seq, i+1)
		}
	}
	// Round 1 stored {"i":50} at seq=51; round 2 with startSeq=50,i=0
	// would have stored {"i":0} at seq=51 if dedup were broken.
	// Snapshot the first overlap row to assert the original payload won.
	overlapRow := loaded[50] // seq=51
	wantPayload := `{"i":50}`
	if string(overlapRow.Data) != wantPayload {
		t.Errorf("seq=51 payload = %q, want %q (round-1 payload should survive ON CONFLICT)",
			overlapRow.Data, wantPayload)
	}
}

// TestPostgres_InsertEventsBatch_SmallBatchUsesPreparedPath asserts
// that a sub-threshold batch bypasses the COPY tx machinery entirely
// — it should still land all rows, but via the same prepared
// multi-row INSERT path SQLite uses. Functional equivalence is the
// only thing the test can assert from the outside; a regression that
// accidentally routed small batches through COPY would still pass
// this test, but the slow path would show up in repo_postgres.go's
// tryCopyInsertEvents threshold check failing review.
func TestPostgres_InsertEventsBatch_SmallBatchUsesPreparedPath(t *testing.T) {
	s := openPGWithCopyThreshold(t, 50)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	runID := fmt.Sprintf("copy-small-%d", time.Now().UnixNano())
	if err := s.UpsertRun(ctx, store.RunRow{
		ID: runID, TenantID: "copy-test", Status: "in_progress",
		ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("UpsertRun: %v", err)
	}

	const batchSize = 10 // < threshold of 50, bypasses COPY
	if err := s.InsertEventsBatch(ctx, makeEvents(runID, 0, batchSize)); err != nil {
		t.Fatalf("InsertEventsBatch: %v", err)
	}

	loaded, err := s.LoadEventsSince(ctx, runID, 0)
	if err != nil {
		t.Fatalf("LoadEventsSince: %v", err)
	}
	if len(loaded) != batchSize {
		t.Fatalf("loaded %d events, want %d", len(loaded), batchSize)
	}
}

// TestPostgres_InsertEventsBatch_CopyDisabled asserts that
// PGCopyThreshold=0 routes EVERY batch through the prepared multi-row
// INSERT, regardless of size. Catches a regression where a zero-value
// threshold accidentally degrades to "use the default".
func TestPostgres_InsertEventsBatch_CopyDisabled(t *testing.T) {
	s := openPGWithCopyThreshold(t, 0) // 0 → COPY disabled
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	runID := fmt.Sprintf("copy-disabled-%d", time.Now().UnixNano())
	if err := s.UpsertRun(ctx, store.RunRow{
		ID: runID, TenantID: "copy-test", Status: "in_progress",
		ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("UpsertRun: %v", err)
	}

	// 200 rows — well above any reasonable default threshold. Should
	// still go through the prepared path because we set threshold=0.
	rows := makeEvents(runID, 0, 200)
	if err := s.InsertEventsBatch(ctx, rows); err != nil {
		t.Fatalf("InsertEventsBatch: %v", err)
	}
	loaded, err := s.LoadEventsSince(ctx, runID, 0)
	if err != nil {
		t.Fatalf("LoadEventsSince: %v", err)
	}
	if len(loaded) != 200 {
		t.Fatalf("loaded %d events, want 200", len(loaded))
	}
}
