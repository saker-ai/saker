package pipeline_test

import (
	"context"
	"testing"

	"github.com/cinience/saker/pkg/api"
	"github.com/cinience/saker/pkg/pipeline"
	"github.com/stretchr/testify/require"
)

func TestError_NilRuntime(t *testing.T) {
	var rt *api.Runtime
	_, err := rt.Run(context.Background(), api.Request{
		Pipeline: &pipeline.Step{Name: "echo", Tool: "echo"},
	})
	require.Error(t, err)
}

func TestError_ToolNotFound(t *testing.T) {
	rt := newTestRuntime(t)
	err := runPipelineErr(t, rt, pipeline.Step{
		Name: "missing",
		Tool: "this-tool-does-not-exist",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "this-tool-does-not-exist")
}

func TestError_EmptyPipeline(t *testing.T) {
	rt := newTestRuntime(t)
	// A step with no tool/skill/batch/fanout/etc should error
	err := runPipelineErr(t, rt, pipeline.Step{
		Name: "empty",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "no executable target")
}

func TestError_NilCheckpointStore(t *testing.T) {
	rt := newTestRuntime(t) // no checkpoint store
	_, err := rt.Run(context.Background(), api.Request{
		ResumeFromCheckpoint: "some-checkpoint-id",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "checkpoint")
}

func TestError_FailAlwaysTool(t *testing.T) {
	rt := newTestRuntime(t)
	err := runPipelineErr(t, rt, pipeline.Step{
		Name: "will-fail",
		Tool: "fail-always",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "fail-always")
}

func TestError_ContextCancelled(t *testing.T) {
	rt := newTestRuntime(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately
	_, err := rt.Run(ctx, api.Request{
		Pipeline: &pipeline.Step{
			Name: "cancelled",
			Tool: "slow",
			With: map[string]any{"ms": float64(5000)},
		},
	})
	require.Error(t, err)
}
