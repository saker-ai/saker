//go:build postgres

// Postgres LISTEN/NOTIFY support. Built only when the binary is
// compiled with `-tags postgres` so the default build keeps pgx out of
// the link path. As of Stage B all per-run Listeners share a single
// pgx.Conn via listenPool, so a hub serving N concurrent runs holds 1
// pgx connection instead of N. The Listener type stays as a thin
// handle (Notifications + Close) so existing call sites (PersistentHub)
// don't change.
//
// Fan-out semantics:
//   - Each Listener subscribes to a logical channel via the shared pool.
//   - Notifications are delivered on a buffered channel; slow consumers
//     can miss notifications, but the consumer always falls back to
//     polling LoadEventsSince so missed notifications only delay
//     delivery, never lose data.
//   - Close removes the Listener from the pool; when the last Listener
//     for a channel goes away, the pool issues UNLISTEN.
//   - On a dropped pgx connection the pool reconnects (exp backoff
//     ±20% jitter, cap 30s) and pushes a synthetic payload to every
//     subscriber so they re-issue LoadEventsSince and pick up anything
//     that fired during the outage.

package store

import (
	"context"
	"errors"
	"strings"
)

// Listener is an active LISTEN handle on a Postgres channel. Backed by
// a subscription to the Store's shared listenPool — multiple Listeners
// for distinct (or even the same) channel share one pgx.Conn.
type Listener struct {
	notifications <-chan string
	unsub         func()
	channel       string
}

// Listen subscribes to a postgres notification channel via the shared
// pool. The returned Listener exposes the same Notifications/Close API
// as the legacy per-conn implementation; PersistentHub doesn't need
// to know about the pool.
//
// Caller MUST call Close to release the subscription. Listen returns
// ErrListenUnsupported on non-postgres builds (handled by listen_stub.go).
func (s *Store) Listen(ctx context.Context, channel string) (*Listener, error) {
	if s.driver != "postgres" {
		return nil, errors.New("runhub/store: Listen requires postgres driver, got " + s.driver)
	}
	if strings.TrimSpace(channel) == "" {
		return nil, errors.New("runhub/store: Listen requires a non-empty channel name")
	}
	pool, err := s.ensurePool()
	if err != nil {
		return nil, err
	}
	notifications, unsub, err := pool.subscribe(channel)
	if err != nil {
		return nil, err
	}
	return &Listener{
		notifications: notifications,
		unsub:         unsub,
		channel:       channel,
	}, nil
}

// Notifications returns the channel of payloads received from the pool.
// Closed when Close runs or when the pool shuts down.
func (l *Listener) Notifications() <-chan string { return l.notifications }

// Close removes the subscription from the pool. Idempotent: calling
// twice is safe (the second call is a no-op because unsub is set to nil).
func (l *Listener) Close() error {
	if l.unsub == nil {
		return nil
	}
	unsub := l.unsub
	l.unsub = nil
	unsub()
	return nil
}
