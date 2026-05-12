package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// memStore returns a fresh, fully-isolated Store for a test, mirroring the
// pkg/project memStore(t) fixture. We use a real sqlite file under
// t.TempDir rather than ":memory:" because glebarez/sqlite + the GORM pool
// have surprising cache-sharing behavior across goroutines (documented in
// pkg/project/store_test.go:8-12).
func memStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(Config{DSN: filepath.Join(t.TempDir(), "runhub.db")})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestOpen_RejectsEmptyDSN(t *testing.T) {
	t.Parallel()
	if _, err := Open(Config{}); err == nil {
		t.Fatalf("expected error on empty DSN")
	}
}

func TestOpen_PRAGMAsAppendedToBareSQLite(t *testing.T) {
	t.Parallel()
	if got := withSQLitePragmas("/tmp/x.db"); !strings.Contains(got, "journal_mode(WAL)") {
		t.Errorf("expected WAL pragma appended, got %q", got)
	}
	// Existing query string → pragmas merged with &.
	if got := withSQLitePragmas("/tmp/x.db?cache=shared"); !strings.Contains(got, "cache=shared") || !strings.Contains(got, "journal_mode(WAL)") {
		t.Errorf("expected merged query, got %q", got)
	}
	// Operator already supplied a pragma → leave it alone (operator wins).
	op := "/tmp/x.db?_pragma=journal_mode(MEMORY)"
	if got := withSQLitePragmas(op); got != op {
		t.Errorf("operator pragmas should win, got %q", got)
	}
}

func TestOpen_AutoMigrateIdempotent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "runhub.db")
	s1, err := Open(Config{DSN: path})
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	if got := s1.Driver(); got != "sqlite" {
		t.Errorf("driver = %q, want sqlite", got)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	s2, err := Open(Config{DSN: path})
	if err != nil {
		t.Fatalf("re-open (auto-migrate must be idempotent): %v", err)
	}
	_ = s2.Close()
}

func TestUpsertRun_CreatesAndReplaces(t *testing.T) {
	t.Parallel()
	s := memStore(t)
	ctx := context.Background()

	row := RunRow{
		ID:        "run_aaa",
		SessionID: "sess1",
		TenantID:  "t1",
		Status:    "in_progress",
		ExpiresAt: time.Now().Add(time.Minute),
	}
	if err := s.UpsertRun(ctx, row); err != nil {
		t.Fatalf("UpsertRun create: %v", err)
	}

	got, err := s.LoadRun(ctx, "run_aaa")
	if err != nil {
		t.Fatalf("LoadRun: %v", err)
	}
	if got.Status != "in_progress" || got.SessionID != "sess1" {
		t.Errorf("LoadRun returned %+v", got)
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Errorf("CreatedAt / UpdatedAt should be auto-stamped, got %+v", got)
	}

	// Upsert again with a new status — UpdatedAt advances, CreatedAt should
	// be preserved (Upsert is conflict-aware on id).
	prevCreated := got.CreatedAt
	time.Sleep(2 * time.Millisecond)
	row.Status = "completed"
	if err := s.UpsertRun(ctx, row); err != nil {
		t.Fatalf("UpsertRun replace: %v", err)
	}
	got2, _ := s.LoadRun(ctx, "run_aaa")
	if got2.Status != "completed" {
		t.Errorf("expected status=completed after upsert, got %q", got2.Status)
	}
	if !got2.CreatedAt.Equal(prevCreated) {
		t.Errorf("CreatedAt should survive Upsert: was %v, became %v", prevCreated, got2.CreatedAt)
	}
}

func TestLoadRun_NotFoundReturnsErrNotFound(t *testing.T) {
	t.Parallel()
	s := memStore(t)
	if _, err := s.LoadRun(context.Background(), "run_missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestUpdateStatus_BumpsRow(t *testing.T) {
	t.Parallel()
	s := memStore(t)
	ctx := context.Background()
	_ = s.UpsertRun(ctx, RunRow{ID: "run_x", Status: "queued", TenantID: "t1"})

	if err := s.UpdateStatus(ctx, "run_x", "in_progress"); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	got, _ := s.LoadRun(ctx, "run_x")
	if got.Status != "in_progress" {
		t.Errorf("status = %q, want in_progress", got.Status)
	}
}

func TestInsertEvent_Appends(t *testing.T) {
	t.Parallel()
	s := memStore(t)
	ctx := context.Background()
	_ = s.UpsertRun(ctx, RunRow{ID: "run_e", Status: "in_progress"})

	for i := 1; i <= 5; i++ {
		err := s.InsertEvent(ctx, EventRow{
			RunID: "run_e", Seq: i, Type: "chunk", Data: []byte{byte(i)},
		})
		if err != nil {
			t.Fatalf("InsertEvent #%d: %v", i, err)
		}
	}

	rows, err := s.LoadEventsSince(ctx, "run_e", 0)
	if err != nil {
		t.Fatalf("LoadEventsSince: %v", err)
	}
	if len(rows) != 5 {
		t.Fatalf("expected 5 events, got %d", len(rows))
	}
	for i, r := range rows {
		if r.Seq != i+1 {
			t.Errorf("row[%d].Seq = %d, want %d", i, r.Seq, i+1)
		}
	}
}

func TestInsertEvent_DuplicatePKIgnored(t *testing.T) {
	t.Parallel()
	s := memStore(t)
	ctx := context.Background()
	_ = s.UpsertRun(ctx, RunRow{ID: "run_d", Status: "in_progress"})

	row := EventRow{RunID: "run_d", Seq: 1, Type: "chunk", Data: []byte("first")}
	if err := s.InsertEvent(ctx, row); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	// Same (RunID, Seq) — DoNothing must absorb it without error.
	dup := EventRow{RunID: "run_d", Seq: 1, Type: "chunk", Data: []byte("second")}
	if err := s.InsertEvent(ctx, dup); err != nil {
		t.Fatalf("duplicate insert should be silently ignored, got %v", err)
	}
	rows, _ := s.LoadEventsSince(ctx, "run_d", 0)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row after duplicate, got %d", len(rows))
	}
	if string(rows[0].Data) != "first" {
		t.Errorf("first write must win, got %q", rows[0].Data)
	}
}

func TestLoadEventsSince_RangeFilter(t *testing.T) {
	t.Parallel()
	s := memStore(t)
	ctx := context.Background()
	_ = s.UpsertRun(ctx, RunRow{ID: "run_r", Status: "in_progress"})

	for i := 1; i <= 10; i++ {
		_ = s.InsertEvent(ctx, EventRow{RunID: "run_r", Seq: i, Type: "chunk"})
	}
	rows, _ := s.LoadEventsSince(ctx, "run_r", 7)
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows after seq=7, got %d", len(rows))
	}
	if rows[0].Seq != 8 || rows[2].Seq != 10 {
		t.Errorf("range = [%d..%d], want [8..10]", rows[0].Seq, rows[2].Seq)
	}
}

func TestMaxSeq(t *testing.T) {
	t.Parallel()
	s := memStore(t)
	ctx := context.Background()
	_ = s.UpsertRun(ctx, RunRow{ID: "run_m", Status: "in_progress"})

	if got, _ := s.MaxSeq(ctx, "run_m"); got != 0 {
		t.Errorf("empty-run MaxSeq = %d, want 0", got)
	}
	for i := 1; i <= 4; i++ {
		_ = s.InsertEvent(ctx, EventRow{RunID: "run_m", Seq: i, Type: "chunk"})
	}
	if got, _ := s.MaxSeq(ctx, "run_m"); got != 4 {
		t.Errorf("MaxSeq = %d, want 4", got)
	}
}

func TestLoadActiveRuns_ExcludesTerminal(t *testing.T) {
	t.Parallel()
	s := memStore(t)
	ctx := context.Background()

	_ = s.UpsertRun(ctx, RunRow{ID: "r1", Status: "in_progress"})
	_ = s.UpsertRun(ctx, RunRow{ID: "r2", Status: "queued"})
	_ = s.UpsertRun(ctx, RunRow{ID: "r3", Status: "completed"})
	_ = s.UpsertRun(ctx, RunRow{ID: "r4", Status: "failed"})

	active, err := s.LoadActiveRuns(ctx)
	if err != nil {
		t.Fatalf("LoadActiveRuns: %v", err)
	}
	if len(active) != 2 {
		t.Fatalf("expected 2 active rows, got %d (%+v)", len(active), active)
	}
	got := map[string]bool{}
	for _, r := range active {
		got[r.ID] = true
	}
	if !got["r1"] || !got["r2"] {
		t.Errorf("missing expected active rows, got %v", got)
	}
}

func TestDeleteRun_RemovesRowAndEvents(t *testing.T) {
	t.Parallel()
	s := memStore(t)
	ctx := context.Background()
	_ = s.UpsertRun(ctx, RunRow{ID: "del", Status: "completed"})
	for i := 1; i <= 3; i++ {
		_ = s.InsertEvent(ctx, EventRow{RunID: "del", Seq: i, Type: "chunk"})
	}

	if err := s.DeleteRun(ctx, "del"); err != nil {
		t.Fatalf("DeleteRun: %v", err)
	}
	if _, err := s.LoadRun(ctx, "del"); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected run gone, got err=%v", err)
	}
	rows, _ := s.LoadEventsSince(ctx, "del", 0)
	if len(rows) != 0 {
		t.Errorf("expected events gone, got %d", len(rows))
	}
}

func TestSweepExpired_FlipsActiveRowsPastDeadline(t *testing.T) {
	t.Parallel()
	s := memStore(t)
	ctx := context.Background()

	now := time.Now()
	// past-deadline + active → should flip to expired
	_ = s.UpsertRun(ctx, RunRow{ID: "exp1", Status: "in_progress", ExpiresAt: now.Add(-time.Minute)})
	// past-deadline + already terminal → ignored
	_ = s.UpsertRun(ctx, RunRow{ID: "term", Status: "completed", ExpiresAt: now.Add(-time.Minute)})
	// future-deadline + active → ignored
	_ = s.UpsertRun(ctx, RunRow{ID: "future", Status: "in_progress", ExpiresAt: now.Add(time.Hour)})
	// no-deadline + active → ignored
	_ = s.UpsertRun(ctx, RunRow{ID: "nodl", Status: "in_progress"})

	ids, err := s.SweepExpired(ctx, now)
	if err != nil {
		t.Fatalf("SweepExpired: %v", err)
	}
	if len(ids) != 1 || ids[0] != "exp1" {
		t.Fatalf("expected [exp1], got %v", ids)
	}

	got, _ := s.LoadRun(ctx, "exp1")
	if got.Status != "expired" {
		t.Errorf("expected status=expired after sweep, got %q", got.Status)
	}
}

func TestSweepFinished_ReturnsOldTerminalIDs(t *testing.T) {
	t.Parallel()
	s := memStore(t)
	ctx := context.Background()

	_ = s.UpsertRun(ctx, RunRow{ID: "old1", Status: "completed"})
	_ = s.UpsertRun(ctx, RunRow{ID: "old2", Status: "failed"})
	_ = s.UpsertRun(ctx, RunRow{ID: "live", Status: "in_progress"})

	// Old-cutoff in the FUTURE so all rows look "old enough".
	ids, err := s.SweepFinished(ctx, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("SweepFinished: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("expected 2 terminal ids, got %d (%v)", len(ids), ids)
	}
	have := map[string]bool{}
	for _, id := range ids {
		have[id] = true
	}
	if !have["old1"] || !have["old2"] {
		t.Errorf("missing terminal ids, got %v", ids)
	}
	if have["live"] {
		t.Errorf("active row should not be in finished sweep")
	}
}

func TestClose_Idempotent(t *testing.T) {
	t.Parallel()
	s, err := Open(Config{DSN: filepath.Join(t.TempDir(), "runhub.db")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	// Second Close should not error and should not panic.
	if err := s.Close(); err != nil {
		t.Errorf("second close returned %v, want nil", err)
	}
}
