package pipeline_test

import (
	"testing"

	"github.com/saker-ai/saker/pkg/pipeline"
	"github.com/stretchr/testify/require"
)

func TestConditional_ReturnsNotImplementedError(t *testing.T) {
	rt := newTestRuntime(t)
	err := runPipelineErr(t, rt, pipeline.Step{
		Conditional: &pipeline.Conditional{
			Condition: "always-true",
			Then:      pipeline.Step{Name: "then", Tool: "echo", With: map[string]any{"text": "yes"}},
		},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "conditional")
	require.Contains(t, err.Error(), "not yet supported")
}

func TestConditional_WithElseBranch(t *testing.T) {
	rt := newTestRuntime(t)
	elseBranch := pipeline.Step{Name: "else", Tool: "echo", With: map[string]any{"text": "no"}}
	err := runPipelineErr(t, rt, pipeline.Step{
		Conditional: &pipeline.Conditional{
			Condition: "check",
			Then:      pipeline.Step{Name: "then", Tool: "echo", With: map[string]any{"text": "yes"}},
			Else:      &elseBranch,
		},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "not yet supported")
}
