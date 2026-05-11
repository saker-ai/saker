package api

import "context"

// spanContextKey carries a Tracer SpanContext through context.Context so that
// the runtime, model, and tool layers can chain child spans off the active
// agent span without threading SpanContext through every signature.
//
// The propagation surface is intentionally tiny: only spanFromContext and
// withSpanContext are exported within the package. Call sites that need a
// child span should call rt.tracer.StartXxxSpan with spanFromContext(ctx)
// as the parent, defer rt.tracer.EndSpan, and stash the new span via
// withSpanContext(ctx, child) before invoking nested operations.
type spanContextKey struct{}

func withSpanContext(ctx context.Context, span SpanContext) context.Context {
	if ctx == nil || span == nil {
		return ctx
	}
	return context.WithValue(ctx, spanContextKey{}, span)
}

func spanFromContext(ctx context.Context) SpanContext {
	if ctx == nil {
		return nil
	}
	v, _ := ctx.Value(spanContextKey{}).(SpanContext)
	return v
}
