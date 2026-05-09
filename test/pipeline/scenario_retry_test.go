package pipeline_test

import (
	"context"
	"testing"
	"time"

	"github.com/cinience/saker/pkg/pipeline"
	"github.com/stretchr/testify/require"
)

func TestRetry_SuccessOnThirdAttempt(t *testing.T) {
	fn := &failNTool{failCount: 2}
	rt := newTestRuntime(t, withTools(fn))
	resp := runPipeline(t, rt, pipeline.Step{
		Retry: &pipeline.Retry{
			Attempts: 5,
			Step:     pipeline.Step{Name: "retry-me", Tool: "fail-n"},
		},
	})
	requireOutput(t, resp, "ok-after-2-failures")
	require.Equal(t, int64(3), fn.calls.Load(), "should have taken 3 attempts")
}

func TestRetry_ExhaustedReturnsError(t *testing.T) {
	rt := newTestRuntime(t)
	err := runPipelineErr(t, rt, pipeline.Step{
		Retry: &pipeline.Retry{
			Attempts: 3,
			Step:     pipeline.Step{Name: "doomed", Tool: "fail-always"},
		},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "fail-always")
}

func TestRetry_BackoffTiming(t *testing.T) {
	fn := &failNTool{failCount: 2}
	rt := newTestRuntime(t, withTools(fn))
	start := time.Now()
	resp := runPipeline(t, rt, pipeline.Step{
		Retry: &pipeline.Retry{
			Attempts:  5,
			BackoffMs: 50,
			Step:      pipeline.Step{Name: "backoff", Tool: "fail-n"},
		},
	})
	elapsed := time.Since(start)
	requireOutput(t, resp, "ok-after-2-failures")
	// Linear backoff: attempt 1 = 50ms, attempt 2 = 100ms → total >= 150ms
	require.GreaterOrEqual(t, elapsed.Milliseconds(), int64(100), "backoff should add delay")
}

func TestRetry_ContextCancellation(t *testing.T) {
	rt := newTestRuntime(t)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := rt.Run(ctx, api_request_pipeline(pipeline.Step{
		Retry: &pipeline.Retry{
			Attempts:  100,
			BackoffMs: 100,
			Step:      pipeline.Step{Name: "forever", Tool: "fail-always"},
		},
	}))
	require.Error(t, err)
}

func TestRetry_ZeroBackoff(t *testing.T) {
	fn := &failNTool{failCount: 2}
	rt := newTestRuntime(t, withTools(fn))
	start := time.Now()
	resp := runPipeline(t, rt, pipeline.Step{
		Retry: &pipeline.Retry{
			Attempts:  5,
			BackoffMs: 0,
			Step:      pipeline.Step{Name: "instant", Tool: "fail-n"},
		},
	})
	elapsed := time.Since(start)
	requireOutput(t, resp, "ok-after-2-failures")
	require.Less(t, elapsed.Milliseconds(), int64(500), "zero backoff should be fast")
}
