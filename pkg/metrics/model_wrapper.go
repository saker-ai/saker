package metrics

import (
	"context"
	"strings"
	"time"

	"github.com/saker-ai/saker/pkg/model"
)

// WrapModel returns a model.Model decorator that records request count,
// duration, token usage, and error classification. Provider/model labels
// are sanitized before observation.
//
// If primary is nil, WrapModel returns nil — callers should treat the
// result identically to the original primary value.
func WrapModel(primary model.Model, provider string) model.Model {
	if primary == nil {
		return nil
	}
	return &instrumentedModel{base: primary, provider: SanitizeProvider(provider)}
}

type instrumentedModel struct {
	base     model.Model
	provider string
}

// Compile-time interface conformance.
var _ model.Model = (*instrumentedModel)(nil)

func (m *instrumentedModel) Complete(ctx context.Context, req model.Request) (*model.Response, error) {
	start := time.Now()
	resp, err := m.base.Complete(ctx, req)
	m.observe(req, resp, err, start, "unary")
	return resp, err
}

func (m *instrumentedModel) CompleteStream(ctx context.Context, req model.Request, cb model.StreamHandler) error {
	start := time.Now()
	var captured *model.Response
	wrapped := func(sr model.StreamResult) error {
		if sr.Final && sr.Response != nil {
			captured = sr.Response
		}
		return cb(sr)
	}
	err := m.base.CompleteStream(ctx, req, wrapped)
	m.observe(req, captured, err, start, "stream")
	return err
}

// Forward optional ContextWindowProvider / ModelNamer through the wrapper
// so downstream code that type-asserts on these still works.
func (m *instrumentedModel) ContextWindow() int {
	if cwp, ok := m.base.(model.ContextWindowProvider); ok {
		return cwp.ContextWindow()
	}
	return 0
}

func (m *instrumentedModel) ModelName() string {
	if mn, ok := m.base.(model.ModelNamer); ok {
		return mn.ModelName()
	}
	return ""
}

func (m *instrumentedModel) observe(req model.Request, resp *model.Response, err error, start time.Time, mode string) {
	status := ClassifyErr(err)
	modelName := SanitizeModel(req.Model)
	if modelName == "unknown" {
		// Fall back to the wrapped model's self-reported name.
		if mn, ok := m.base.(model.ModelNamer); ok {
			modelName = SanitizeModel(mn.ModelName())
		}
	}
	if strings.TrimSpace(modelName) == "" {
		modelName = "unknown"
	}
	provider := m.provider
	if provider == "unknown" || provider == "" {
		provider = InferProviderFromModel(modelName)
	}

	ModelRequestsTotal.WithLabelValues(provider, modelName, mode, status).Inc()
	ModelRequestDuration.WithLabelValues(provider, modelName, mode).Observe(time.Since(start).Seconds())

	if resp != nil {
		u := resp.Usage
		if u.InputTokens > 0 {
			ModelTokensTotal.WithLabelValues(provider, modelName, "input").Add(float64(u.InputTokens))
		}
		if u.OutputTokens > 0 {
			ModelTokensTotal.WithLabelValues(provider, modelName, "output").Add(float64(u.OutputTokens))
		}
		if u.CacheReadTokens > 0 {
			ModelTokensTotal.WithLabelValues(provider, modelName, "cache_read").Add(float64(u.CacheReadTokens))
		}
		if u.CacheCreationTokens > 0 {
			ModelTokensTotal.WithLabelValues(provider, modelName, "cache_creation").Add(float64(u.CacheCreationTokens))
		}
	}
}
