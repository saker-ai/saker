package runhub

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/cinience/saker/pkg/runhub/store"
)

// SQLite chaos tests for E.6. These assert that filesystem-level
// failure modes (DB file deleted, WAL read-only, disk full) do not take
// the producer down: ring + fan-out keep serving live subscribers, and
// the persistent hub's circuit breaker trips Open after sustained store
// failure so the writer goroutine stops burning CPU on doomed flushes.
//
// Plan placement note: the plan calls for `pkg/runhub/store/chaos_sqlite_test.go`,
// but the load-bearing assertions ("publish errors but ring/fan-out
// don't block; breaker enters Open and keeps running") require
// PersistentHub + dbSink + sinkBreaker — all of which live in
// pkg/runhub. Co-located here next to chaos_test.go so we can reuse
// openTestStore / newTestPersistentHub / countingHooks / contains.
//
// Linux file-descriptor semantics caveat: an unlinked or chmod'd SQLite
// DB file is still writable through an already-open FD (the inode
// persists until the last FD closes; chmod is checked at open() time,
// not on every write). So `os.Remove(dbPath)` alone does NOT guarantee
// SQLite writes start failing on Linux. To reliably trigger the
// "store is broken" path under SQLite + WAL, every test below combines
// the documented filesystem gesture (Remove / Chmod / /dev/full) with
// closing the store's *sql.DB pool — the Close() is what the next
// InsertEventsBatch trips on.

// TestChaos_SQLite_DBFileDeleted simulates accidental deletion of the
// SQLite database file and its sidecars (-wal, -shm) underneath a
// running hub. Verifies:
//  1. Publish never blocks (watchdog).
//  2. Live subscribers still receive events from the in-memory ring.
//  3. The circuit breaker trips Open within a bounded window so the
//     batchWriter stops attempting failed flushes.
func TestChaos_SQLite_DBFileDeleted(t *testing.T) {
	t.Parallel()
	hooks := newCountingHooks()
	s, dbPath := openTestStore(t)
	h := newTestPersistentHub(t, PersistentConfig{
		Store:                s,
		Metrics:              hooks,
		BatchSize:            1, // every Publish is its own flush — fastest path to threshold
		BatchInterval:        time.Hour,
		SinkBreakerThreshold: 3,
		SinkBreakerCooldown:  time.Hour, // long enough that recovery doesn't race
	})

	run, err := h.Create(CreateOptions{TenantID: "chaos-sqlite", ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Sanity: storage works pre-chaos.
	run.Publish("chunk", []byte("pre-chaos"))
	h.Flush()
	if rows, err := s.LoadEventsSince(context.Background(), run.ID, 0); err != nil || len(rows) == 0 {
		t.Fatalf("pre-chaos publish didn't persist: rows=%d err=%v", len(rows), err)
	}

	// Subscribe + drain in background so fan-out is exercised under chaos.
	ch, _, unsub := run.Subscribe()
	defer unsub()
	var wg sync.WaitGroup
	wg.Add(1)
	var fanoutGot int
	stopDrain := make(chan struct{})
	go func() {
		defer wg.Done()
		for {
			select {
			case _, ok := <-ch:
				if !ok {
					return
				}
				fanoutGot++
			case <-stopDrain:
				return
			}
		}
	}()

	// Chaos: nuke the SQLite files AND close the store. Removing the
	// files is the documented gesture; closing the store guarantees
	// SQLite stops accepting inserts (Linux WAL+open FD would otherwise
	// keep writes succeeding).
	_ = os.Remove(dbPath)
	_ = os.Remove(dbPath + "-wal")
	_ = os.Remove(dbPath + "-shm")
	_ = s.Close()

	// Watchdog the producer: a stuck Publish (sink-induced backpressure
	// regression) trips here instead of hanging the test indefinitely.
	const burst = 20
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < burst; i++ {
			run.Publish("chunk", []byte{byte(i)})
			time.Sleep(15 * time.Millisecond) // give the writer time to drain so each event becomes its own batch
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("Publish loop blocked > 5s after store sabotage — store failures must not backpressure into Publish")
	}

	// Wait for the breaker to settle into Open.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		hooks.mu.Lock()
		opened := contains(hooks.breakerTransitions, "closed->open")
		hooks.mu.Unlock()
		if opened {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	close(stopDrain)
	wg.Wait()

	hooks.mu.Lock()
	defer hooks.mu.Unlock()
	if !contains(hooks.breakerTransitions, "closed->open") {
		t.Errorf("breaker never opened after DB file deletion + store close; transitions=%v persistErr=%d",
			hooks.breakerTransitions, hooks.persistErr)
	}
	if fanoutGot == 0 {
		t.Errorf("subscriber received 0 events — fan-out broken under store failure (live ring should keep serving)")
	}
}

// TestChaos_SQLite_WALReadOnly simulates a partial disk-readonly
// condition by chmod-ing the WAL/SHM sidecars to 0444. Same invariants
// as DBFileDeleted: producer alive, fan-out alive, breaker trips.
//
// On Linux the chmod-after-open semantics mean already-held FDs keep
// write access — so we again close the store to guarantee the next
// flush fails. The chmod gesture documents the intended chaos mode and
// makes any future reopen attempt fail too.
func TestChaos_SQLite_WALReadOnly(t *testing.T) {
	t.Parallel()
	hooks := newCountingHooks()
	s, dbPath := openTestStore(t)
	h := newTestPersistentHub(t, PersistentConfig{
		Store:                s,
		Metrics:              hooks,
		BatchSize:            1,
		BatchInterval:        time.Hour,
		SinkBreakerThreshold: 3,
		SinkBreakerCooldown:  time.Hour,
	})

	run, err := h.Create(CreateOptions{TenantID: "chaos-sqlite", ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Force WAL + SHM into existence before the chmod so chmod has
	// targets. A single Publish + Flush guarantees both files exist.
	run.Publish("chunk", []byte("warmup"))
	h.Flush()

	ch, _, unsub := run.Subscribe()
	defer unsub()
	var wg sync.WaitGroup
	wg.Add(1)
	var fanoutGot int
	stopDrain := make(chan struct{})
	go func() {
		defer wg.Done()
		for {
			select {
			case _, ok := <-ch:
				if !ok {
					return
				}
				fanoutGot++
			case <-stopDrain:
				return
			}
		}
	}()

	// Chaos: lock down the SQLite files to read-only AND close the store.
	for _, p := range []string{dbPath, dbPath + "-wal", dbPath + "-shm"} {
		if _, err := os.Stat(p); err == nil {
			_ = os.Chmod(p, 0o444)
		}
	}
	_ = s.Close()

	const burst = 20
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < burst; i++ {
			run.Publish("chunk", []byte{byte(i)})
			time.Sleep(15 * time.Millisecond)
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("Publish loop blocked > 5s after WAL chmod — store failures must not backpressure into Publish")
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		hooks.mu.Lock()
		opened := contains(hooks.breakerTransitions, "closed->open")
		hooks.mu.Unlock()
		if opened {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	close(stopDrain)
	wg.Wait()

	// Restore perms so t.TempDir cleanup can rm the files.
	for _, p := range []string{dbPath, dbPath + "-wal", dbPath + "-shm"} {
		_ = os.Chmod(p, 0o644)
	}

	hooks.mu.Lock()
	defer hooks.mu.Unlock()
	if !contains(hooks.breakerTransitions, "closed->open") {
		t.Errorf("breaker never opened after WAL read-only + store close; transitions=%v persistErr=%d",
			hooks.breakerTransitions, hooks.persistErr)
	}
	if fanoutGot == 0 {
		t.Errorf("subscriber received 0 events — fan-out broken under store failure")
	}
}

// TestChaos_SQLite_DiskFull verifies that a SQLite store opened against
// a write-rejecting target (Linux's /dev/full character device, which
// returns ENOSPC on every write) fails at Open time rather than
// crashing or producing a half-initialized store. SQLite needs a
// seekable file for the database, and AutoMigrate writes schema rows
// during Open — both fail against /dev/full.
//
// Skipped on non-Linux systems because /dev/full is Linux-specific.
// On Linux without /dev/full present (rare; some minimal containers),
// also skipped — the test would be vacuous.
func TestChaos_SQLite_DiskFull(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skipf("/dev/full is a Linux-specific device; got %s", runtime.GOOS)
	}
	if _, err := os.Stat("/dev/full"); err != nil {
		t.Skipf("/dev/full not present on this system: %v", err)
	}

	// Try to open a SQLite store directly on /dev/full. This MUST fail —
	// either at sqlite Open (seek not supported on a character device)
	// or at AutoMigrate (write returns ENOSPC). Either failure mode is
	// acceptable; we just don't want a silent success that would hide a
	// bug where SQLite somehow tolerates a write-rejecting target.
	_, err := store.Open(store.Config{DSN: "/dev/full"})
	if err == nil {
		t.Fatal("expected error opening SQLite store on /dev/full, got nil (writes would silently vanish)")
	}
	t.Logf("expected store.Open error on /dev/full: %v", err)

	// Bonus: also try a path inside a normal tmpdir but with a parent
	// directory that's chmod'd 0500 (read+exec only, no write). SQLite
	// needs to create the file, so Open fails before AutoMigrate. This
	// covers the partition-full analog without needing root or special
	// devices.
	parent := filepath.Join(t.TempDir(), "no-write")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Fatalf("chmod parent: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o755) })
	_, err = store.Open(store.Config{DSN: filepath.Join(parent, "runhub.db")})
	if err == nil {
		t.Fatal("expected error opening SQLite store in a 0500 parent directory, got nil")
	}
	t.Logf("expected store.Open error in read-only parent dir: %v", err)
}
