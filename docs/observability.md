# Observability

Saker ships with three integrated signals:

1. **Prometheus metrics** — exported on `/metrics`
2. **OpenTelemetry traces** — OTLP-HTTP exporter, configurable
3. **Structured logs** — `slog` JSON, request-ID correlated

Everything is wired in by default; no extra dependencies required.

## Metrics

Two collector locations exist:

- **HTTP layer** (`pkg/server/metrics.go`) — gin middleware, registered via
  `PrometheusMiddleware()`; the `/metrics` route is mounted in
  `pkg/server/gin_engine.go:84–90` (gated under auth when the server runs
  in authenticated mode).
- **Runtime layer** (`pkg/metrics/`) — package-level vecs, registered in
  `init()`. Injected at the agent run boundary, the tool registry's
  `Execute`, and via a `model.Model` wrapper applied after failover.

### HTTP

| Metric | Type | Labels | Source |
| --- | --- | --- | --- |
| `saker_http_requests_total` | Counter | `method`, `path`, `status` | `pkg/server/metrics.go:13` |
| `saker_http_request_duration_seconds` | Histogram | `method`, `path` | `pkg/server/metrics.go:21` |
| `saker_websocket_connections_active` | Gauge | — | `pkg/server/metrics.go:30` |

WebSocket lifecycle hooks call `IncWSConnections` / `DecWSConnections`
on connect/disconnect.

### Session / Agent / Tool / Model

All defined in `pkg/metrics/metrics.go`. Labels are bounded to a small
closed set (statuses are `ok`/`error`/`canceled`; `tool` comes from the
registered tool whitelist; `provider`/`model` are bucketed via
`SanitizeProvider`/`SanitizeModel`).

| Metric | Type | Labels |
| --- | --- | --- |
| `saker_session_active` | Gauge | — |
| `saker_agent_runs_total` | Counter | `status` |
| `saker_agent_run_duration_seconds` | Histogram | `status` |
| `saker_tool_invocations_total` | Counter | `tool`, `status` |
| `saker_tool_duration_seconds` | Histogram | `tool` |
| `saker_model_requests_total` | Counter | `provider`, `model`, `mode`, `status` |
| `saker_model_request_duration_seconds` | Histogram | `provider`, `model`, `mode` |
| `saker_model_tokens_total` | Counter | `provider`, `model`, `direction` |

`mode` is `unary` (Complete) or `stream` (CompleteStream); `direction` is
`input`/`output`/`cache_read`/`cache_creation`. The agent run counters
include only runs that successfully passed the session gate — concurrent
rejections are not counted as runs.

#### Useful queries

```promql
# Agent error rate
sum(rate(saker_agent_runs_total{status="error"}[5m]))
  / sum(rate(saker_agent_runs_total[5m]))

# p95 tool latency by tool
histogram_quantile(0.95,
  sum by (tool, le) (rate(saker_tool_duration_seconds_bucket[5m])))

# Token burn per provider/model
sum by (provider, model) (rate(saker_model_tokens_total[5m]))
```

### Scrape configuration

```yaml
# prometheus.yml
scrape_configs:
  - job_name: saker
    metrics_path: /metrics
    scheme: https
    basic_auth:
      username: admin
      password: ${SAKER_AUTH_PASS}
    static_configs:
      - targets: ["saker.internal:10112"]
```

If the server runs without `--auth-user`, drop `basic_auth` and the route
becomes anonymous (only acceptable inside a private network).

### Recommended alerts

```yaml
groups:
  - name: saker
    rules:
      - alert: SakerHighErrorRate
        expr: |
          sum(rate(saker_http_requests_total{status=~"5.."}[5m]))
            / sum(rate(saker_http_requests_total[5m])) > 0.05
        for: 10m
        annotations:
          summary: "Saker 5xx rate >5% for 10m"

      - alert: SakerSlowRequests
        expr: |
          histogram_quantile(0.95,
            sum by (le) (rate(saker_http_request_duration_seconds_bucket[5m]))
          ) > 5
        for: 10m
        annotations:
          summary: "Saker p95 request latency >5s"

      - alert: SakerWebsocketLeak
        expr: saker_websocket_connections_active > 1000
        for: 30m
        annotations:
          summary: "WebSocket connections plateaued — possible leak"
```

## Tracing

OpenTelemetry support is **build-tag gated**. The OTLP exporter and SDK
are compiled in only when the binary is built with `-tags otel`; default
builds use a no-op tracer (`pkg/api/otel_noop.go`) with zero overhead.

```bash
# Build with OTel support
go build -tags otel -o saker ./cmd/saker
```

Configure via `WithOTEL(...)` when constructing the runtime
(`pkg/api/otel_config.go`):

```go
import "github.com/cinience/saker/pkg/api"

opts := api.OTELConfig{
    Enabled:     true,
    ServiceName: "saker",
    Endpoint:    "http://otel-collector.internal:4318",
    Headers:     map[string]string{"Authorization": "Bearer ..."},
    SampleRate:  1.0,
    Insecure:    false,
}
runtime, err := api.New(api.WithOTEL(opts))
```

If `Enabled` is false (default), the no-op tracer remains active even on
`-tags otel` builds.

The tracer emits one root span per `agent.run`, with attributes for
session ID, model name, and stop reason. Tool calls become child spans.

### Common collector setups

```yaml
# otel-collector-config.yaml
receivers:
  otlp:
    protocols:
      http:
        endpoint: 0.0.0.0:4318

processors:
  batch:

exporters:
  otlp/jaeger:
    endpoint: jaeger:4317
    tls: { insecure: true }
  prometheus:
    endpoint: 0.0.0.0:8889   # exposes saker spans as RED metrics

service:
  pipelines:
    traces:
      receivers: [otlp]
      processors: [batch]
      exporters: [otlp/jaeger]
```

## Profiling (pprof)

The Go runtime profiler is exposed at `/debug/pprof/` **only when the
server is started with `--debug`**. The flag prints a startup warning
because the endpoint is unauthenticated; never enable it on a public
listener — bind saker to localhost or put it behind a VPN/SSH tunnel
when you need a snapshot in production.

Routes registered (`pkg/server/gin_engine.go:38-52`):

| Path | Purpose |
| --- | --- |
| `/debug/pprof/` | Index page with links to the named profiles |
| `/debug/pprof/profile?seconds=N` | CPU profile, sampled for N seconds |
| `/debug/pprof/heap` | Live-object heap allocations |
| `/debug/pprof/allocs` | Cumulative allocations since process start |
| `/debug/pprof/goroutine` | Goroutine stack samples (compact) |
| `/debug/pprof/goroutine?debug=2` | Full goroutine stack dump (text) |
| `/debug/pprof/block` | Blocking events (requires runtime tuning) |
| `/debug/pprof/mutex` | Mutex contention (requires runtime tuning) |
| `/debug/pprof/threadcreate` | OS-thread creation sites |
| `/debug/pprof/cmdline` `/symbol` `/trace` | Standard Go runtime helpers |

`block` and `mutex` profiles are **off by default** to avoid runtime
overhead. Enable them only while you're capturing — see the runbook
below for the call sequence.

### Snapshot script

`scripts/pprof-snapshot.sh` captures the full bundle (cpu + heap +
goroutine + allocs + block + mutex + threadcreate + full stacks) into a
timestamped directory:

```bash
# Local dev
./scripts/pprof-snapshot.sh

# Remote, behind basic auth, with a longer CPU window
PPROF_URL=https://saker.internal ./scripts/pprof-snapshot.sh \
  --auth admin:$SAKER_AUTH_PASS \
  --cpu 60 \
  --out ./pprof-prod-$(date -u +%Y%m%d)
```

The script aborts early with a clear message if `--debug` isn't enabled
on the target. CPU profiles block for the sampling window; everything
else returns near-instantly.

### Runbook — when to capture which profile

| Symptom | Profile to grab first | What to look for |
| --- | --- | --- |
| Sustained high CPU (top shows >70% saker) | `cpu` (60s) | Hot functions in `top10` / flame graph; check for unexpected serialization, regex compile loops, JSON marshal of huge payloads |
| RSS grows unboundedly | `heap` over time (≥3 snapshots, 10 min apart) | Diff with `pprof -base`; look for ever-growing `inuse_space` in caches or session stores |
| Allocation pressure (high GC %) | `allocs` | High `alloc_objects` from hot paths — candidates for `sync.Pool` or buffer reuse |
| Hangs / deadlocks / "everything stuck" | `goroutine?debug=2` | Look for piles of goroutines blocked on the same `chan send` / `Mutex.Lock` / `select` site |
| Goroutine count drifting up | `goroutine` (compact) over time | A single creation site dominating — usually a goroutine that never returns from a request handler |
| Slow tail latency, no CPU pressure | `block` + `mutex` (after enabling, see below) | Long blocking durations on shared resources (DB pool, session map, model client) |
| Unexpected OS-thread count | `threadcreate` | Cgo callbacks (sqlite, image codecs) that pin threads |

### Enabling block & mutex profiling

These profiles only collect data after you set non-zero rates. Saker
does not enable them by default because they add per-event overhead —
flip them on for the capture, then off again:

```bash
# Enable for a 5-minute capture window.
curl -s "$URL/debug/pprof/block?seconds=0" > /dev/null   # no-op probe
# Set the runtime rates via a temporary helper or direct admin RPC if added.
# Default Go: runtime.SetBlockProfileRate(1) and runtime.SetMutexProfileFraction(1)
```

If you need this regularly, add a small admin-only RPC that toggles
`runtime.SetBlockProfileRate` / `runtime.SetMutexProfileFraction` so
operators don't have to restart the binary.

### Inspecting the bundle

```bash
# Top consumers — fastest sanity check
go tool pprof -top pprof-*/heap.pgz | head -30
go tool pprof -top pprof-*/cpu.pgz  | head -30

# Interactive flame graph (opens browser)
go tool pprof -http=localhost:8081 pprof-*/cpu.pgz

# Diff two heap snapshots taken 10 min apart
go tool pprof -http=localhost:8081 \
  -base pprof-T0/heap.pgz pprof-T1/heap.pgz

# Read the goroutine stack dump directly (no go tool needed)
less pprof-*/goroutine-stacks.txt
```

For sharing externally, the `.pgz` files are self-contained — recipients
need the matching binary only to resolve unsymbolized frames; with
`go build -trimpath` builds, just the binary path matches.

## Structured logging

`pkg/logging/logger.go` wires `slog` with JSON output by default. The CLI
entry point sets the level via `--log-level` (`debug`, `info`, `warn`, `error`).

Every HTTP request is tagged with `request_id`, generated server-side by
`RequestIDMiddleware` in `pkg/server/middleware_http.go:17`. The ID is
emitted on the `X-Request-ID` response header for log correlation. The
current implementation does not honor a caller-supplied `X-Request-ID` —
each hop generates its own; if you need cross-service trace continuity,
rely on OTel propagation (W3C `traceparent`) instead.

Sample log line:

```json
{
  "time": "2026-05-11T03:14:15Z",
  "level": "INFO",
  "msg": "agent run completed",
  "request_id": "01HXX...",
  "session_id": "sess_42",
  "model": "claude-opus-4-5",
  "duration_ms": 1834,
  "stop_reason": "end_turn",
  "tokens_in": 2310,
  "tokens_out": 487
}
```

## End-to-end flow

```
HTTP request
  └─▶ RequestIDMiddleware  (X-Request-ID set; logger picks it up)
      └─▶ PrometheusMiddleware  (counter + latency histogram)
          └─▶ Auth + RateLimit
              └─▶ Handler
                  └─▶ Agent.Run  ──┬─▶ OTel root span "agent.run"
                                   ├─▶ slog "agent run start"
                                   ├─▶ Tool spans (child)
                                   └─▶ slog "agent run completed"
```

## Production checklist

- [ ] `/metrics` reachable from Prometheus (auth or VPC).
- [ ] Alerts configured for 5xx rate, p95 latency, WS gauge.
- [ ] OTel collector deployed; traces visible in Jaeger / Tempo / equivalent.
- [ ] Log shipper (Vector / Promtail / Fluent Bit) parses JSON and indexes
      `request_id`, `session_id`, `model`.
- [ ] Dashboards include both HTTP RED and WebSocket connection trend.

## Grafana dashboard

A starter dashboard JSON is intentionally not committed yet — once we have
representative production signals we will publish one. For now, the
recommended panels are:

- `sum by (status) (rate(saker_http_requests_total[5m]))` — RED, status breakdown
- `histogram_quantile(0.95, ...)` over `saker_http_request_duration_seconds_bucket`
- `saker_websocket_connections_active` — single-stat + sparkline

## Disabling

- Metrics: comment out the route in `pkg/server/gin_engine.go`. There is
  no runtime flag because the cost is negligible (no labels with high
  cardinality are emitted).
- Tracing: leave `OTELConfig.Enabled` false, or build without `-tags otel`
  (no-op tracer; SDK not compiled in).
- Structured logs: text-vs-JSON formatting is controlled in
  `pkg/logging/logger.go` — change the handler if downstream tooling
  expects line-oriented logs.
