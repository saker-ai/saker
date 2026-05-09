package terminalbench

import (
	"context"
	"errors"
	"testing"

	"github.com/cinience/saker/pkg/agent"
	"github.com/cinience/saker/pkg/message"
	"github.com/cinience/saker/pkg/model"
)

// scriptedBaseModel is a model.Model that walks a fixed list of (response,
// error) pairs and returns one per CompleteStream call. Used to verify the
// modelBridge correctly records per-call usage and surfaces the most recent
// provider error to LastError().
type scriptedBaseModel struct {
	steps []scriptedStep
	idx   int
}

type scriptedStep struct {
	resp *model.Response
	err  error
}

func (s *scriptedBaseModel) Complete(_ context.Context, _ model.Request) (*model.Response, error) {
	return nil, errors.New("scriptedBaseModel: Complete not used")
}

func (s *scriptedBaseModel) CompleteStream(_ context.Context, _ model.Request, cb model.StreamHandler) error {
	if s.idx >= len(s.steps) {
		return errors.New("scriptedBaseModel: script exhausted")
	}
	step := s.steps[s.idx]
	s.idx++
	if step.err != nil {
		return step.err
	}
	return cb(model.StreamResult{Final: true, Response: step.resp})
}

func TestModelBridge_PerCallUsageRecordsEveryGenerate(t *testing.T) {
	t.Parallel()
	base := &scriptedBaseModel{
		steps: []scriptedStep{
			{resp: &model.Response{
				Message:    model.Message{Role: "assistant", Content: "step-1"},
				Usage:      model.Usage{InputTokens: 100, OutputTokens: 20, CacheReadTokens: 5},
				StopReason: "tool_use",
			}},
			{resp: &model.Response{
				Message:    model.Message{Role: "assistant", Content: "step-2"},
				Usage:      model.Usage{InputTokens: 200, OutputTokens: 40, CacheReadTokens: 50},
				StopReason: "end_turn",
			}},
		},
	}
	bridge := newModelBridge(base, message.NewHistory(), "", "do work", nil)
	if _, err := bridge.Generate(context.Background(), nil); err != nil {
		t.Fatalf("first Generate: %v", err)
	}
	if _, err := bridge.Generate(context.Background(), nil); err != nil {
		t.Fatalf("second Generate: %v", err)
	}
	pc := bridge.PerCallUsage()
	if len(pc) != 2 {
		t.Fatalf("PerCallUsage len = %d, want 2", len(pc))
	}
	if pc[0].InputTokens != 100 || pc[0].OutputTokens != 20 || pc[0].CacheReadTokens != 5 {
		t.Errorf("call 0 usage = %+v, want input=100 output=20 cache_read=5", pc[0])
	}
	if pc[1].InputTokens != 200 || pc[1].OutputTokens != 40 || pc[1].CacheReadTokens != 50 {
		t.Errorf("call 1 usage = %+v, want input=200 output=40 cache_read=50", pc[1])
	}
	// Aggregate Usage() must equal sum of perCall — guards against the
	// runner's iteration_tokens column drifting from the headline numbers.
	if u := bridge.Usage(); u.InputTokens != 300 || u.OutputTokens != 60 || u.CacheReadTokens != 55 {
		t.Errorf("aggregate Usage = %+v, want sum of per-call", u)
	}
	if got := bridge.StopReason(); got != "end_turn" {
		t.Errorf("StopReason = %q, want end_turn", got)
	}
	if bridge.LastError() != nil {
		t.Errorf("LastError should be nil after successful Generate, got %v", bridge.LastError())
	}
}

func TestModelBridge_LastErrorCapturesProviderFailure(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("provider 503 service unavailable")
	base := &scriptedBaseModel{
		steps: []scriptedStep{
			{err: sentinel},
		},
	}
	bridge := newModelBridge(base, message.NewHistory(), "", "do work", nil)
	_, err := bridge.Generate(context.Background(), &agent.Context{})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Generate err = %v, want sentinel", err)
	}
	if !errors.Is(bridge.LastError(), sentinel) {
		t.Fatalf("LastError = %v, want sentinel for transcript capture", bridge.LastError())
	}
	// PerCallUsage must NOT record the failed call — there is no usage to
	// attribute, and including a zero entry would skew per-iteration
	// token plots.
	if pc := bridge.PerCallUsage(); len(pc) != 0 {
		t.Errorf("failed Generate should not append to PerCallUsage, got %v", pc)
	}
}

// A successful Generate after a failed one must clear LastError, otherwise
// the runner would write a spurious "model error" transcript line for a
// task that actually succeeded after a transient blip.
func TestModelBridge_LastErrorClearedAfterRecovery(t *testing.T) {
	t.Parallel()
	base := &scriptedBaseModel{
		steps: []scriptedStep{
			{err: errors.New("transient")},
			{resp: &model.Response{
				Message:    model.Message{Role: "assistant", Content: "ok"},
				Usage:      model.Usage{InputTokens: 1, OutputTokens: 1},
				StopReason: "end_turn",
			}},
		},
	}
	bridge := newModelBridge(base, message.NewHistory(), "", "go", nil)
	if _, err := bridge.Generate(context.Background(), nil); err == nil {
		t.Fatal("expected first Generate to fail")
	}
	if bridge.LastError() == nil {
		t.Fatal("LastError should be set after failure")
	}
	if _, err := bridge.Generate(context.Background(), nil); err != nil {
		t.Fatalf("second Generate: %v", err)
	}
	if bridge.LastError() != nil {
		t.Fatalf("LastError should be cleared after recovery, got %v", bridge.LastError())
	}
}
