//go:build postgres && integration

// Postgres chaos tests for the shared LISTEN pool. Verifies the
// auto-reconnect path: when the pool's pgx backend is killed, the
// reader goroutine reconnects with backoff, re-LISTENs every active
// channel, and pushes a synthetic payload to every subscriber so the
// consumer's poll-fallback (LoadEventsSince) closes any gap from
// notifications missed during the outage.
//
// Run with:
//
//	SAKER_TEST_PG_DSN=postgres://user:pass@localhost/dbname?sslmode=disable \
//	  go test -tags 'postgres integration' -run TestChaos_PG ./pkg/runhub/store/...
//
// Re-uses pgDSN/openPG helpers from postgres_integration_test.go.

package store_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cinience/saker/pkg/runhub/store"
	"github.com/jackc/pgx/v5"
)

// TestChaos_PG_ListenerConnDrop_Reconnects kills every non-self backend
// on the test database (which includes the listener pool's pgx.Conn),
// then asserts the reader goroutine reconnects, the LISTEN survives,
// and a fresh NOTIFY round-trips end-to-end.
//
// Two-phase verification:
//  1. After kill: a synthetic reconnect payload arrives within the
//     backoff cap (~1s with default jitter).
//  2. After reconnect: an explicit Notify round-trips through the
//     re-established LISTEN session.
func TestChaos_PG_ListenerConnDrop_Reconnects(t *testing.T) {
	dsn := pgDSN(t)

	var reconnectOK, reconnectFail atomic.Int64
	s, err := store.Open(store.Config{
		DSN: dsn,
		OnListenerReconnect: func(success bool) {
			if success {
				reconnectOK.Add(1)
			} else {
				reconnectFail.Add(1)
			}
		},
	})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	const channel = "saker_chaos_runhub_pool_drop"
	listener, err := s.Listen(context.Background(), channel)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	// Sanity: a NOTIFY before any chaos should round-trip immediately.
	if err := s.Notify(context.Background(), channel, "pre-chaos"); err != nil {
		t.Fatalf("pre-chaos Notify: %v", err)
	}
	select {
	case payload, ok := <-listener.Notifications():
		if !ok {
			t.Fatal("listener closed before pre-chaos NOTIFY arrived")
		}
		t.Logf("pre-chaos payload: %q", payload)
	case <-time.After(5 * time.Second):
		t.Fatal("pre-chaos NOTIFY never arrived")
	}

	// Drain any leftover synthetic payloads from the pre-chaos NOTIFY
	// (e.g. if the pool reconnected during open). Non-blocking peek.
	drainNotifications(listener.Notifications())

	// Now murder every backend except our killer connection. The pool's
	// pgx.Conn is one of them; the reader goroutine sees an error,
	// reconnects, re-LISTENs, and pushes a synthetic payload.
	if err := killAllOtherBackends(t, dsn); err != nil {
		t.Fatalf("kill backends: %v", err)
	}

	// Phase 1: synthetic reconnect payload (or any payload from a racing
	// real NOTIFY) must arrive within the backoff cap window.
	select {
	case payload, ok := <-listener.Notifications():
		if !ok {
			t.Fatal("listener closed before reconnect payload arrived")
		}
		t.Logf("post-kill payload: %q", payload)
	case <-time.After(15 * time.Second):
		t.Fatalf("no payload received within 15s of backend kill (reconnectOK=%d, reconnectFail=%d)",
			reconnectOK.Load(), reconnectFail.Load())
	}

	// Phase 2: explicit Notify must flow through the re-established
	// LISTEN. Retry once: the kill may have racing-killed our gorm pool
	// too, and the first Notify call will reconnect transparently but
	// might fail the very first attempt depending on driver behavior.
	var notifyErr error
	for attempt := 0; attempt < 3; attempt++ {
		notifyErr = s.Notify(context.Background(), channel, "post-reconnect")
		if notifyErr == nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if notifyErr != nil {
		t.Fatalf("post-reconnect Notify after retries: %v", notifyErr)
	}

	// One of the next payloads should be ours; tolerate up to a few
	// queued synthetic payloads from extra reconnects.
	got := false
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
DRAIN:
	for {
		select {
		case payload, ok := <-listener.Notifications():
			if !ok {
				break DRAIN
			}
			t.Logf("post-reconnect payload: %q", payload)
			if payload == "post-reconnect" {
				got = true
				break DRAIN
			}
		case <-deadline.C:
			break DRAIN
		}
	}
	if !got {
		t.Fatal("post-reconnect Notify never arrived on the listener — LISTEN was not re-established")
	}

	if reconnectOK.Load() == 0 {
		t.Errorf("expected at least one OnListenerReconnect(success=true), got 0 (failures=%d)", reconnectFail.Load())
	}
}

// killAllOtherBackends opens a side pgx connection and runs
// pg_terminate_backend on every backend that's not us. This nukes both
// the gorm pool's idle conns and the listener pool's persistent conn —
// both reconnect transparently on next access (gorm via its driver,
// listener via the readerLoop reconnect path under test).
func killAllOtherBackends(t *testing.T, dsn string) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return err
	}
	defer conn.Close(context.Background())

	rows, err := conn.Query(ctx, `
		SELECT pid FROM pg_stat_activity
		WHERE pid <> pg_backend_pid()
		AND backend_type = 'client backend'
	`)
	if err != nil {
		return err
	}
	var pids []int32
	for rows.Next() {
		var pid int32
		if err := rows.Scan(&pid); err != nil {
			rows.Close()
			return err
		}
		pids = append(pids, pid)
	}
	rows.Close()
	for _, pid := range pids {
		if _, err := conn.Exec(ctx, "SELECT pg_terminate_backend($1)", pid); err != nil {
			t.Logf("pg_terminate_backend(%d): %v (ignoring)", pid, err)
		}
	}
	t.Logf("terminated %d backend(s)", len(pids))
	return nil
}

// drainNotifications non-blocking-pulls every payload currently sitting
// in the listener chan. Used between phases so the next select sees
// only post-event payloads.
func drainNotifications(ch <-chan string) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

// silence unused import warnings if sync is conditionally referenced.
var _ = sync.WaitGroup{}
