package aigo

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain wraps the test suite with goleak to catch any goroutine leaked by
// the aigo task store (background workers).
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
