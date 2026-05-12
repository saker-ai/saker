//go:build !postgres

// Stub for the COPY-based bulk insert path on builds without the
// postgres tag. Both helpers are no-ops so InsertEventsBatch always
// falls through to the prepared multi-row INSERT path. The pgxState
// field on Store stays nil, so pgxPoolShutdown has nothing to do.

package store

import "context"

// tryCopyInsertEvents always returns (false, nil) on non-postgres
// builds so InsertEventsBatch falls through to the prepared multi-row
// INSERT. The driver check inside the postgres-tagged version makes
// this functionally equivalent to "the driver isn't postgres" — kept
// as a separate stub so the builds don't need to compile pgxpool.
func (s *Store) tryCopyInsertEvents(ctx context.Context, rows []EventRow) (bool, error) {
	return false, nil
}

// pgxPoolShutdown is a no-op on non-postgres builds. The pool itself is
// only constructed by ensurePgxPool inside the postgres-tagged file, so
// pgxState is always nil here.
func (s *Store) pgxPoolShutdown() {}
