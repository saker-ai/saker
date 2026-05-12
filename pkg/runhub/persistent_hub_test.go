package runhub

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/cinience/saker/pkg/runhub/store"
)

// openTestStore returns a fresh store backed by a real sqlite file under
// t.TempDir. Mirrors store.memStore(t) but exposes the path so tests
// that need to "restart" can reopen the same DB.
func openTestStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "runhub.db")
	s, err := store.Open(store.Config{DSN: path})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, path
}

// newTestPersistentHub builds a PersistentHub against a freshly-opened
// store. cfg.Logger / cfg.Store get filled in if zero.
func newTestPersistentHub(t *testing.T, cfg PersistentConfig) *PersistentHub {
	t.Helper()
	if cfg.Store == nil {
		s, _ := openTestStore(t)
		cfg.Store = s
	}
	h, err := NewPersistentHub(cfg)
	if err != nil {
		t.Fatalf("NewPersistentHub: %v", err)
	}
	t.Cleanup(h.Shutdown)
	return h
}

func TestPersistentHub_CreatePersistsRow(t *testing.T) {
	t.Parallel()
	s, _ := openTestStore(t)
	h := newTestPersistentHub(t, PersistentConfig{Store: s})

	run, err := h.Create(CreateOptions{
		SessionID: "sess1",
		TenantID:  "t1",
		ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	row, err := s.LoadRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("store should hold the row, got err=%v", err)
	}
	if row.SessionID != "sess1" || row.TenantID != "t1" {
		t.Errorf("LoadRun = %+v, missing identity fields", row)
	}
	if row.Status != string(RunStatusQueued) {
		t.Errorf("status = %q, want queued (initial)", row.Status)
	}
}

func TestPersistentHub_PublishWritesToStore(t *testing.T) {
	t.Parallel()
	s, _ := openTestStore(t)
	h := newTestPersistentHub(t, PersistentConfig{Store: s})

	run, _ := h.Create(CreateOptions{TenantID: "t1", ExpiresAt: time.Now().Add(time.Hour)})
	for i := 0; i < 3; i++ {
		run.Publish("chunk", []byte{byte('a' + i)})
	}
	// Async batch writer — fence so the LoadEventsSince readback observes
	// every event we just published.
	h.Flush()

	rows, err := s.LoadEventsSince(context.Background(), run.ID, 0)
	if err != nil {
		t.Fatalf("LoadEventsSince: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 stored events, got %d", len(rows))
	}
	for i, r := range rows {
		if r.Seq != i+1 {
			t.Errorf("rows[%d].Seq = %d, want %d", i, r.Seq, i+1)
		}
	}
}

func TestPersistentHub_RestartReplay(t *testing.T) {
	t.Parallel()
	s1, path := openTestStore(t)
	h1, err := NewPersistentHub(PersistentConfig{
		Config: Config{RingSize: 4},
		Store:  s1,
	})
	if err != nil {
		t.Fatalf("NewPersistentHub h1: %v", err)
	}

	// Force the run into a non-terminal status so loadActive picks it
	// up after restart. Without this the new in-memory run sits at
	// RunStatusQueued, which IS in activeStatuses — so this is just
	// belt-and-suspenders.
	run, err := h1.Create(CreateOptions{
		SessionID: "sess",
		TenantID:  "t1",
		ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("h1 Create: %v", err)
	}
	run.SetStatus(RunStatusInProgress)
	_ = s1.UpdateStatus(context.Background(), run.ID, string(RunStatusInProgress))

	for i := 0; i < 5; i++ {
		run.Publish("chunk", []byte{byte('A' + i)})
	}
	runID := run.ID
	// Async batch writer — flush before "crash" so the persisted state
	// reflects everything the run published. A real crash MIGHT lose the
	// last batch; this test asserts the steady-state revival path.
	h1.Flush()

	// Simulate an abrupt shutdown — close the store directly without
	// calling h1.Shutdown so the DB row stays at "in_progress" (the
	// pre-crash status), mimicking a kill.
	if err := s1.Close(); err != nil {
		t.Fatalf("close s1: %v", err)
	}

	// "Restart": reopen the same DB and instantiate a fresh hub.
	s2, err := store.Open(store.Config{DSN: path})
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	h2, err := NewPersistentHub(PersistentConfig{
		Config: Config{RingSize: 4},
		Store:  s2,
	})
	if err != nil {
		t.Fatalf("NewPersistentHub h2: %v", err)
	}
	t.Cleanup(h2.Shutdown)

	// Get must return the revived shell (in-memory miss, store hit).
	revived, err := h2.Get(runID)
	if err != nil {
		t.Fatalf("h2 Get after restart: %v", err)
	}
	if revived.Status() != RunStatusInProgress {
		t.Errorf("revived status = %q, want in_progress", revived.Status())
	}
	if revived.SessionID != "sess" || revived.TenantID != "t1" {
		t.Errorf("revived identity wrong: %+v", revived)
	}

	// Backfill from sink — ring is empty so sink.loadSince fires.
	_, backfill, recoverable, unsub := revived.SubscribeSince(0)
	defer unsub()
	if !recoverable {
		t.Fatalf("expected recoverable=true (sink should fill empty ring)")
	}
	if len(backfill) != 5 {
		t.Fatalf("expected 5 events from sink, got %d", len(backfill))
	}
	for i, e := range backfill {
		if e.Seq != i+1 {
			t.Errorf("backfill[%d].Seq = %d, want %d", i, e.Seq, i+1)
		}
	}

	// Per-tenant counter must also be restored.
	if got := h2.LenForTenant("t1"); got != 1 {
		t.Errorf("LenForTenant = %d, want 1 after revival", got)
	}
}

func TestPersistentHub_RingMissFallsBackToStore(t *testing.T) {
	t.Parallel()
	s, _ := openTestStore(t)
	h := newTestPersistentHub(t, PersistentConfig{
		Config: Config{RingSize: 4},
		Store:  s,
	})

	run, _ := h.Create(CreateOptions{TenantID: "t1", ExpiresAt: time.Now().Add(time.Hour)})

	// Publish more events than the ring can hold; oldest seqs age out.
	const total = 20
	for i := 0; i < total; i++ {
		run.Publish("chunk", []byte{byte(i)})
	}
	// Async batch writer — fence before the SubscribeSince readback so
	// the sink fallback observes every event we published.
	h.Flush()

	// Without the sink the ring would have only seqs 17-20 and a
	// SubscribeSince(5) would return recoverable=false (5+1 < 17).
	// With the sink we expect a full backfill of 15 events (seqs 6..20).
	_, backfill, recoverable, unsub := run.SubscribeSince(5)
	defer unsub()
	if !recoverable {
		t.Fatalf("expected sink to recover the aged-out prefix")
	}
	if len(backfill) != 15 {
		t.Fatalf("expected 15 backfilled events, got %d", len(backfill))
	}
	if backfill[0].Seq != 6 || backfill[len(backfill)-1].Seq != 20 {
		t.Errorf("backfill range = [%d..%d], want [6..20]",
			backfill[0].Seq, backfill[len(backfill)-1].Seq)
	}
}

func TestPersistentHub_FinishUpdatesStoreStatus(t *testing.T) {
	t.Parallel()
	s, _ := openTestStore(t)
	h := newTestPersistentHub(t, PersistentConfig{Store: s})

	run, _ := h.Create(CreateOptions{TenantID: "t1"})
	h.Finish(run.ID, RunStatusCompleted)

	row, err := s.LoadRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("LoadRun: %v", err)
	}
	if row.Status != string(RunStatusCompleted) {
		t.Errorf("DB status = %q, want completed", row.Status)
	}
}

func TestPersistentHub_RemoveDeletesFromStore(t *testing.T) {
	t.Parallel()
	s, _ := openTestStore(t)
	h := newTestPersistentHub(t, PersistentConfig{Store: s})

	run, _ := h.Create(CreateOptions{TenantID: "t1"})
	for i := 0; i < 3; i++ {
		run.Publish("chunk", nil)
	}

	h.Remove(run.ID)

	if _, err := s.LoadRun(context.Background(), run.ID); err == nil {
		t.Errorf("expected store row to be gone after Remove")
	}
	rows, _ := s.LoadEventsSince(context.Background(), run.ID, 0)
	if len(rows) != 0 {
		t.Errorf("expected events purged after Remove, got %d rows", len(rows))
	}
	// In-memory side should also be gone.
	if _, err := h.Get(run.ID); err == nil {
		t.Errorf("expected ErrNotFound after Remove")
	}
}

func TestPersistentHub_GetReturnsErrNotFoundForUnknown(t *testing.T) {
	t.Parallel()
	h := newTestPersistentHub(t, PersistentConfig{})
	if _, err := h.Get("run_does_not_exist"); err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestPersistentHub_StoreSweepExpiresOverdueRows(t *testing.T) {
	t.Parallel()
	s, _ := openTestStore(t)
	h := newTestPersistentHub(t, PersistentConfig{Store: s})

	// In-memory + store row, with an already-past ExpiresAt — set the
	// past deadline directly in DB so sweepStore picks it up. The
	// in-memory run was created with no deadline so the inner sweeper
	// won't double-handle it.
	run, _ := h.Create(CreateOptions{TenantID: "t1"})
	_ = s.UpsertRun(context.Background(), store.RunRow{
		ID:        run.ID,
		SessionID: run.SessionID,
		TenantID:  run.TenantID,
		Status:    string(RunStatusInProgress),
		ExpiresAt: time.Now().Add(-time.Minute),
	})

	h.sweepStore(context.Background())

	row, err := s.LoadRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("LoadRun after sweep: %v", err)
	}
	if row.Status != string(RunStatusExpired) {
		t.Errorf("expected status=expired after sweep, got %q", row.Status)
	}
	if got := run.Status(); got != RunStatusExpired {
		t.Errorf("in-memory status = %q, want expired", got)
	}
}

func TestPersistentHub_StoreSweepDeletesOldTerminal(t *testing.T) {
	t.Parallel()
	s, _ := openTestStore(t)
	h := newTestPersistentHub(t, PersistentConfig{Store: s})

	run, _ := h.Create(CreateOptions{TenantID: "t1"})
	for i := 0; i < 3; i++ {
		run.Publish("chunk", nil)
	}
	h.Finish(run.ID, RunStatusCompleted)

	// Backdate the row so it falls past the retention cutoff in
	// sweepStore. UpsertRun preserves CreatedAt only when zero, so we
	// rewrite UpdatedAt directly.
	if err := s.DB().Exec("UPDATE runhub_runs SET updated_at = ? WHERE id = ?",
		time.Now().Add(-2*time.Hour), run.ID).Error; err != nil {
		t.Fatalf("backdate UpdatedAt: %v", err)
	}

	h.sweepStore(context.Background())

	if _, err := s.LoadRun(context.Background(), run.ID); err == nil {
		t.Errorf("expected store row to be deleted by retention sweep")
	}
	rows, _ := s.LoadEventsSince(context.Background(), run.ID, 0)
	if len(rows) != 0 {
		t.Errorf("expected events deleted with the row, got %d", len(rows))
	}
	if _, err := h.Get(run.ID); err == nil {
		t.Errorf("in-memory run should also be gone")
	}
}

func TestPersistentHub_NewRequiresStore(t *testing.T) {
	t.Parallel()
	if _, err := NewPersistentHub(PersistentConfig{}); err == nil {
		t.Errorf("expected error when Store is nil")
	}
}

func TestPersistentHub_ShutdownIdempotent(t *testing.T) {
	t.Parallel()
	s, _ := openTestStore(t)
	h, err := NewPersistentHub(PersistentConfig{Store: s})
	if err != nil {
		t.Fatalf("NewPersistentHub: %v", err)
	}
	h.Shutdown()
	// Second Shutdown should not panic on re-close of the storeGCStop
	// channel or the underlying store.
	h.Shutdown()
}

func TestPersistentHub_SatisfiesHubInterface(t *testing.T) {
	t.Parallel()
	// Compile-time assertion lives in persistent_hub.go; this test is
	// a runtime sanity check that interface dispatch works.
	s, _ := openTestStore(t)
	h, err := NewPersistentHub(PersistentConfig{Store: s})
	if err != nil {
		t.Fatalf("NewPersistentHub: %v", err)
	}
	t.Cleanup(h.Shutdown)
	var hub Hub = h
	if _, err := hub.Get("nope"); err != ErrNotFound {
		t.Errorf("interface dispatch broken: Get returned %v, want ErrNotFound", err)
	}
}
