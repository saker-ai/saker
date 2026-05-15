package pipeline_test

import (
	"context"
	"testing"

	"github.com/saker-ai/saker/pkg/api"
	"github.com/saker-ai/saker/pkg/artifact"
	"github.com/saker-ai/saker/pkg/pipeline"
	"github.com/stretchr/testify/require"
)

func TestSingleStep_ToolExecution(t *testing.T) {
	rt := newTestRuntime(t)
	resp := runPipeline(t, rt, pipeline.Step{
		Name: "echo-hello",
		Tool: "echo",
		With: map[string]any{"text": "hello world"},
	})
	requireOutput(t, resp, "hello world")
	requireNotInterrupted(t, resp)
}

func TestSingleStep_WithInputArtifacts(t *testing.T) {
	rt := newTestRuntime(t)
	resp := runPipeline(t, rt, pipeline.Step{
		Name: "upper-input",
		Tool: "upper",
		Input: []artifact.ArtifactRef{
			artifact.NewGeneratedRef("test-id", artifact.ArtifactKindText),
		},
	})
	requireOutput(t, resp, "TEST-ID")
	requireArtifacts(t, resp, 1)
}

func TestSingleStep_UnknownTool(t *testing.T) {
	rt := newTestRuntime(t)
	err := runPipelineErr(t, rt, pipeline.Step{
		Name: "unknown",
		Tool: "nonexistent-tool",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "nonexistent-tool")
}

func TestSingleStep_NilPipeline(t *testing.T) {
	rt := newTestRuntime(t)
	_, err := rt.Run(context.Background(), api.Request{})
	require.Error(t, err)
}

func TestSingleStep_ArtifactGeneration(t *testing.T) {
	rt := newTestRuntime(t)
	resp := runPipeline(t, rt, pipeline.Step{
		Name: "gen",
		Tool: "artifact-gen",
		With: map[string]any{"count": float64(3)},
	})
	requireArtifacts(t, resp, 3)
}

func TestSingleStep_TimelineEvents(t *testing.T) {
	rt := newTestRuntime(t)
	resp := runPipeline(t, rt, pipeline.Step{
		Name: "echo-tl",
		Tool: "echo",
		With: map[string]any{"text": "timeline"},
	})
	requireTimeline(t, resp, "tool_call", "tool_result", "token_snapshot")
}
