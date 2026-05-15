// OpenTelemetry HTTP server-side middleware. When no OTLP endpoint is
// configured (no SetupOTLP call and no OTEL_EXPORTER_OTLP_ENDPOINT env),
// the OTel global tracer provider is the built-in noop, so this
// middleware imposes near-zero overhead beyond an attribute-less Start/End
// span call against a noop tracer.
package middleware

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
	"go.opentelemetry.io/otel/trace"
)

const otelTracerName = "github.com/saker-ai/saker/pkg/middleware"

// OTELHTTPMiddleware returns a gin middleware that creates a server-side
// span per HTTP request using the OTel global tracer. Trace context is
// extracted from incoming W3C traceparent / tracestate headers so the
// span chains under any upstream caller.
func OTELHTTPMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		tracer := otel.Tracer(otelTracerName)
		propagator := otel.GetTextMapPropagator()

		ctx := propagator.Extract(c.Request.Context(), propagation.HeaderCarrier(c.Request.Header))

		route := c.FullPath()
		if route == "" {
			route = c.Request.URL.Path
		}
		spanName := c.Request.Method + " " + route

		ctx, span := tracer.Start(ctx, spanName,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				attribute.String("http.method", c.Request.Method),
				attribute.String("http.target", c.Request.URL.Path),
				attribute.String("http.route", route),
				attribute.String("http.user_agent", c.Request.UserAgent()),
				attribute.String("net.peer.ip", c.ClientIP()),
			),
		)
		c.Request = c.Request.WithContext(ctx)

		start := time.Now()
		c.Next()
		status := c.Writer.Status()

		span.SetAttributes(
			attribute.Int("http.status_code", status),
			attribute.Int64("http.duration_ms", time.Since(start).Milliseconds()),
		)
		if status >= http.StatusInternalServerError {
			span.SetStatus(codes.Error, http.StatusText(status))
		}
		if len(c.Errors) > 0 {
			if err := c.Errors.Last().Err; err != nil {
				span.RecordError(err)
			}
		}
		span.End()
	}
}

// OTLPConfig captures the runtime knobs needed to bootstrap an OTLP HTTP
// trace exporter.
type OTLPConfig struct {
	Endpoint    string            // host:port (no scheme)
	ServiceName string            // service.name resource attribute
	SampleRate  float64           // 0.0–1.0; defaults to 1.0 when zero
	Insecure    bool              // disable TLS
	Headers     map[string]string // additional OTLP headers
}

// OTLPConfigFromEnv reads the standard OTEL_EXPORTER_OTLP_* variables and
// returns the parsed config plus a flag indicating whether tracing should
// be wired up. False means no endpoint is configured — caller should skip
// SetupOTLP entirely.
func OTLPConfigFromEnv() (OTLPConfig, bool) {
	endpoint := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"))
	if endpoint == "" {
		endpoint = strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
	}
	if endpoint == "" {
		return OTLPConfig{}, false
	}

	insecure := strings.HasPrefix(endpoint, "http://")
	if v := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_INSECURE")); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			insecure = b
		}
	}

	cfg := OTLPConfig{
		Endpoint:    stripOTLPScheme(endpoint),
		ServiceName: envOrDefault("OTEL_SERVICE_NAME", "saker"),
		SampleRate:  parseOTLPSampleRate(os.Getenv("OTEL_TRACES_SAMPLER_ARG"), 1.0),
		Insecure:    insecure,
		Headers:     parseOTLPHeaders(os.Getenv("OTEL_EXPORTER_OTLP_HEADERS")),
	}
	return cfg, true
}

// SetupOTLP installs a global TracerProvider with an OTLP HTTP exporter.
// Caller must invoke the returned shutdown func before process exit so
// pending spans are flushed. Calling with an empty Endpoint is a no-op.
func SetupOTLP(ctx context.Context, cfg OTLPConfig) (func(context.Context) error, error) {
	if strings.TrimSpace(cfg.Endpoint) == "" {
		return func(context.Context) error { return nil }, nil
	}
	if cfg.ServiceName == "" {
		cfg.ServiceName = "saker"
	}
	if cfg.SampleRate <= 0 {
		cfg.SampleRate = 1.0
	}

	opts := []otlptracehttp.Option{otlptracehttp.WithEndpoint(cfg.Endpoint)}
	if cfg.Insecure {
		opts = append(opts, otlptracehttp.WithInsecure())
	}
	if len(cfg.Headers) > 0 {
		opts = append(opts, otlptracehttp.WithHeaders(cfg.Headers))
	}

	exporter, err := otlptrace.New(ctx, otlptracehttp.NewClient(opts...))
	if err != nil {
		return nil, fmt.Errorf("create OTLP exporter: %w", err)
	}

	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(semconv.SchemaURL,
			semconv.ServiceNameKey.String(cfg.ServiceName),
		),
	)
	if err != nil {
		_ = exporter.Shutdown(ctx)
		return nil, fmt.Errorf("build OTel resource: %w", err)
	}

	var sampler sdktrace.Sampler
	switch {
	case cfg.SampleRate >= 1.0:
		sampler = sdktrace.AlwaysSample()
	case cfg.SampleRate <= 0:
		sampler = sdktrace.NeverSample()
	default:
		sampler = sdktrace.TraceIDRatioBased(cfg.SampleRate)
	}

	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sampler),
	)
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	return provider.Shutdown, nil
}

func stripOTLPScheme(s string) string {
	s = strings.TrimPrefix(s, "https://")
	s = strings.TrimPrefix(s, "http://")
	return strings.TrimSuffix(s, "/")
}

func envOrDefault(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func parseOTLPSampleRate(s string, def float64) float64 {
	if s == "" {
		return def
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil && f >= 0 && f <= 1 {
		return f
	}
	return def
}

// parseOTLPHeaders parses the W3C-style "k1=v1,k2=v2" header list used by
// OTEL_EXPORTER_OTLP_HEADERS. Empty or malformed pairs are silently skipped.
func parseOTLPHeaders(raw string) map[string]string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	out := map[string]string{}
	for _, pair := range strings.Split(raw, ",") {
		kv := strings.SplitN(pair, "=", 2)
		if len(kv) != 2 {
			continue
		}
		k := strings.TrimSpace(kv[0])
		v := strings.TrimSpace(kv[1])
		if k != "" && v != "" {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
