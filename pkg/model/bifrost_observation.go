// bifrost_observation.go: provider-call telemetry for Bifrost-backed models.
//
// Bifrost v1.5.8 declares schemas.ObservabilityPlugin (Inject(ctx, *Trace))
// but its core engine never invokes Inject — the contract is aspirational
// in this release. The full schemas.Tracer interface (~30 methods) is
// implementable but heavy, and saker already owns its own OTel-wired
// Tracer at the agent/model/tool layer (pkg/api/otel.go), so we settle
// for the slice we can deliver today: per-call observation events
// harvested at the LLMPlugin Pre/Post boundary.
//
// What this gives saker callers:
//   - Wall-clock latency for the full Bifrost dispatch
//   - Resolved provider/model after Bifrost's SDK-level Fallbacks routing
//   - Token usage (input/output/cache read/cache write)
//   - HTTP status code + error type/message on failure
//   - UsedFallback flag mirroring failoverObserverPlugin's detection
//
// What it does NOT give (and would require a real schemas.Tracer impl):
//   - Per-attempt timing inside a fallback chain
//   - Bifrost retry counts
//   - Plugin execution durations
//
// Saker callers register an ObservationSink via SetGlobalObservationSink;
// NewBifrost picks it up and wires an LLMPlugin into the engine. When
// Bifrost upstream wires Inject(), this file is the natural place to add
// the *Trace forwarding path on top of what's already here.
package model

import (
	"strings"
	"sync/atomic"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
)

// ObservationEvent is one completed Bifrost call surfaced via the LLMPlugin
// PostLLMHook. Fields are best-effort: unset for non-applicable cases
// (e.g. StatusCode is 0 on success, ErrorMessage is "" when no error).
type ObservationEvent struct {
	// RequestedProvider / RequestedModel are the primary configured for this
	// bifrostModel — the call as the saker caller asked for it.
	RequestedProvider string
	RequestedModel    string

	// Provider / Model are the actually-resolved provider/model after
	// Bifrost's SDK-level Fallbacks engine ran. When equal to Requested*,
	// the primary served the call.
	Provider string
	Model    string

	// LatencyMS is wall-clock time between PreLLMHook and PostLLMHook for
	// the same plugin instance. Spans the full Bifrost dispatch including
	// retries and fallbacks but excludes saker's own pre/post processing.
	LatencyMS int64

	// StatusCode is the HTTP status from the resolved provider on error;
	// 0 on success.
	StatusCode int

	// ErrorType is the BifrostError.Type ("authentication_error",
	// "rate_limit_exceeded", etc.) when present. Stable label suitable for
	// metric series cardinality.
	ErrorType string

	// ErrorMessage is the upstream error text, truncated to 500 chars to
	// bound log/metric attribute size.
	ErrorMessage string

	// Token usage (zero when unavailable).
	InputTokens      int
	OutputTokens     int
	TotalTokens      int
	CacheReadTokens  int
	CacheWriteTokens int

	// UsedFallback is true when Bifrost dispatched to a non-primary
	// provider/model. Mirrors the detection in failoverObserverPlugin so
	// observability sinks can count fallback invocations directly.
	UsedFallback bool
}

// ObservationSink is the consumer side. Implementations should not block —
// PostLLMHook fires on Bifrost's request-serving goroutine, so heavy work
// must be deferred (channel + worker, async exporter, etc.).
type ObservationSink interface {
	OnObservation(ObservationEvent)
}

// ObservationSinkFunc adapts a plain function to ObservationSink. Useful
// for tests and the saker → OTel bridge in pkg/api.
type ObservationSinkFunc func(ObservationEvent)

// OnObservation implements ObservationSink.
func (f ObservationSinkFunc) OnObservation(ev ObservationEvent) { f(ev) }

// observationSinkBox holds a sink in atomic.Value (which requires a
// concrete-type wrapper to allow nil stores).
type observationSinkBox struct {
	sink ObservationSink
}

var globalObservationSink atomic.Value // *observationSinkBox

// SetGlobalObservationSink registers a sink that NewBifrost reads when
// constructing observation plugins. Pass nil to clear. Concurrency-safe:
// reads in NewBifrost and the plugin path snapshot the pointer once.
func SetGlobalObservationSink(s ObservationSink) {
	globalObservationSink.Store(&observationSinkBox{sink: s})
}

// currentObservationSink returns the registered sink or nil.
func currentObservationSink() ObservationSink {
	v := globalObservationSink.Load()
	if v == nil {
		return nil
	}
	box, _ := v.(*observationSinkBox)
	if box == nil {
		return nil
	}
	return box.sink
}

// observationStartCtxKey stashes the PreLLMHook timestamp inside the
// per-request BifrostContext. Each Bifrost dispatch has a fresh context,
// so concurrent calls don't collide.
const observationStartCtxKey = "saker.observation.start"

// observationPlugin is an LLMPlugin that times Pre→Post boundaries and
// emits ObservationEvent via the configured sink.
type observationPlugin struct {
	primaryProvider schemas.ModelProvider
	primaryModel    string
	sink            ObservationSink
}

func newObservationPlugin(primaryProvider schemas.ModelProvider, primaryModel string, sink ObservationSink) *observationPlugin {
	return &observationPlugin{
		primaryProvider: primaryProvider,
		primaryModel:    primaryModel,
		sink:            sink,
	}
}

func (p *observationPlugin) GetName() string { return "saker.observation" }
func (p *observationPlugin) Cleanup() error  { return nil }

// PreLLMHook records the start time on the BifrostContext.
func (p *observationPlugin) PreLLMHook(ctx *schemas.BifrostContext, req *schemas.BifrostRequest) (*schemas.BifrostRequest, *schemas.LLMPluginShortCircuit, error) {
	if ctx != nil {
		ctx.SetValue(observationStartCtxKey, time.Now())
	}
	return req, nil, nil
}

// PostLLMHook builds an ObservationEvent from the response/error pair and
// forwards it to the sink. Always returns the inputs unchanged.
func (p *observationPlugin) PostLLMHook(ctx *schemas.BifrostContext, resp *schemas.BifrostResponse, bifrostErr *schemas.BifrostError) (*schemas.BifrostResponse, *schemas.BifrostError, error) {
	if p.sink == nil {
		return resp, bifrostErr, nil
	}

	ev := ObservationEvent{
		RequestedProvider: string(p.primaryProvider),
		RequestedModel:    p.primaryModel,
	}

	if ctx != nil {
		if v, ok := ctx.Value(observationStartCtxKey).(time.Time); ok && !v.IsZero() {
			ev.LatencyMS = time.Since(v).Milliseconds()
		}
	}

	fillFromResponse(&ev, resp, p.primaryProvider, p.primaryModel)
	fillFromError(&ev, bifrostErr)

	p.sink.OnObservation(ev)
	return resp, bifrostErr, nil
}

// fillFromResponse populates the event from a successful BifrostResponse.
// No-op if resp or its ChatResponse is nil.
func fillFromResponse(ev *ObservationEvent, resp *schemas.BifrostResponse, primary schemas.ModelProvider, primaryModel string) {
	if resp == nil || resp.ChatResponse == nil {
		return
	}
	ef := resp.ChatResponse.ExtraFields
	ev.Provider = string(ef.Provider)
	ev.Model = ef.ResolvedModelUsed
	if ev.Model == "" {
		ev.Model = ef.OriginalModelRequested
	}
	if ef.Provider != "" {
		if ef.Provider != primary {
			ev.UsedFallback = true
		} else if ef.ResolvedModelUsed != "" && ef.ResolvedModelUsed != primaryModel {
			ev.UsedFallback = true
		}
	}
	if u := resp.ChatResponse.Usage; u != nil {
		ev.InputTokens = u.PromptTokens
		ev.OutputTokens = u.CompletionTokens
		ev.TotalTokens = u.TotalTokens
		if u.PromptTokensDetails != nil {
			ev.CacheReadTokens = u.PromptTokensDetails.CachedReadTokens
			ev.CacheWriteTokens = u.PromptTokensDetails.CachedWriteTokens
		}
	}
}

// fillFromError populates the event from a BifrostError when present.
// Status code, error type, and resolved provider/model on failure paths
// (which the success path doesn't see) all come from here.
func fillFromError(ev *ObservationEvent, bifrostErr *schemas.BifrostError) {
	if bifrostErr == nil {
		return
	}
	if bifrostErr.StatusCode != nil {
		ev.StatusCode = *bifrostErr.StatusCode
	}
	if bifrostErr.Type != nil {
		ev.ErrorType = *bifrostErr.Type
	}
	if bifrostErr.Error != nil {
		if ev.ErrorType == "" && bifrostErr.Error.Type != nil {
			ev.ErrorType = *bifrostErr.Error.Type
		}
		ev.ErrorMessage = truncateMessage(strings.TrimSpace(bifrostErr.Error.Message), 500)
	}
	ef := bifrostErr.ExtraFields
	if ev.Provider == "" {
		ev.Provider = string(ef.Provider)
	}
	if ev.Model == "" {
		ev.Model = ef.ResolvedModelUsed
		if ev.Model == "" {
			ev.Model = ef.OriginalModelRequested
		}
	}
}
