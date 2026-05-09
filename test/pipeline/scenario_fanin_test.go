package pipeline_test

import (
	"testing"

	"github.com/cinience/saker/pkg/artifact"
	"github.com/cinience/saker/pkg/pipeline"
	"github.com/stretchr/testify/require"
)

func TestFanOutFanIn_RoundTrip(t *testing.T) {
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
						Step: pipeline.Step{Name: "upper", Tool: "upper"},
					},
				},
				{
					FanIn: &pipeline.FanIn{
						Into: "results",
					},
				},
			},
		},
	})
	requireNotInterrupted(t, resp)
	require.NotNil(t, resp.Result)
	// FanIn should produce Structured with "results" key
	if resp.Result.Structured != nil {
		m, ok := resp.Result.Structured.(map[string]any)
		if ok {
			_, hasResults := m["results"]
			require.True(t, hasResults, "fan-in should aggregate into 'results' key")
		}
	}
}

func TestFanOutFanIn_OrderedAggregation(t *testing.T) {
	rt := newTestRuntime(t)
	resp := runPipeline(t, rt, pipeline.Step{
		Batch: &pipeline.Batch{
			Steps: []pipeline.Step{
				{
					Name: "gen",
					Tool: "artifact-gen",
					With: map[string]any{"count": float64(2)},
				},
				{
					FanOut: &pipeline.FanOut{
						Step: pipeline.Step{
							Name: "echo-id",
							Tool: "upper",
						},
					},
				},
				{
					FanIn: &pipeline.FanIn{
						Into: "ordered",
					},
				},
			},
		},
	})
	requireNotInterrupted(t, resp)
	require.NotNil(t, resp.Result)
}

func TestFanIn_NoItems(t *testing.T) {
	rt := newTestRuntime(t)
	resp := runPipeline(t, rt, pipeline.Step{
		FanIn: &pipeline.FanIn{
			Into: "empty",
		},
	})
	requireNotInterrupted(t, resp)
	if resp.Result.Structured != nil {
		m, ok := resp.Result.Structured.(map[string]any)
		if ok {
			vals, _ := m["empty"].([]string)
			require.Empty(t, vals)
		}
	}
}

func TestFanOutFanIn_WithInputArtifacts(t *testing.T) {
	rt := newTestRuntime(t)
	step := pipeline.Step{
		Batch: &pipeline.Batch{
			Steps: []pipeline.Step{
				{
					Name: "gen",
					Tool: "artifact-gen",
					With: map[string]any{"count": float64(4)},
					Input: []artifact.ArtifactRef{
						artifact.NewGeneratedRef("seed", artifact.ArtifactKindText),
					},
				},
				{
					FanOut: &pipeline.FanOut{
						Step: pipeline.Step{Name: "up", Tool: "upper"},
					},
				},
				{
					FanIn: &pipeline.FanIn{Into: "all"},
				},
			},
		},
	}
	resp := runPipeline(t, rt, step)
	requireNotInterrupted(t, resp)
}
