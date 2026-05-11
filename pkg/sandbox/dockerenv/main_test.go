package dockerenv

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain wraps the test suite with goleak to catch any goroutine leaked by
// docker exec lifecycle helpers.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
