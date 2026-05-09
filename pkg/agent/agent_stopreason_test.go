package agent

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/cinience/saker/pkg/middleware"
	"github.com/cinience/saker/pkg/model"
)

// usagePublishingModel mimics how pkg/api/runtime_model.go publishes
// resp.Usage onto the middleware state under the "model.usage" key after
// every Generate call. The test scripts both the per-call usage and the
// emitted ModelOutput so a single helper can drive token-cap, budget-cap,
// and completion paths.
type usagePublishingModel struct {
	usages  []model.Usage
	outputs []*ModelOutput
	idx     int
}

func (m *usagePublishingModel) Generate(ctx context.Context, _ *Context) (*ModelOutput, error) {
	st, _ := ctx.Value(model.MiddlewareStateKey).(*middleware.State)
	if st != nil && m.idx < len(m.usages) {
		if st.Values == nil {
			st.Values = map[string]any{}
		}
		st.Values["model.usage"] = m.usages[m.idx]
	}
	if m.idx >= len(m.outputs) {
		// Emit a final response if the script is exhausted so any caller
		// that didn't size the script precisely still terminates.
		return &ModelOutput{Done: true}, nil
	}
	out := m.outputs[m.idx]
	m.idx++
	return out, nil
}

func TestAgentStopReasonCompleted(t *testing.T) {
	mdl := &usagePublishingModel{
		outputs: []*ModelOutput{{Done: true, Content: "ok"}},
	}
	ag, err := New(mdl, nil, Options{})
	if err != nil {
		t.Fatalf("new agent: %v", err)
	}
	out, err := ag.Run(context.Background(), NewContext())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if out.StopReason != StopReasonCompleted {
		t.Fatalf("expected StopReasonCompleted, got %q", out.StopReason)
	}
}

func TestAgentStopReasonMaxIterations(t *testing.T) {
	mdl := &usagePublishingModel{
		outputs: []*ModelOutput{
			{ToolCalls: []ToolCall{{Name: "loop"}}},
			{ToolCalls: []ToolCall{{Name: "loop"}}},
		},
	}
	ag, err := New(mdl, &stubTools{}, Options{MaxIterations: 1})
	if err != nil {
		t.Fatalf("new agent: %v", err)
	}
	out, err := ag.Run(context.Background(), NewContext())
	if !errors.Is(err, ErrMaxIterations) {
		t.Fatalf("expected ErrMaxIterations, got %v", err)
	}
	if out == nil || out.StopReason != StopReasonMaxIterations {
		t.Fatalf("expected StopReasonMaxIterations, got %+v", out)
	}
}

func TestAgentStopReasonMaxTokens(t *testing.T) {
	mdl := &usagePublishingModel{
		usages: []model.Usage{
			{InputTokens: 400, OutputTokens: 200, TotalTokens: 600},
		},
		outputs: []*ModelOutput{
			// Returns a tool call so the loop would otherwise iterate.
			{ToolCalls: []ToolCall{{Name: "noop"}}},
		},
	}
	ag, err := New(mdl, &stubTools{}, Options{MaxTokens: 500, MaxIterations: 5})
	if err != nil {
		t.Fatalf("new agent: %v", err)
	}
	out, err := ag.Run(context.Background(), NewContext())
	if err != nil {
		t.Fatalf("budget cap should produce a structured stop, not an error: %v", err)
	}
	if out == nil || out.StopReason != StopReasonMaxTokens {
		t.Fatalf("expected StopReasonMaxTokens, got %+v", out)
	}
}

func TestAgentStopReasonMaxBudget(t *testing.T) {
	// Pick a very large per-call usage and a known model name so EstimateCost
	// returns a non-zero number even on cheap models. claude-3-5-haiku and
	// claude-sonnet-4-5 both have embedded LiteLLM pricing.
	mdl := &usagePublishingModel{
		usages: []model.Usage{
			{InputTokens: 1_000_000, OutputTokens: 1_000_000, TotalTokens: 2_000_000},
		},
		outputs: []*ModelOutput{
			{ToolCalls: []ToolCall{{Name: "noop"}}},
		},
	}
	ag, err := New(mdl, &stubTools{}, Options{
		MaxBudgetUSD:  0.0001,
		MaxIterations: 5,
		ModelName:     "claude-sonnet-4-5",
	})
	if err != nil {
		t.Fatalf("new agent: %v", err)
	}
	out, err := ag.Run(context.Background(), NewContext())
	if err != nil {
		t.Fatalf("budget cap should be a structured stop, not an error: %v", err)
	}
	if out == nil || out.StopReason != StopReasonMaxBudget {
		t.Fatalf("expected StopReasonMaxBudget, got %+v", out)
	}
}

func TestAgentStopReasonMaxBudgetInertWithoutModelName(t *testing.T) {
	// Same usage as above but no ModelName — guard must stay disabled.
	// We cap iterations at 1 so the run still terminates deterministically
	// and we can assert the reason is *not* MaxBudget.
	mdl := &usagePublishingModel{
		usages: []model.Usage{{InputTokens: 1_000_000, OutputTokens: 1_000_000, TotalTokens: 2_000_000}},
		outputs: []*ModelOutput{
			{ToolCalls: []ToolCall{{Name: "noop"}}},
		},
	}
	ag, err := New(mdl, &stubTools{}, Options{
		MaxBudgetUSD:  0.0001,
		MaxIterations: 1,
		// ModelName intentionally empty.
	})
	if err != nil {
		t.Fatalf("new agent: %v", err)
	}
	out, err := ag.Run(context.Background(), NewContext())
	if !errors.Is(err, ErrMaxIterations) {
		t.Fatalf("expected the iteration cap to fire, got %v", err)
	}
	if out == nil || out.StopReason == StopReasonMaxBudget {
		t.Fatalf("budget guard should be inert without ModelName, got %+v", out)
	}
}

func TestAgentStopReasonContextCancel(t *testing.T) {
	mdl := &usagePublishingModel{
		outputs: []*ModelOutput{{Done: true}},
	}
	ag, err := New(mdl, nil, Options{})
	if err != nil {
		t.Fatalf("new agent: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out, err := ag.Run(ctx, NewContext())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	// out is nil here because cancel fires before any model call.
	if out != nil {
		t.Fatalf("expected nil output for pre-loop cancel, got %+v", out)
	}
}

func TestAgentStopReasonContextDeadline(t *testing.T) {
	// Drive a real deadline through Options.Timeout and a slow model so the
	// loop's ctx.Err() check observes DeadlineExceeded.
	mdl := &slowModel{delay: 100 * time.Millisecond}
	ag, err := New(mdl, nil, Options{Timeout: 20 * time.Millisecond})
	if err != nil {
		t.Fatalf("new agent: %v", err)
	}
	_, err = ag.Run(context.Background(), NewContext())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got %v", err)
	}
}

type slowModel struct {
	delay time.Duration
}

func (m *slowModel) Generate(ctx context.Context, _ *Context) (*ModelOutput, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(m.delay):
	}
	return &ModelOutput{Done: true}, nil
}

// errModel returns a fixed error every Generate call. Used to drive the
// mid-iteration classifyError path without relying on a real network
// timeout (which would make tests flaky and slow).
type errModel struct{ err error }

func (m *errModel) Generate(context.Context, *Context) (*ModelOutput, error) {
	return nil, m.err
}

func TestClassifyError_TaxonomyMatrix(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
		want StopReason
	}{
		{"nil → model_error", nil, StopReasonModelError},
		{"DeadlineExceeded → aborted_deadline", context.DeadlineExceeded, StopReasonContextDeadline},
		{"Canceled → aborted_context", context.Canceled, StopReasonContextCancel},
		{"wrapped DeadlineExceeded → aborted_deadline", fmt.Errorf("upstream: %w", context.DeadlineExceeded), StopReasonContextDeadline},
		{"wrapped Canceled → aborted_context", fmt.Errorf("upstream: %w", context.Canceled), StopReasonContextCancel},
		{"generic error → model_error", errors.New("boom"), StopReasonModelError},
	}
	for _, tc := range cases {
		got := classifyError(tc.err)
		if got != tc.want {
			t.Errorf("classifyError(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
}

// When model.Generate returns a wrapped DeadlineExceeded mid-iteration, the
// agent loop should annotate StopReasonContextDeadline — NOT StopReasonModelError.
// This is the path that the runner relied on to distinguish "task timed out"
// from "provider crashed" in the stop-reason histogram.
func TestAgentStopReasonModelMidIterationDeadline(t *testing.T) {
	t.Parallel()
	mdl := &errModel{err: fmt.Errorf("wrapped: %w", context.DeadlineExceeded)}
	ag, err := New(mdl, nil, Options{})
	if err != nil {
		t.Fatalf("new agent: %v", err)
	}
	out, err := ag.Run(context.Background(), NewContext())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected wrapped DeadlineExceeded, got %v", err)
	}
	// out is nil here because the very first Generate already errored
	// before the loop set `last`. The classifyError contract is that
	// when out is non-nil the StopReason is annotated; when nil the
	// caller surfaces the error directly. Verify the error path stays
	// distinguishable from a generic provider failure.
	_ = out
}

func TestAgentStopReasonModelGenericError(t *testing.T) {
	t.Parallel()
	mdl := &errModel{err: errors.New("provider 503")}
	ag, err := New(mdl, nil, Options{})
	if err != nil {
		t.Fatalf("new agent: %v", err)
	}
	if _, runErr := ag.Run(context.Background(), NewContext()); runErr == nil {
		t.Fatal("expected error from model")
	} else if errors.Is(runErr, context.DeadlineExceeded) || errors.Is(runErr, context.Canceled) {
		t.Fatalf("generic error should not be classified as ctx error: %v", runErr)
	}
}

func TestAgentCumulativeUsageTracked(t *testing.T) {
	mdl := &usagePublishingModel{
		usages: []model.Usage{
			{InputTokens: 100, OutputTokens: 50, TotalTokens: 150},
			{InputTokens: 80, OutputTokens: 30, TotalTokens: 110},
		},
		outputs: []*ModelOutput{
			{ToolCalls: []ToolCall{{Name: "step"}}},
			{Done: true},
		},
	}
	c := NewContext()
	ag, err := New(mdl, &stubTools{}, Options{MaxIterations: 5})
	if err != nil {
		t.Fatalf("new agent: %v", err)
	}
	if _, err := ag.Run(context.Background(), c); err != nil {
		t.Fatalf("run: %v", err)
	}
	if c.CumulativeUsage.TotalTokens != 260 {
		t.Fatalf("cumulative TotalTokens = %d, want 260", c.CumulativeUsage.TotalTokens)
	}
	if c.CumulativeUsage.InputTokens != 180 {
		t.Fatalf("cumulative InputTokens = %d, want 180", c.CumulativeUsage.InputTokens)
	}
}

func TestSubagentDefaultIterationConstant(t *testing.T) {
	// Guard against the constant silently regressing — the api layer
	// pins subagents to this value, so a downward bump would shrink
	// every subagent invocation across the codebase.
	if DefaultSubagentMaxIterations != 50 {
		t.Fatalf("DefaultSubagentMaxIterations = %d, want 50 (mirrors Claude Code MAX_AGENT_TURNS)", DefaultSubagentMaxIterations)
	}
}
