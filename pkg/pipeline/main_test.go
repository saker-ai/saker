package pipeline

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain wraps the test suite with goleak to catch any goroutine leaked by
// the pipeline executor (audio extractor ffmpeg subprocesses, frame
// processors, go2rtc sources).
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m,
		// go2rtc maintains a global producer registry that spawns long-lived
		// reader/writer goroutines for active streams; tests do not always
		// fully tear these down, and they are bound to the package lifetime.
		goleak.IgnoreTopFunction("github.com/AlexxIT/go2rtc/pkg/core.(*Producer).Start"),
	)
}
