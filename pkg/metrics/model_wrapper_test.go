package metrics

import (
	"context"
	"errors"
	"testing"

	"github.com/saker-ai/saker/pkg/model"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

type fakeModel struct {
	name        string
	completeErr error
	streamErr   error
	usage       model.Usage
}

func (m *fakeModel) ModelName() string { return m.name }

func (m *fakeModel) Complete(_ context.Context, _ model.Request) (*model.Response, error) {
	if m.completeErr != nil {
		return nil, m.completeErr
	}
	return &model.Response{Usage: m.usage}, nil
}

func (m *fakeModel) CompleteStream(_ context.Context, _ model.Request, cb model.StreamHandler) error {
	if m.streamErr != nil {
		return m.streamErr
	}
	return cb(model.StreamResult{Final: true, Response: &model.Response{Usage: m.usage}})
}

func TestWrapModel_NilPassthrough(t *testing.T) {
	if got := WrapModel(nil, "anthropic"); got != nil {
		t.Fatalf("WrapModel(nil) = %v, want nil", got)
	}
}

func TestWrapModel_CompleteCountsAndTokens(t *testing.T) {
	fake := &fakeModel{
		name:  "claude-sonnet-4-20250514",
		usage: model.Usage{InputTokens: 100, OutputTokens: 200, CacheReadTokens: 50},
	}
	wrapped := WrapModel(fake, "anthropic")

	before := counterValue(t, ModelRequestsTotal, "anthropic", "claude-sonnet", "unary", "ok")
	beforeIn := counterValue(t, ModelTokensTotal, "anthropic", "claude-sonnet", "input")
	beforeOut := counterValue(t, ModelTokensTotal, "anthropic", "claude-sonnet", "output")
	beforeCache := counterValue(t, ModelTokensTotal, "anthropic", "claude-sonnet", "cache_read")

	if _, err := wrapped.Complete(context.Background(), model.Request{Model: "claude-sonnet-4-20250514"}); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	if got := counterValue(t, ModelRequestsTotal, "anthropic", "claude-sonnet", "unary", "ok"); got != before+1 {
		t.Errorf("requests_total: got %v, want %v", got, before+1)
	}
	if got := counterValue(t, ModelTokensTotal, "anthropic", "claude-sonnet", "input"); got != beforeIn+100 {
		t.Errorf("input tokens: got %v, want %v", got, beforeIn+100)
	}
	if got := counterValue(t, ModelTokensTotal, "anthropic", "claude-sonnet", "output"); got != beforeOut+200 {
		t.Errorf("output tokens: got %v, want %v", got, beforeOut+200)
	}
	if got := counterValue(t, ModelTokensTotal, "anthropic", "claude-sonnet", "cache_read"); got != beforeCache+50 {
		t.Errorf("cache_read tokens: got %v, want %v", got, beforeCache+50)
	}
}

func TestWrapModel_StreamErrorClassified(t *testing.T) {
	fake := &fakeModel{name: "gpt-4o-mini", streamErr: errors.New("upstream blew up")}
	wrapped := WrapModel(fake, "openai")

	before := counterValue(t, ModelRequestsTotal, "openai", "gpt-4o", "stream", "error")
	err := wrapped.CompleteStream(context.Background(), model.Request{Model: "gpt-4o-mini"}, func(model.StreamResult) error { return nil })
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := counterValue(t, ModelRequestsTotal, "openai", "gpt-4o", "stream", "error"); got != before+1 {
		t.Errorf("error counter: got %v, want %v", got, before+1)
	}
}

func TestWrapModel_ProviderInferredFromModelWhenEmpty(t *testing.T) {
	fake := &fakeModel{name: "claude-haiku-4.5", usage: model.Usage{InputTokens: 1, OutputTokens: 1}}
	wrapped := WrapModel(fake, "")

	before := counterValue(t, ModelRequestsTotal, "anthropic", "claude-haiku", "stream", "ok")
	err := wrapped.CompleteStream(context.Background(), model.Request{Model: "claude-haiku-4.5"}, func(sr model.StreamResult) error { return nil })
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if got := counterValue(t, ModelRequestsTotal, "anthropic", "claude-haiku", "stream", "ok"); got != before+1 {
		t.Errorf("provider-inferred counter: got %v, want %v", got, before+1)
	}
}

func TestWrapModel_PassthroughInterfaces(t *testing.T) {
	fake := &fakeModel{name: "gpt-4.1"}
	wrapped := WrapModel(fake, "openai")

	mn, ok := wrapped.(model.ModelNamer)
	if !ok {
		t.Fatal("wrapped model should implement ModelNamer")
	}
	if mn.ModelName() != "gpt-4.1" {
		t.Errorf("ModelName: got %q, want %q", mn.ModelName(), "gpt-4.1")
	}
}

// counterValue returns the current value of a CounterVec for the given
// label set. Used to compute deltas across test calls because init()
// registration is process-wide.
func counterValue(t *testing.T, vec *prometheus.CounterVec, labels ...string) float64 {
	t.Helper()
	var m dto.Metric
	if err := vec.WithLabelValues(labels...).Write(&m); err != nil {
		t.Fatalf("counterValue: write: %v", err)
	}
	return m.Counter.GetValue()
}
