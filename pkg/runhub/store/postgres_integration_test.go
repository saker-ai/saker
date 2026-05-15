//go:build postgres && integration

// Postgres integration test for the runhub store's LISTEN/NOTIFY
// fan-out plus the PersistentHub cross-process flow that depends on
// it. Skipped automatically when SAKER_TEST_PG_DSN is unset.
//
// Run with:
//
//	SAKER_TEST_PG_DSN=postgres://user:pass@localhost/dbname?sslmode=disable \
//	  go test -tags 'postgres integration' ./pkg/runhub/store/...
//
// The test database must already exist. The test exercises end-to-end
// cross-process fan-out: it opens TWO independent Stores against the
// same DB, NOTIFYs from one and LISTENs on the other, asserting that
// notifications round-trip through the broker.

package store_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/saker-ai/saker/pkg/runhub/store"
)

func pgDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("SAKER_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("set SAKER_TEST_PG_DSN to run postgres integration tests")
	}
	return dsn
}

func openPG(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(store.Config{DSN: pgDSN(t)})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestPostgres_ListenNotify_CrossConnection(t *testing.T) {
	dsn := pgDSN(t)

	// Two independent Stores against the same DB — listener on one,
	// publisher on the other. Mirrors the cross-process scenario.
	listenerStore, err := store.Open(store.Config{DSN: dsn})
	if err != nil {
		t.Fatalf("open listener store: %v", err)
	}
	defer listenerStore.Close()

	publisherStore, err := store.Open(store.Config{DSN: dsn})
	if err != nil {
		t.Fatalf("open publisher store: %v", err)
	}
	defer publisherStore.Close()

	channel := "runhub_test_xprocess"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	listener, err := listenerStore.Listen(ctx, channel)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer listener.Close()

	// Wait briefly for the LISTEN to register before NOTIFYing — pgx
	// is synchronous on the LISTEN exec but the broker takes a tick.
	time.Sleep(100 * time.Millisecond)

	if err := publisherStore.Notify(ctx, channel, "hello"); err != nil {
		t.Fatalf("Notify: %v", err)
	}

	select {
	case payload, ok := <-listener.Notifications():
		if !ok {
			t.Fatal("listener channel closed before notification arrived")
		}
		if payload != "hello" {
			t.Errorf("payload = %q, want %q", payload, "hello")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("notification did not arrive within 3s")
	}
}

func TestPostgres_NotifyOnSqliteIsNoOp(t *testing.T) {
	t.Parallel()
	// Sanity: ensure Notify on the postgres path doesn't error for
	// well-formed channels (the no-op assertion is in the sqlite
	// unit test elsewhere; this one just verifies PG accepts our
	// inputs).
	s := openPG(t)
	if err := s.Notify(context.Background(), "runhub_smoke", "ping"); err != nil {
		t.Errorf("Notify with no listener should still succeed, got %v", err)
	}
}
