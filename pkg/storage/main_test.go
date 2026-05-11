package storage

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain wraps the test suite with goleak to catch any goroutine leaked by
// the storage backends (s3 multipart uploaders, osfs background tasks).
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m,
		// HTTP keep-alive connections persist across requests; not a leak.
		goleak.IgnoreTopFunction("net/http.(*persistConn).readLoop"),
		goleak.IgnoreTopFunction("net/http.(*persistConn).writeLoop"),
	)
}
