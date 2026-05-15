//go:build postgres && integration

// Postgres integration test for PersistentHub cross-process fan-out.
// Skipped when SAKER_TEST_PG_DSN is unset.
//
// Run with:
//
//	SAKER_TEST_PG_DSN=postgres://user:pass@localhost/dbname?sslmode=disable \
//	  go test -tags 'postgres integration' ./pkg/runhub/...

package runhub_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/saker-ai/saker/pkg/runhub"
	"github.com/saker-ai/saker/pkg/runhub/store"
)

func openHub(t *testing.T, dsn string) *runhub.PersistentHub {
	t.Helper()
	s, err := store.Open(store.Config{DSN: dsn})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	h, err := runhub.NewPersistentHub(runhub.PersistentConfig{
		Config: runhub.Config{RingSize: 16},
		Store:  s,
	})
	if err != nil {
		_ = s.Close()
		t.Fatalf("new hub: %v", err)
	}
	t.Cleanup(h.Shutdown)
	return h
}

func TestPostgres_PersistentHub_CrossProcessFanOut(t *testing.T) {
	dsn := os.Getenv("SAKER_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("set SAKER_TEST_PG_DSN to run postgres integration tests")
	}

	hubA := openHub(t, dsn)
	hubB := openHub(t, dsn)

	// Producer side: hub A creates the run and publishes a few events.
	runA, err := hubA.Create(runhub.CreateOptions{
		TenantID:  "t-xprocess",
		ExpiresAt: time.Now().Add(5 * time.Minute),
	})
	if err != nil {
		t.Fatalf("hubA.Create: %v", err)
	}
	t.Cleanup(func() { hubA.Remove(runA.ID) })

	for i := 0; i < 3; i++ {
		runA.Publish("chunk", []byte{byte('a' + i)})
	}

	// Consumer side: hub B revives the run from the store + LISTEN.
	runB, err := hubB.Get(runA.ID)
	if err != nil {
		t.Fatalf("hubB.Get: %v", err)
	}

	// Initial backfill comes through the sink.loadSince path because B's
	// ring is empty. Subscribe at seq 0 to receive everything.
	ch, backfill, recoverable, unsub := runB.SubscribeSince(0)
	defer unsub()
	if !recoverable {
		t.Fatal("expected recoverable=true on revived shell")
	}
	if len(backfill) != 3 {
		t.Fatalf("backfill = %d events, want 3", len(backfill))
	}

	// Now publish one more event on A. B's listener should pick it up
	// via NOTIFY → LoadEventsSince → DeliverExternal.
	runA.Publish("chunk", []byte("d"))

	select {
	case e, ok := <-ch:
		if !ok {
			t.Fatal("subscriber closed before live event arrived")
		}
		if e.Seq != 4 {
			t.Errorf("seq = %d, want 4", e.Seq)
		}
		if string(e.Data) != "d" {
			t.Errorf("data = %q, want %q", e.Data, "d")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("live event did not arrive on B within 5s")
	}
}

// TestPostgres_PersistentHub_RestartReplay verifies the existing
// restart-replay behavior also works against postgres (the SQLite
// unit test exercises the same path against in-memory dialect).
func TestPostgres_PersistentHub_RestartReplay(t *testing.T) {
	dsn := os.Getenv("SAKER_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("set SAKER_TEST_PG_DSN to run postgres integration tests")
	}

	hub := openHub(t, dsn)
	run, err := hub.Create(runhub.CreateOptions{
		TenantID:  "t-restart",
		ExpiresAt: time.Now().Add(5 * time.Minute),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { hub.Remove(run.ID) })

	for i := 0; i < 5; i++ {
		run.Publish("chunk", []byte{byte('A' + i)})
	}

	// "Restart" by spinning up a fresh hub on the same DSN.
	hub2 := openHub(t, dsn)
	revived, err := hub2.Get(run.ID)
	if err != nil {
		t.Fatalf("hub2.Get: %v", err)
	}
	_, backfill, _, unsub := revived.SubscribeSince(0)
	defer unsub()
	if len(backfill) != 5 {
		t.Fatalf("revived backfill = %d, want 5", len(backfill))
	}
}

// silence unused import warnings if context is conditionally referenced.
var _ = context.Background
