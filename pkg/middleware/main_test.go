package middleware

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain wraps the test suite with goleak so any goroutine leaked by a
// middleware test (trace recorders, OTel exporters, rate limiters, etc.) is
// surfaced as a CI failure rather than silently piling up.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m,
		// OTel SDK background batch span processor goroutine.
		goleak.IgnoreTopFunction("go.opentelemetry.io/otel/sdk/trace.(*batchSpanProcessor).processQueue"),
		// OTel SDK metric reader background loop, if registered.
		goleak.IgnoreTopFunction("go.opentelemetry.io/otel/sdk/metric.(*PeriodicReader).run"),
	)
}
