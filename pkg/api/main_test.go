package api

import (
	"os"
	"testing"

	"go.uber.org/goleak"
)

// TestMain isolates HOME so user-specific settings do not leak into tests, and
// wraps the suite with goleak so any goroutine left dangling by an agent,
// sandbox, or model stream test surfaces as a CI failure.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "api-test-home")
	if err != nil {
		panic(err)
	}
	os.Setenv("HOME", home)

	// Run inside a closure so deferred cleanup still executes if goleak
	// signals a leak via os.Exit.
	exitCode := func() int {
		defer func() { _ = os.RemoveAll(home) }()
		// VerifyTestMain handles os.Exit itself; we mirror its semantics by
		// wrapping m.Run() and forwarding the result.
		return goleakRun(m)
	}()
	os.Exit(exitCode)
}

// goleakRun runs the test suite and verifies no goroutines leak. It returns
// the test exit code; on a goroutine leak it returns a non-zero code without
// calling os.Exit so the surrounding cleanup can complete.
func goleakRun(m *testing.M) int {
	code := m.Run()
	if code != 0 {
		return code
	}
	if err := goleak.Find(
		// Standard library connection pool keep-alive readers/writers; bound
		// to the http.Transport lifetime, not a per-test leak.
		goleak.IgnoreTopFunction("net/http.(*persistConn).readLoop"),
		goleak.IgnoreTopFunction("net/http.(*persistConn).writeLoop"),
		// OTel SDK background batch span processor goroutine.
		goleak.IgnoreTopFunction("go.opentelemetry.io/otel/sdk/trace.(*batchSpanProcessor).processQueue"),
		// sessionGate cleanup loop; Close() exists but many runtime tests do
		// not run the full teardown path. Loop is select-blocked on stopCh.
		goleak.IgnoreTopFunction("github.com/saker-ai/saker/pkg/api.(*sessionGate).cleanupLoop"),
		// Hooks executor onceTracker background sweeper; Close() exists, but
		// embedded handler tests share an executor without explicit teardown.
		goleak.IgnoreTopFunction("github.com/saker-ai/saker/pkg/core/hooks.(*Executor).onceTrackerCleanupLoop"),
		// chromedp browser context cancel watcher; terminates on ctx.Done.
		goleak.IgnoreTopFunction("github.com/chromedp/chromedp.NewContext.func1"),
	); err != nil {
		// Match goleak.VerifyTestMain output style.
		_, _ = os.Stderr.WriteString("goleak: errors on successful test run: " + err.Error() + "\n")
		return 1
	}
	return 0
}
