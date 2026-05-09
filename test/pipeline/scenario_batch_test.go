package pipeline_test

import (
	"testing"

	"github.com/cinience/saker/pkg/pipeline"
	"github.com/stretchr/testify/require"
)

func TestBatch_SequentialExecution(t *testing.T) {
	ct := &counterTool{}
	rt := newTestRuntime(t, withTools(echoTool{}, ct))
	resp := runPipeline(t, rt, pipeline.Step{
		Batch: &pipeline.Batch{
			Steps: []pipeline.Step{
				{Name: "first", Tool: "counter"},
				{Name: "second", Tool: "counter"},
				{Name: "third", Tool: "counter"},
			},
		},
	})
	// Last step output should be "3"
	requireOutput(t, resp, "3")
	require.Equal(t, int64(3), ct.count.Load())
}

func TestBatch_ArtifactPropagation(t *testing.T) {
	rt := newTestRuntime(t)
	resp := runPipeline(t, rt, pipeline.Step{
		Batch: &pipeline.Batch{
			Steps: []pipeline.Step{
				{
					Name: "gen-artifacts",
					Tool: "artifact-gen",
					With: map[string]any{"count": float64(2)},
				},
				{
					Name: "upper-first",
					Tool: "upper",
					// upper reads from passed-in artifacts (propagated from gen-artifacts)
				},
			},
		},
	})
	// upper should have processed the first artifact from gen
	requireNotInterrupted(t, resp)
	require.NotNil(t, resp.Result)
}

func TestBatch_OutputOverwrite(t *testing.T) {
	rt := newTestRuntime(t)
	resp := runPipeline(t, rt, pipeline.Step{
		Batch: &pipeline.Batch{
			Steps: []pipeline.Step{
				{Name: "step1", Tool: "echo", With: map[string]any{"text": "first"}},
				{Name: "step2", Tool: "echo", With: map[string]any{"text": "second"}},
				{Name: "step3", Tool: "echo", With: map[string]any{"text": "final"}},
			},
		},
	})
	requireOutput(t, resp, "final")
}

func TestBatch_MidStepError(t *testing.T) {
	rt := newTestRuntime(t)
	err := runPipelineErr(t, rt, pipeline.Step{
		Batch: &pipeline.Batch{
			Steps: []pipeline.Step{
				{Name: "ok", Tool: "echo", With: map[string]any{"text": "ok"}},
				{Name: "fail", Tool: "fail-always"},
				{Name: "unreachable", Tool: "echo", With: map[string]any{"text": "never"}},
			},
		},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "fail-always")
}

func TestBatch_Empty(t *testing.T) {
	rt := newTestRuntime(t)
	resp := runPipeline(t, rt, pipeline.Step{
		Batch: &pipeline.Batch{Steps: []pipeline.Step{}},
	})
	requireNotInterrupted(t, resp)
}
