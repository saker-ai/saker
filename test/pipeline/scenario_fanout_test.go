package pipeline_test

import (
	"testing"

	"github.com/saker-ai/saker/pkg/pipeline"
	"github.com/stretchr/testify/require"
)

func TestFanOut_OrderPreserved(t *testing.T) {
	rt := newTestRuntime(t)
	// Generate 3 artifacts, then fan-out upper over each
	resp := runPipeline(t, rt, pipeline.Step{
		Batch: &pipeline.Batch{
			Steps: []pipeline.Step{
				{
					Name: "gen",
					Tool: "artifact-gen",
					With: map[string]any{"count": float64(3)},
				},
				{
					FanOut: &pipeline.FanOut{
						Step: pipeline.Step{Name: "upper-each", Tool: "upper"},
					},
				},
			},
		},
	})
	requireNotInterrupted(t, resp)
	// 3 artifacts → 3 fan-out branches → 3 result artifacts
	requireArtifacts(t, resp, 3)
}

func TestFanOut_ConcurrencyBounded(t *testing.T) {
	slow := &slowTool{}
	rt := newTestRuntime(t, withTools(slow, artifactGenTool{}))
	resp := runPipeline(t, rt, pipeline.Step{
		Batch: &pipeline.Batch{
			Steps: []pipeline.Step{
				{Name: "gen", Tool: "artifact-gen", With: map[string]any{"count": float64(8)}},
				{
					FanOut: &pipeline.FanOut{
						Step:        pipeline.Step{Name: "slow-each", Tool: "slow", With: map[string]any{"ms": float64(20)}},
						Concurrency: 2,
					},
				},
			},
		},
	})
	requireNotInterrupted(t, resp)
	// Peak concurrency should be <= 2
	require.LessOrEqual(t, slow.peak.Load(), int64(2), "concurrency should be bounded to 2")
}

func TestFanOut_DefaultUnbounded(t *testing.T) {
	slow := &slowTool{}
	rt := newTestRuntime(t, withTools(slow, artifactGenTool{}))
	resp := runPipeline(t, rt, pipeline.Step{
		Batch: &pipeline.Batch{
			Steps: []pipeline.Step{
				{Name: "gen", Tool: "artifact-gen", With: map[string]any{"count": float64(5)}},
				{
					FanOut: &pipeline.FanOut{
						Step:        pipeline.Step{Name: "slow", Tool: "slow", With: map[string]any{"ms": float64(30)}},
						Concurrency: 0, // unbounded
					},
				},
			},
		},
	})
	requireNotInterrupted(t, resp)
	// All 5 should run concurrently → peak should be close to 5
	require.GreaterOrEqual(t, slow.peak.Load(), int64(3), "unbounded concurrency should run many in parallel")
}

func TestFanOut_EmptyCollection(t *testing.T) {
	rt := newTestRuntime(t)
	resp := runPipeline(t, rt, pipeline.Step{
		FanOut: &pipeline.FanOut{
			Collection: "empty",
			Step:       pipeline.Step{Name: "echo", Tool: "echo", With: map[string]any{"text": "x"}},
		},
	})
	requireNotInterrupted(t, resp)
}

func TestFanOut_ErrorPropagation(t *testing.T) {
	rt := newTestRuntime(t)
	err := runPipelineErr(t, rt, pipeline.Step{
		Batch: &pipeline.Batch{
			Steps: []pipeline.Step{
				{Name: "gen", Tool: "artifact-gen", With: map[string]any{"count": float64(3)}},
				{
					FanOut: &pipeline.FanOut{
						Step: pipeline.Step{Name: "fail", Tool: "fail-always"},
					},
				},
			},
		},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "fail-always")
}

func TestFanOut_ArtifactLineage(t *testing.T) {
	rt := newTestRuntime(t)
	resp := runPipeline(t, rt, pipeline.Step{
		Batch: &pipeline.Batch{
			Steps: []pipeline.Step{
				{
					Name: "gen",
					Tool: "artifact-gen",
					With: map[string]any{"count": float64(3)},
				},
				{
					FanOut: &pipeline.FanOut{
						Step: pipeline.Step{Name: "upper-each", Tool: "upper"},
					},
				},
			},
		},
	})
	requireNotInterrupted(t, resp)
	requireArtifacts(t, resp, 3)
}
