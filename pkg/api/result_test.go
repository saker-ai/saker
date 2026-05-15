package api

import (
	"context"
	"testing"

	"github.com/saker-ai/saker/pkg/agent"
	"github.com/saker-ai/saker/pkg/artifact"
	"github.com/saker-ai/saker/pkg/model"
	"github.com/saker-ai/saker/pkg/pipeline"
	"github.com/saker-ai/saker/pkg/tool"
)

func TestResultOutputRemainsFinalTextAnswer(t *testing.T) {
	got := convertRunResult(runResult{
		output: &agent.ModelOutput{Content: "final answer"},
		usage:  model.Usage{InputTokens: 1, OutputTokens: 2, TotalTokens: 3},
		reason: "stop",
	})
	if got == nil {
		t.Fatal("expected result")
	}
	if got.Output != "final answer" {
		t.Fatalf("expected text output to remain in Output, got %+v", got)
	}
	if got.Structured != nil {
		t.Fatalf("expected Structured to stay empty for text run, got %+v", got.Structured)
	}
	if len(got.Artifacts) != 0 {
		t.Fatalf("expected Artifacts to stay empty for text run, got %+v", got.Artifacts)
	}
}

func TestResultStopReasonPrecedence(t *testing.T) {
	cases := []struct {
		name         string
		providerStop string
		agentStop    agent.StopReason
		want         string
	}{
		{
			name:         "agent_max_budget_overrides_provider",
			providerStop: "tool_use",
			agentStop:    agent.StopReasonMaxBudget,
			want:         "max_budget",
		},
		{
			name:         "agent_max_tokens_overrides_provider",
			providerStop: "end_turn",
			agentStop:    agent.StopReasonMaxTokens,
			want:         "max_tokens",
		},
		{
			name:         "agent_max_iterations_overrides_provider",
			providerStop: "stop",
			agentStop:    agent.StopReasonMaxIterations,
			want:         "max_iterations",
		},
		{
			name:         "completed_yields_to_provider_for_back_compat",
			providerStop: "stop",
			agentStop:    agent.StopReasonCompleted,
			want:         "stop",
		},
		{
			name:         "completed_yields_to_provider_end_turn",
			providerStop: "end_turn",
			agentStop:    agent.StopReasonCompleted,
			want:         "end_turn",
		},
		{
			name:         "agent_only_when_provider_empty",
			providerStop: "",
			agentStop:    agent.StopReasonRepeatLoop,
			want:         "repeat_loop",
		},
		{
			name:         "provider_only_when_agent_empty",
			providerStop: "interrupted",
			agentStop:    "",
			want:         "interrupted",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := convertRunResult(runResult{
				output: &agent.ModelOutput{
					Content:    "ok",
					StopReason: tc.agentStop,
				},
				reason: tc.providerStop,
			})
			if got == nil {
				t.Fatal("expected non-nil result")
			}
			if got.StopReason != tc.want {
				t.Fatalf("StopReason = %q, want %q (provider=%q agent=%q)",
					got.StopReason, tc.want, tc.providerStop, tc.agentStop)
			}
		})
	}
}

func TestResultStructuredAndArtifactsAreSeparatedFromOutput(t *testing.T) {
	root := newClaudeProject(t)
	rt, err := New(context.Background(), Options{
		ProjectRoot: root,
		Model:       &stubModel{},
		CustomTools: []tool.Tool{&pipelineStepTool{outputs: map[string]*tool.ToolResult{
			"finalize": {
				Output:     "final answer",
				Structured: map[string]any{"status": "ok"},
				Artifacts:  []artifact.ArtifactRef{artifact.NewGeneratedRef("art_done", artifact.ArtifactKindDocument)},
			},
		}}},
	})
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })

	resp, err := rt.Run(context.Background(), Request{
		Pipeline: &pipeline.Step{Name: "finalize", Tool: "pipeline_step"},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if resp.Result == nil {
		t.Fatal("expected result")
	}
	if resp.Result.Output != "final answer" {
		t.Fatalf("expected Output to remain final text, got %+v", resp.Result)
	}
	structured, ok := resp.Result.Structured.(map[string]any)
	if !ok || structured["status"] != "ok" {
		t.Fatalf("expected Structured payload to be carried separately, got %+v", resp.Result.Structured)
	}
	if len(resp.Result.Artifacts) != 1 || resp.Result.Artifacts[0].ArtifactID != "art_done" {
		t.Fatalf("expected Artifacts to be carried separately, got %+v", resp.Result.Artifacts)
	}
}
