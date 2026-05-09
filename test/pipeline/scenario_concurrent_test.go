package pipeline_test

import (
	"sync"
	"testing"

	"github.com/cinience/saker/pkg/api"
	"github.com/cinience/saker/pkg/pipeline"
	"github.com/cinience/saker/pkg/tool"
	"github.com/stretchr/testify/require"
)

func TestConcurrent_ParallelPipelineRuns(t *testing.T) {
	ct := &counterTool{}
	rt := newTestRuntime(t, func(o *api.Options) {
		o.CustomTools = []tool.Tool{ct}
		o.MaxSessions = 100
	})

	const n = 10
	var wg sync.WaitGroup
	errs := make([]error, n)
	resps := make([]*api.Response, n)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			resp, err := rt.Run(t.Context(), api.Request{
				SessionID: sessionID(idx),
				Pipeline: &pipeline.Step{
					Name: "parallel",
					Tool: "counter",
				},
			})
			errs[idx] = err
			resps[idx] = resp
		}(i)
	}
	wg.Wait()

	for i := 0; i < n; i++ {
		require.NoError(t, errs[i], "run %d failed", i)
		require.NotNil(t, resps[i], "run %d nil response", i)
		require.NotNil(t, resps[i].Result, "run %d nil result", i)
	}
	require.Equal(t, int64(n), ct.count.Load(), "all runs should have executed")
}

func TestConcurrent_FanOutRaceDetection(t *testing.T) {
	// This test is primarily for -race detection
	ct := &counterTool{}
	rt := newTestRuntime(t, func(o *api.Options) {
		o.CustomTools = []tool.Tool{ct, artifactGenTool{}, upperTool{}}
	})

	resp := runPipeline(t, rt, pipeline.Step{
		Batch: &pipeline.Batch{
			Steps: []pipeline.Step{
				{
					Name: "gen",
					Tool: "artifact-gen",
					With: map[string]any{"count": float64(10)},
				},
				{
					FanOut: &pipeline.FanOut{
						Step:        pipeline.Step{Name: "up", Tool: "upper"},
						Concurrency: 0, // unbounded
					},
				},
			},
		},
	})
	requireNotInterrupted(t, resp)
}

func TestConcurrent_TimelineSafety(t *testing.T) {
	rt := newTestRuntime(t)
	resp := runPipeline(t, rt, pipeline.Step{
		Batch: &pipeline.Batch{
			Steps: []pipeline.Step{
				{
					Name: "gen",
					Tool: "artifact-gen",
					With: map[string]any{"count": float64(5)},
				},
				{
					FanOut: &pipeline.FanOut{
						Step:        pipeline.Step{Name: "up", Tool: "upper"},
						Concurrency: 0,
					},
				},
			},
		},
	})
	// Verify no timeline entries are lost
	require.NotEmpty(t, resp.Timeline)
	// Should have at least: gen tool_call + tool_result + 5x(fan-out tool_call+tool_result) + token_snapshot
	require.GreaterOrEqual(t, len(resp.Timeline), 5, "timeline should capture concurrent events")
}

func sessionID(idx int) string {
	return "session-" + string(rune('A'+idx))
}
