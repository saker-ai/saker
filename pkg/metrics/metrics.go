// Package metrics centralizes Prometheus instrumentation for the Saker
// runtime: sessions, agent runs, tool invocations, and model calls.
//
// Design notes:
//
//   - Lives below pkg/api, pkg/tool, pkg/model so any of them can import
//     it without creating a cycle.
//   - Vecs are package-level and registered exactly once via init();
//     prometheus.MustRegister panics on duplicate registration, which
//     surfaces accidental double-init early.
//   - Labels are kept low-cardinality on purpose. Tool/model identifiers
//     come from a small fixed registry; status is a closed enum
//     {ok,error,canceled}; provider/model are bucketed before observation.
//
// The HTTP-layer metrics (saker_http_*) remain in pkg/server/metrics.go
// because their middleware is gin-specific and they pre-date this package.
package metrics

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Status is the closed enum used for *_total{status} counters.
const (
	StatusOK       = "ok"
	StatusError    = "error"
	StatusCanceled = "canceled"
)

var (
	// SessionsActive is a gauge of currently-running sessions (Acquire/Release pair).
	SessionsActive = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "saker",
		Subsystem: "session",
		Name:      "active",
		Help:      "Number of sessions currently holding the session gate.",
	})

	// AgentRunsTotal counts terminal outcomes of a runtime.Run / RunStream call.
	AgentRunsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "saker",
		Subsystem: "agent",
		Name:      "runs_total",
		Help:      "Total number of agent runs by terminal status.",
	}, []string{"status"})

	// AgentRunDuration measures wall-clock duration of an agent run.
	AgentRunDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "saker",
		Subsystem: "agent",
		Name:      "run_duration_seconds",
		Help:      "Wall-clock duration of agent runs in seconds.",
		// Buckets tuned for an interactive agent: sub-second to a few minutes.
		Buckets: []float64{0.1, 0.25, 0.5, 1, 2, 5, 10, 20, 30, 60, 120, 300},
	}, []string{"status"})

	// ToolInvocationsTotal counts every tool execution attempt.
	ToolInvocationsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "saker",
		Subsystem: "tool",
		Name:      "invocations_total",
		Help:      "Total tool invocations by tool name and outcome.",
	}, []string{"tool", "status"})

	// ToolDuration measures wall-clock duration of tool.Execute().
	ToolDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "saker",
		Subsystem: "tool",
		Name:      "duration_seconds",
		Help:      "Tool execution duration in seconds.",
		Buckets:   []float64{0.005, 0.01, 0.05, 0.1, 0.5, 1, 2, 5, 10, 30, 60, 120},
	}, []string{"tool"})

	// ModelRequestsTotal counts model.Complete / CompleteStream calls.
	ModelRequestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "saker",
		Subsystem: "model",
		Name:      "requests_total",
		Help:      "Total model requests by provider, model and outcome.",
	}, []string{"provider", "model", "mode", "status"})

	// ModelRequestDuration measures wall-clock duration of model calls.
	ModelRequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "saker",
		Subsystem: "model",
		Name:      "request_duration_seconds",
		Help:      "Model request duration in seconds.",
		Buckets:   []float64{0.1, 0.25, 0.5, 1, 2, 5, 10, 20, 30, 60, 120},
	}, []string{"provider", "model", "mode"})

	// ModelTokensTotal accumulates token usage across model calls.
	ModelTokensTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "saker",
		Subsystem: "model",
		Name:      "tokens_total",
		Help:      "Total tokens consumed by direction (input/output/cache_read/cache_creation).",
	}, []string{"provider", "model", "direction"})
)

func init() {
	prometheus.MustRegister(
		SessionsActive,
		AgentRunsTotal,
		AgentRunDuration,
		ToolInvocationsTotal,
		ToolDuration,
		ModelRequestsTotal,
		ModelRequestDuration,
		ModelTokensTotal,
	)
}

// ClassifyErr maps a Go error to one of {ok, canceled, error}. It treats
// context.Canceled / DeadlineExceeded as "canceled" so dashboards don't
// confuse user-aborted runs with real failures.
func ClassifyErr(err error) string {
	if err == nil {
		return StatusOK
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return StatusCanceled
	}
	return StatusError
}

// ObserveSince observes elapsed seconds since start on the given histogram.
func ObserveSince(h prometheus.Observer, start time.Time) {
	h.Observe(time.Since(start).Seconds())
}

// SanitizeProvider normalizes provider strings to a small bucket. Unknown
// providers map to "other" to bound cardinality.
func SanitizeProvider(p string) string {
	switch strings.ToLower(strings.TrimSpace(p)) {
	case "anthropic":
		return "anthropic"
	case "openai":
		return "openai"
	case "dashscope":
		return "dashscope"
	case "":
		return "unknown"
	default:
		return "other"
	}
}

// InferProviderFromModel derives a provider bucket from the (already
// sanitized) model name. Used as a fallback when a caller wraps a model
// without knowing the provider explicitly (e.g. failover wrapper or
// dynamic model factories). Returns "unknown" for empty input and
// "other" for unrecognized families.
func InferProviderFromModel(sanitizedModel string) string {
	if sanitizedModel == "" || sanitizedModel == "unknown" {
		return "unknown"
	}
	switch {
	case strings.HasPrefix(sanitizedModel, "claude"):
		return "anthropic"
	case strings.HasPrefix(sanitizedModel, "gpt") ||
		strings.HasPrefix(sanitizedModel, "o1") ||
		strings.HasPrefix(sanitizedModel, "o3") ||
		strings.HasPrefix(sanitizedModel, "o4"):
		return "openai"
	case sanitizedModel == "qwen" || sanitizedModel == "deepseek" ||
		sanitizedModel == "kimi" || sanitizedModel == "glm" ||
		sanitizedModel == "doubao":
		return "dashscope"
	default:
		return "other"
	}
}

// SanitizeModel buckets a raw model string. We keep the family prefix
// (e.g. "claude-sonnet", "gpt-4o", "o3") and drop the version/date suffix
// to bound the cardinality of {provider,model} pairs to ~20.
func SanitizeModel(m string) string {
	m = strings.ToLower(strings.TrimSpace(m))
	if m == "" {
		return "unknown"
	}
	// Common family prefixes.
	prefixes := []string{
		"claude-opus", "claude-sonnet", "claude-haiku", "claude-3", "claude-2",
		"gpt-4o", "gpt-4.1", "gpt-4", "gpt-3.5",
		"o4-mini", "o3-mini", "o3", "o1-mini", "o1",
		"qwen", "deepseek", "kimi", "glm", "doubao",
	}
	for _, p := range prefixes {
		if strings.HasPrefix(m, p) {
			return p
		}
	}
	return "other"
}
