// OpenTelemetry tracer wiring for the runhub publish + persist + fan-out
// hot path. The tracer is fetched from the OTel global TracerProvider on
// every span start so callers don't have to thread an injected tracer
// through every constructor — when no provider has been installed (default
// in unit tests + in deployments without OTLP env vars), the global is
// the built-in noop and the Start/End calls cost a couple of nanoseconds.
//
// Span layout (matches plan F.2):
//
//   runhub.publish        — Run.Publish entry, attributes: run.id, tenant,
//                           event.type, payload.bytes, seq
//   runhub.batch.flush    — batchWriter.flush, attributes: batch.size,
//                           tenants_count, breaker_skipped
//   runhub.store.insert   — InsertEventsBatch, attributes: rows
//   runhub.store.notify   — Per-channel NOTIFY, attributes: channel
//   runhub.fanout         — Run-internal subscriber fan-out, attributes:
//                           subscribers, dropped
//
// Every span name is prefixed with "runhub." so a Jaeger / Tempo operator
// can filter the entire pipeline with one regex; attribute keys follow
// dot.case to match OTel semconv conventions.
package runhub

import (
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

// runhubTracerName is the import-path-style identifier surfaced as
// otel.scope.name on every emitted span. Matches the standard pattern
// used elsewhere in the codebase (pkg/middleware uses
// "github.com/cinience/saker/pkg/middleware") so dashboards filtering by
// scope can pivot per-package.
const runhubTracerName = "github.com/cinience/saker/pkg/runhub"

// runhubTracer fetches the runhub OTel tracer from the global provider.
// Cheaper to fetch on every call than to cache (the global resolution is
// a single map lookup) and avoids the import-cycle risk of a package-
// level var initialised in a different init phase than
// otel.SetTracerProvider. Returns a noop tracer when no provider has
// been installed.
func runhubTracer() trace.Tracer { return otel.Tracer(runhubTracerName) }
