//go:build !postgres

// Stub implementation used when the binary is built without `-tags
// postgres`. Listen is a hard error so callers don't silently fall
// back to single-process behavior on a postgres-style DSN.

package store

import (
	"context"
	"errors"
)

// ErrListenUnsupported is returned by Listen on builds without `-tags
// postgres`. PersistentHub treats it as "no cross-process fan-out
// available; rely on poll-on-Get". Surfacing the error (vs returning a
// nil channel) keeps the operator informed.
var ErrListenUnsupported = errors.New("runhub/store: postgres LISTEN not built; rebuild with -tags postgres")

// Listener is the stub Listener handle. Methods are no-ops.
type Listener struct{}

// Listen always fails on non-postgres builds.
func (s *Store) Listen(ctx context.Context, channel string) (*Listener, error) {
	return nil, ErrListenUnsupported
}

// Notifications returns a nil channel; ranging over it blocks forever
// (consistent with "no notifications will arrive on this build").
func (l *Listener) Notifications() <-chan string { return nil }

// Close is a no-op on the stub.
func (l *Listener) Close() error { return nil }
