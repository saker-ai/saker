package sessiondb

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain wraps the test suite with goleak to catch any goroutine leaked by
// the session database (sqlite/postgres connection pools).
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m,
		// glebarez/sqlite WAL writer is a long-lived background goroutine that
		// only stops when the process exits. It does not leak between tests.
		goleak.IgnoreTopFunction("github.com/glebarez/sqlite.(*sqliteConnPool).asyncWriteWAL"),
		// modernc.org/sqlite (used by glebarez/go-sqlite) keeps a worker pool.
		goleak.IgnoreTopFunction("modernc.org/libc.startWorker"),
	)
}
