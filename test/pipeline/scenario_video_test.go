package pipeline_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/saker-ai/saker/pkg/artifact"
	ptemplates "github.com/saker-ai/saker/pkg/pipeline/templates"
	"github.com/saker-ai/saker/pkg/tool"
	"github.com/stretchr/testify/require"
)

// --- stub tools for video pipeline tests ---

type stubVideoSampler struct{}

func (stubVideoSampler) Name() string             { return "video_sampler" }
func (stubVideoSampler) Description() string      { return "stub" }
func (stubVideoSampler) Schema() *tool.JSONSchema { return &tool.JSONSchema{Type: "object"} }
func (stubVideoSampler) Execute(_ context.Context, params map[string]interface{}) (*tool.ToolResult, error) {
	count := 8
	if c, ok := params["count"].(float64); ok && c > 0 {
		count = int(c)
	}
	if c, ok := params["count"].(int); ok && c > 0 {
		count = c
	}
	arts := make([]artifact.ArtifactRef, count)
	for i := range arts {
		arts[i] = artifact.NewGeneratedRef(fmt.Sprintf("frame_%03d", i), artifact.ArtifactKindImage)
	}
	return &tool.ToolResult{
		Output:    fmt.Sprintf("extracted %d frames", count),
		Artifacts: arts,
	}, nil
}

type stubFrameAnalyzer struct{}

func (stubFrameAnalyzer) Name() string             { return "frame_analyzer" }
func (stubFrameAnalyzer) Description() string      { return "stub" }
func (stubFrameAnalyzer) Schema() *tool.JSONSchema { return &tool.JSONSchema{Type: "object"} }
func (stubFrameAnalyzer) Execute(_ context.Context, params map[string]interface{}) (*tool.ToolResult, error) {
	id := "unknown"
	if refs, ok := params["artifacts"].([]artifact.ArtifactRef); ok && len(refs) > 0 {
		id = refs[0].ArtifactID
	}
	return &tool.ToolResult{
		Output: "analysis of " + id,
		Artifacts: []artifact.ArtifactRef{
			artifact.NewGeneratedRef("analysis_"+id, artifact.ArtifactKindJSON),
		},
	}, nil
}

type stubVideoSummarizer struct{}

func (stubVideoSummarizer) Name() string             { return "video_summarizer" }
func (stubVideoSummarizer) Description() string      { return "stub" }
func (stubVideoSummarizer) Schema() *tool.JSONSchema { return &tool.JSONSchema{Type: "object"} }
func (stubVideoSummarizer) Execute(_ context.Context, _ map[string]interface{}) (*tool.ToolResult, error) {
	return &tool.ToolResult{
		Output: "video summary: urban scene with pedestrians",
		Artifacts: []artifact.ArtifactRef{
			artifact.NewGeneratedRef("video_summary", artifact.ArtifactKindJSON),
		},
	}, nil
}

func videoTools() []tool.Tool {
	return []tool.Tool{
		stubVideoSampler{},
		stubFrameAnalyzer{},
		stubVideoSummarizer{},
	}
}

func TestVideoUnderstandingPipeline(t *testing.T) {
	rt := newTestRuntime(t, withTools(videoTools()...))

	step := ptemplates.VideoUnderstanding("/tmp/test.mp4", ptemplates.VideoUnderstandingOptions{
		Strategy:    "uniform",
		FrameCount:  4,
		Concurrency: 2,
	})

	resp := runPipeline(t, rt, step)
	requireOutput(t, resp, "video summary: urban scene with pedestrians")

	// Verify lineage edges exist
	require.NotEmpty(t, resp.Result.Lineage.Edges, "expected lineage edges")
}

func TestVideoUnderstandingTimeline(t *testing.T) {
	rt := newTestRuntime(t, withTools(videoTools()...))

	step := ptemplates.VideoUnderstanding("/tmp/test.mp4", ptemplates.VideoUnderstandingOptions{
		FrameCount:  4,
		Concurrency: 2,
	})

	resp := runPipeline(t, rt, step)
	requireTimeline(t, resp, "tool_call", "tool_result", "latency_snapshot")
}

func TestVideoUnderstandingLineage(t *testing.T) {
	rt := newTestRuntime(t, withTools(videoTools()...))

	step := ptemplates.VideoUnderstanding("/tmp/test.mp4", ptemplates.VideoUnderstandingOptions{
		FrameCount:  3,
		Concurrency: 2,
	})

	resp := runPipeline(t, rt, step)
	require.NotEmpty(t, resp.Result.Lineage.Edges, "expected lineage edges")

	dot := resp.Result.Lineage.ToDOT()
	require.True(t, strings.Contains(dot, "digraph"), "DOT output should contain digraph")
	require.True(t, strings.Contains(dot, "rankdir"), "DOT output should contain rankdir")
}

func TestVideoUnderstandingWithCache(t *testing.T) {
	rt := newTestRuntime(t, withTools(videoTools()...), withCache())

	step := ptemplates.VideoUnderstanding("/tmp/test.mp4", ptemplates.VideoUnderstandingOptions{
		FrameCount:  2,
		Concurrency: 1,
	})

	// First run: all cache misses
	resp1 := runPipeline(t, rt, step)
	requireOutput(t, resp1, "video summary: urban scene with pedestrians")

	// Second run: should get cache hits
	resp2 := runPipeline(t, rt, step)
	requireOutput(t, resp2, "video summary: urban scene with pedestrians")
	requireTimeline(t, resp2, "cache_hit")
}

func TestVideoUnderstandingTemplate(t *testing.T) {
	step := ptemplates.VideoUnderstanding("/path/to/video.mp4", ptemplates.VideoUnderstandingOptions{
		Strategy:     "keyframe",
		FrameCount:   10,
		Concurrency:  8,
		MaxDimension: 512,
		Task:         "detect safety issues",
	})

	require.NotNil(t, step.Batch, "template should produce a batch step")
	require.Len(t, step.Batch.Steps, 4, "batch should have 4 steps: sample, fan-out, fan-in, summarize")

	sampler := step.Batch.Steps[0]
	require.Equal(t, "video_sampler", sampler.Tool)
	require.Equal(t, "keyframe", sampler.With["strategy"])
	require.Equal(t, 10, sampler.With["count"])
	require.Equal(t, 512, sampler.With["max_dimension"])

	fanOut := step.Batch.Steps[1]
	require.NotNil(t, fanOut.FanOut)
	require.Equal(t, 8, fanOut.FanOut.Concurrency)
	require.Equal(t, "frame_analyzer", fanOut.FanOut.Step.Tool)

	fanIn := step.Batch.Steps[2]
	require.NotNil(t, fanIn.FanIn)
	require.Equal(t, "frame_analyses", fanIn.FanIn.Into)

	summarizer := step.Batch.Steps[3]
	require.Equal(t, "video_summarizer", summarizer.Tool)
	require.Equal(t, "detect safety issues", summarizer.With["task"])
}

func TestVideoUnderstandingTemplateDefaults(t *testing.T) {
	step := ptemplates.VideoUnderstanding("/tmp/v.mp4", ptemplates.VideoUnderstandingOptions{})

	require.NotNil(t, step.Batch)
	sampler := step.Batch.Steps[0]
	require.Equal(t, "uniform", sampler.With["strategy"])
	require.Equal(t, 8, sampler.With["count"])

	fanOut := step.Batch.Steps[1]
	require.Equal(t, 4, fanOut.FanOut.Concurrency)
}
