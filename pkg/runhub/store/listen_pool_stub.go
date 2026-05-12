//go:build !postgres

// Stub for the shared LISTEN pool used on builds without the postgres
// tag. The pool fields exist on Store regardless so the always-built
// store.Close path is the same; this file only provides the no-op
// poolShutdown implementation.

package store

// poolShutdown is a no-op on non-postgres builds. The pool itself is
// only constructed by ensurePool inside the postgres-tagged file, so
// listenState is always nil here.
func (s *Store) poolShutdown() {}
