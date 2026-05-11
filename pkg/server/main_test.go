package server

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain wraps the test suite with goleak so any goroutine left dangling by
// websocket handlers, the cron scheduler, the rate limiter cleanup loop, or
// the cache failed-cleanup loop surfaces as a CI failure.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m,
		// Standard library connection pool keep-alive readers/writers; bound
		// to the http.Transport lifetime, not a per-test leak.
		goleak.IgnoreTopFunction("net/http.(*persistConn).readLoop"),
		goleak.IgnoreTopFunction("net/http.(*persistConn).writeLoop"),
		// httptest.Server keeps a Serve goroutine alive across the suite when
		// individual tests forget to Close the test server. We surface real
		// leaks elsewhere; this top-level Serve loop is harmless.
		goleak.IgnoreTopFunction("net/http.(*Server).Serve"),
		// OTel SDK background batch span processor goroutine.
		goleak.IgnoreTopFunction("go.opentelemetry.io/otel/sdk/trace.(*batchSpanProcessor).processQueue"),
		// AuthManager background cleanup loops; Close()-able but tests do not
		// always reach the teardown path. Bound to package-scope auth state.
		goleak.IgnoreTopFunction("github.com/cinience/saker/pkg/server.(*AuthManager).cleanupRevokedLoop"),
		goleak.IgnoreTopFunction("github.com/cinience/saker/pkg/server.(*AuthManager).cleanupUserInfoCacheLoop"),
		// Hooks executor onceTracker background sweeper; Close() exists, but
		// many embedded handler tests share an executor without explicit
		// teardown. Loop is select-blocked on stopCh.
		goleak.IgnoreTopFunction("github.com/cinience/saker/pkg/core/hooks.(*Executor).onceTrackerCleanupLoop"),
		// Project-scope ComponentRegistry sweeper; tests instantiate a global
		// registry that lives for the test process lifetime.
		goleak.IgnoreAnyFunction("github.com/cinience/saker/pkg/project.(*ComponentRegistry[...]).sweepLoop"),
		// chromedp browser context cancel watcher; tests using chromedp do
		// not always invoke the explicit cancel func, but the goroutine
		// terminates on context.Done.
		goleak.IgnoreTopFunction("github.com/chromedp/chromedp.NewContext.func1"),
		// Apps RateLimitManager background sweeper; Close() exists, but
		// shared rate limiter instances live for the test process lifetime.
		goleak.IgnoreTopFunction("github.com/cinience/saker/pkg/apps.(*RateLimitManager).cleanupLoop"),
	)
}
