package store

import "context"

// Notify sends a payload on a postgres channel via pg_notify. On
// non-postgres drivers it returns nil silently — single-process
// runhubs (sqlite, in-memory) don't need cross-process fan-out, so a
// best-effort no-op keeps the call site driver-agnostic.
//
// Channel and payload are passed as parameters to pg_notify (which
// quotes them server-side), so callers don't have to escape them. The
// payload is capped at ~8000 bytes by Postgres; we don't enforce that
// here — runhub callers send only the seq number, well below the cap.
func (s *Store) Notify(ctx context.Context, channel, payload string) error {
	if s.driver != "postgres" {
		return nil
	}
	return s.db.WithContext(ctx).Exec("SELECT pg_notify(?, ?)", channel, payload).Error
}
