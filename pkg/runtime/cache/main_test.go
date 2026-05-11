package cache

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain wraps the test suite with goleak to catch any goroutine leaked by
// the cache and checkpoint stores (background flushers, eviction loops).
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
