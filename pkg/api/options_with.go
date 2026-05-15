package api

import "github.com/saker-ai/saker/pkg/model"

// WithMaxSessions caps how many parallel session histories are retained.
// Values <= 0 fall back to the default.
func WithMaxSessions(n int) func(*Options) {
	return func(o *Options) {
		if n > 0 {
			o.MaxSessions = n
		}
	}
}

// WithTokenTracking enables or disables token usage tracking.
func WithTokenTracking(enabled bool) func(*Options) {
	return func(o *Options) {
		o.TokenTracking = enabled
	}
}

// WithTokenCallback sets a callback function that is called synchronously after
// each model call with the token usage statistics. Automatically enables
// TokenTracking.
func WithTokenCallback(fn TokenCallback) func(*Options) {
	return func(o *Options) {
		o.TokenCallback = fn
		if fn != nil {
			o.TokenTracking = true
		}
	}
}

// WithAutoCompact configures automatic context compaction.
func WithAutoCompact(config CompactConfig) func(*Options) {
	return func(o *Options) {
		o.AutoCompact = config
	}
}

// WithOTEL configures OpenTelemetry distributed tracing.
// Requires build tag 'otel' for actual instrumentation; otherwise no-op.
func WithOTEL(config OTELConfig) func(*Options) {
	return func(o *Options) {
		o.OTEL = config
	}
}

// WithModelPool configures a pool of models indexed by tier.
func WithModelPool(pool map[ModelTier]model.Model) func(*Options) {
	return func(o *Options) {
		if pool != nil {
			o.ModelPool = pool
		}
	}
}

// WithSubagentModelMapping configures subagent-type-to-tier mappings for model selection.
// Keys should be lowercase subagent type names (e.g., "explore", "plan").
func WithSubagentModelMapping(mapping map[string]ModelTier) func(*Options) {
	return func(o *Options) {
		if mapping != nil {
			o.SubagentModelMapping = mapping
		}
	}
}
