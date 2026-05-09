package pipeline_test

import (
	"testing"

	"github.com/cinience/saker/pkg/api"
	"github.com/cinience/saker/pkg/artifact"
	"github.com/cinience/saker/pkg/pipeline"
	"github.com/stretchr/testify/require"
)

func TestTimeline_SingleStepEvents(t *testing.T) {
	rt := newTestRuntime(t)
	resp := runPipeline(t, rt, pipeline.Step{
		Name: "tl-single",
		Tool: "artifact-gen",
		With: map[string]any{"count": float64(1)},
		Input: []artifact.ArtifactRef{
			artifact.NewGeneratedRef("input-1", artifact.ArtifactKindText),
		},
	})
	requireTimeline(t, resp, "input_artifact", "tool_call", "tool_result", "generated_artifact", "token_snapshot")
}

func TestTimeline_BatchEvents(t *testing.T) {
	rt := newTestRuntime(t)
	resp := runPipeline(t, rt, pipeline.Step{
		Batch: &pipeline.Batch{
			Steps: []pipeline.Step{
				{Name: "s1", Tool: "echo", With: map[string]any{"text": "a"}},
				{Name: "s2", Tool: "echo", With: map[string]any{"text": "b"}},
			},
		},
	})
	// Should have tool_call + tool_result for each step
	toolCalls := 0
	toolResults := 0
	for _, e := range resp.Timeline {
		switch e.Kind {
		case "tool_call":
			toolCalls++
		case "tool_result":
			toolResults++
		}
	}
	require.Equal(t, 2, toolCalls, "should have 2 tool_call events")
	require.Equal(t, 2, toolResults, "should have 2 tool_result events")
}

func TestTimeline_CacheEvents(t *testing.T) {
	rt := newTestRuntime(t, withCache())
	step := pipeline.Step{Name: "cache-tl", Tool: "echo", With: map[string]any{"text": "cached"}}

	resp1 := runPipeline(t, rt, step)
	requireTimeline(t, resp1, "cache_miss")

	resp2 := runPipeline(t, rt, step)
	requireTimeline(t, resp2, "cache_hit")
}

func TestTimeline_CheckpointEvents(t *testing.T) {
	rt := newTestRuntime(t, withCheckpoint())
	const sid = "tl-cp-session"

	resp, err := rt.Run(t.Context(), api.Request{
		SessionID: sid,
		Pipeline: &pipeline.Step{
			Checkpoint: &pipeline.Checkpoint{
				Name: "tl-cp",
				Step: pipeline.Step{Name: "echo", Tool: "echo", With: map[string]any{"text": "cp"}},
			},
		},
	})
	require.NoError(t, err)
	requireTimeline(t, resp, "checkpoint_create")

	// Resume
	resp2, err := rt.Run(t.Context(), api.Request{
		SessionID:            sid,
		ResumeFromCheckpoint: resp.Result.CheckpointID,
	})
	require.NoError(t, err)
	requireTimeline(t, resp2, "checkpoint_resume")
}

func TestTimeline_Timestamps(t *testing.T) {
	rt := newTestRuntime(t)
	resp := runPipeline(t, rt, pipeline.Step{
		Batch: &pipeline.Batch{
			Steps: []pipeline.Step{
				{Name: "s1", Tool: "echo", With: map[string]any{"text": "a"}},
				{Name: "s2", Tool: "echo", With: map[string]any{"text": "b"}},
			},
		},
	})
	require.NotEmpty(t, resp.Timeline)
	for i, e := range resp.Timeline {
		require.False(t, e.Timestamp.IsZero(), "entry %d (%s) should have non-zero timestamp", i, e.Kind)
	}
	// Verify timestamps are non-decreasing
	for i := 1; i < len(resp.Timeline); i++ {
		require.False(t, resp.Timeline[i].Timestamp.Before(resp.Timeline[i-1].Timestamp),
			"timestamps should be non-decreasing at index %d", i)
	}
}
